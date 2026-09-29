// Command web-dashboard is the operator web UI for the Dreadnought private
// server stack.
//
// It is a backend-for-frontend: the browser never sees ADMIN_KEY or
// INTERNAL_API_KEY. The operator logs in once with the admin key, the server
// mints a random session token (HttpOnly cookie), and all upstream calls to
// auth-server / legacy-api / mmogbrain / master-server / game-manager /
// dn-dedicated are made server-side. The UI itself is static files embedded
// with go:embed, so this ships as a single binary with no Node toolchain.
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// sessionLifetime mirrors the JWT lifetime used by the rest of the stack.
const sessionLifetime = 24 * time.Hour

// sessions is a minimal in-memory session store. Single-operator tool, no
// persistence needed: a restart simply requires a fresh login.
type sessions struct {
	mu     sync.Mutex
	tokens map[string]time.Time
}

func newSessions() *sessions {
	return &sessions{tokens: make(map[string]time.Time)}
}

func (s *sessions) mint() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	s.mu.Lock()
	s.tokens[token] = time.Now().Add(sessionLifetime)
	s.mu.Unlock()
	return token, nil
}

func (s *sessions) valid(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.tokens[token]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.tokens, token)
		return false
	}
	return true
}

func (s *sessions) revoke(token string) {
	s.mu.Lock()
	delete(s.tokens, token)
	s.mu.Unlock()
}

func (s *sessions) sweep() {
	s.mu.Lock()
	now := time.Now()
	for tok, exp := range s.tokens {
		if now.After(exp) {
			delete(s.tokens, tok)
		}
	}
	s.mu.Unlock()
}

// config holds the dashboard's own settings plus the upstream base URLs.
type config struct {
	addr        string
	adminKey    string
	internalKey string
	authURL     string
	legacyURL   string
	mmogURL     string
	masterURL   string
	gameMgrURL  string
	runDir      string
	certFile    string
}

func loadConfig() config {
	adminKey := os.Getenv("ADMIN_KEY")
	internalKey := os.Getenv("INTERNAL_API_KEY")
	if internalKey == "" {
		internalKey = adminKey
	}
	return config{
		addr:        getenv("DASHBOARD_ADDR", getenv("ADDR", ":8090")),
		adminKey:    adminKey,
		internalKey: internalKey,
		authURL:     getenv("AUTH_URL", "http://127.0.0.1:8081"),
		legacyURL:   getenv("LEGACY_API_URL", "http://127.0.0.1:8082"),
		mmogURL:     getenv("MMOG_URL", "http://127.0.0.1:8083"),
		masterURL:   getenv("MASTER_URL", "http://127.0.0.1:8084"),
		gameMgrURL:  getenv("GAME_MGR_URL", "http://127.0.0.1:8085"),
		runDir:      getenv("RUN_DIR", "run"),
		certFile:    getenv("TLS_CERT", "certs/server.crt"),
	}
}

func main() {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})

	cfg := loadConfig()
	if cfg.adminKey == "" || cfg.adminKey == "changeme-admin-key" {
		log.Fatal(`ADMIN_KEY must be set to a real secret (not empty or the placeholder "changeme-admin-key")`)
	}

	store := newSessions()
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			store.sweep()
		}
	}()

	srv := &server{cfg: cfg, log: log, sessions: store, http: &http.Client{Timeout: 5 * time.Second}}

	r := mux.NewRouter()
	r.Use(loggingMiddleware(log))
	r.HandleFunc("/health", srv.health).Methods(http.MethodGet)
	r.Handle("/metrics", promhttp.Handler()).Methods(http.MethodGet)

	// Session endpoints are unauthenticated by nature (they establish auth).
	r.HandleFunc("/api/login", srv.login).Methods(http.MethodPost)
	r.HandleFunc("/api/logout", srv.logout).Methods(http.MethodPost)
	r.HandleFunc("/api/me", srv.me).Methods(http.MethodGet)

	// Everything else requires a dashboard session or an admin key header
	// (the header path exists for curl, mirroring admin-cli usage).
	api := r.PathPrefix("/api").Subrouter()
	api.Use(srv.requireAuth)
	api.HandleFunc("/status", srv.apiStatus).Methods(http.MethodGet)
	api.HandleFunc("/config", srv.apiConfig).Methods(http.MethodGet)
	api.HandleFunc("/queue", srv.apiQueue).Methods(http.MethodGet)
	api.HandleFunc("/players", srv.apiPlayers).Methods(http.MethodGet)
	api.HandleFunc("/accounts", srv.apiAccounts).Methods(http.MethodGet)
	api.HandleFunc("/instances", srv.apiInstances).Methods(http.MethodGet)
	api.HandleFunc("/instance/{id}", srv.apiInstance).Methods(http.MethodGet)
	api.HandleFunc("/servers", srv.apiServers).Methods(http.MethodGet)
	api.HandleFunc("/chat", srv.apiChat).Methods(http.MethodGet)
	api.HandleFunc("/broadcast", srv.apiBroadcast).Methods(http.MethodPost)
	api.HandleFunc("/online", srv.apiOnline).Methods(http.MethodGet)
	api.HandleFunc("/results", srv.apiResults).Methods(http.MethodGet)
	api.HandleFunc("/bans", srv.apiBans).Methods(http.MethodGet)
	api.HandleFunc("/tiles", srv.apiTiles).Methods(http.MethodGet)
	api.HandleFunc("/tiles", srv.apiUpsertTile).Methods(http.MethodPost)
	api.HandleFunc("/tiles/{id}", srv.apiDeleteTile).Methods(http.MethodDelete)
	api.HandleFunc("/provision", srv.apiProvision).Methods(http.MethodPost)
	api.HandleFunc("/reset", srv.apiReset).Methods(http.MethodPost)
	api.HandleFunc("/player/{id}", srv.apiPlayerDetail).Methods(http.MethodGet)
	api.HandleFunc("/queue/kick/{entry}", srv.apiQueueKick).Methods(http.MethodDelete)
	api.HandleFunc("/queue/clear", srv.apiQueueClear).Methods(http.MethodPost)
	api.HandleFunc("/force-match", srv.apiForceMatch).Methods(http.MethodPost)
	api.HandleFunc("/backups", srv.apiBackups).Methods(http.MethodGet)
	api.HandleFunc("/audit", srv.apiAudit).Methods(http.MethodGet)
	api.HandleFunc("/crashes", srv.apiCrashes).Methods(http.MethodGet)
	api.HandleFunc("/matches", srv.apiMatches).Methods(http.MethodGet)
	api.HandleFunc("/match/{id}", srv.apiMatchDetail).Methods(http.MethodGet)
	api.HandleFunc("/catalog", srv.apiCatalog).Methods(http.MethodGet)
	api.HandleFunc("/history", srv.apiHistory).Methods(http.MethodGet)
	api.HandleFunc("/sessions", srv.apiSessions).Methods(http.MethodGet)
	api.HandleFunc("/sessions/{id}", srv.apiDeleteSession).Methods(http.MethodDelete)
	api.HandleFunc("/player/{id}/progress", srv.apiPlayerProgress).Methods(http.MethodGet)
	api.HandleFunc("/logs", srv.apiLogs).Methods(http.MethodGet)
	api.HandleFunc("/metrics-summary", srv.apiMetricsSummary).Methods(http.MethodGet)
	api.HandleFunc("/grant", srv.apiGrant).Methods(http.MethodPost)
	api.HandleFunc("/grant-all", srv.apiGrantAll).Methods(http.MethodPost)
	api.HandleFunc("/ban", srv.apiBan).Methods(http.MethodPost)
	api.HandleFunc("/unban", srv.apiUnban).Methods(http.MethodPost)
	api.HandleFunc("/stop-instance/{id}", srv.apiStopInstance).Methods(http.MethodPost)

	// Static UI (embedded). Registered last so /api and /health win.
	r.PathPrefix("/").Handler(frontendHandler(log))

	httpSrv := &http.Server{
		Addr:         cfg.addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.WithField("addr", cfg.addr).Info("web-dashboard starting")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("listen")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log.Info("shutting down web-dashboard")
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.WithError(err).Warn("shutdown web-dashboard")
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// checkAdminKey compares in constant time so key length leaks nothing useful.
func checkAdminKey(provided, real string) bool {
	if provided == "" || real == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(real)) == 1
}
