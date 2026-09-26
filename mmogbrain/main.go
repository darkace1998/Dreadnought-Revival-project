package main

import (
	"context"
	"crypto/subtle"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/db"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/handlers"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/matchmaker"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/darkace1998/Dreadnought-Revival-project/shared/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

const (
	fieldMethod = "method"
	fieldPath   = "path"
	fieldStatus = "status"
)

func main() {
	if maybeRunSubcommand() {
		return
	}
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})
	installLogRedaction(log) // no player credentials in any log (log_redact.go)

	// 0600: the frame log is the most detailed record of player sessions.
	logFile, err := os.OpenFile("mmogbrain.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		log.SetOutput(logFile)
	}

	dbPath := getenv("DB_PATH", "mmog.db")
	addr := getenv("ADDR", ":8083")
	secret := requireJWTSecret(log)
	setMmogJWTSecret(secret)
	gameMgrURL := getenv("GAME_MGR_URL", "http://127.0.0.1:8085")
	playersPerMatch := 2 // default; override with PLAYERS_PER_MATCH env
	if v, err := strconv.Atoi(os.Getenv("PLAYERS_PER_MATCH")); err == nil && v > 0 {
		playersPerMatch = v
	}
	if playersPerMatch == 1 && os.Getenv("DN_MATCH_AUTOSCALE") == "0" {
		// Worth shouting about now that players can actually spawn. With 1, a
		// match forms the moment ANYONE queues, so two people queueing together
		// get two separate battle servers and can never meet. Combined with a
		// PvP mode -- which has no bots at all; our host logs only ever show
		// AYAICombatSceneManager::StartCombat under Training Match -- that is an
		// empty map with nothing to do in it, reported as a server bug in
		// AGENT-CHAT C25.6 when it is this setting.
		log.Warn("PLAYERS_PER_MATCH=1: every player gets a PRIVATE match and " +
			"will never meet another player. Set it to 2 or more for PvP.")
	}

	database, err := db.Open(dbPath)
	if err != nil {
		log.WithError(err).Fatal("open database")
	}
	setMmogPlayerStateDB(database)
	defer func() {
		if err := database.Close(); err != nil {
			log.WithError(err).Warn("close database")
		}
	}()

	h := &handlers.Handler{DB: database, Log: log}

	// game-manager's /instances route requires the shared secret; without it
	// every formed match came back 403 and rolled straight back into the queue.
	internalKey := os.Getenv("INTERNAL_API_KEY")
	if internalKey == "" {
		internalKey = os.Getenv("ADMIN_KEY")
	}
	if internalKey == "" {
		log.Warn("neither INTERNAL_API_KEY nor ADMIN_KEY is set; game-manager will reject match requests with 403")
	}
	mm := matchmaker.New(database, log, gameMgrURL, internalKey, playersPerMatch)
	configureMatchAutoscale(mm, log)
	mm.Start()
	defer mm.Stop()

	adminKey := requireAdminKey(log)
	r := newRouter(h, secret, adminKey, getenv("INTERNAL_API_KEY", adminKey), log)

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Say out loud which behaviour switches are in force. An A/B against a
	// running server has now silently measured nothing twice -- once because a
	// switch is read inside a sync.Once and needs a restart, once because the
	// binary predated the change and the variable was never set. Neither was
	// visible from outside the process, which is the same hidden-failure shape
	// as everything else in CONTRIBUTING.md's list.
	logTechTreeSwitches(log)

	go func() {
		log.WithField("addr", addr).Info("mmogbrain starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("listen")
		}
	}()

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	// YFirmament WebSocket server — the game connects here for the handshake/auth/chat protocol.
	// Protocol (derived from Ghidra decompile of DreadGame-Win64-Shipping.exe):
	//   1. Server → Client: JSON with status=="connection_successful" → game state=1
	//   2. Client → Server: JWT refresh token auth payload
	//   3. Server → Client: JSON with status=="success" → game state=3 (handshake complete)
	//   4. Keep-alive: respond to JSON-RPC {"method":"ping"} calls.
	// Serves TLS (WSS) when FIRMAMENT_CERT/FIRMAMENT_KEY are set; the game does a TLS
	// certificate check (confirmed via Ghidra: FUN_142aa3e00 "Firmament TLS Certificate check").
	firmamentCert := getenv("FIRMAMENT_CERT", "")
	firmamentKey := getenv("FIRMAMENT_KEY", "")
	go startFirmamentServer(shutdownCtx, log, getenv("FIRMAMENT_ADDR", ":48843"), firmamentCert, firmamentKey, secret)

	// Gateway HTTPS server — the game sends REST API calls here for login, session, catalog, etc.
	// Protocol confirmed from game logs: POST /api/v1/authentication/login with Bearer JWT.
	gatewayCert := getenv("GATEWAY_CERT", getenv("FIRMAMENT_CERT", ""))
	gatewayKey := getenv("GATEWAY_KEY", getenv("FIRMAMENT_KEY", ""))
	go startGatewayServer(shutdownCtx, log, getenv("GATEWAY_ADDR", ":65443"), gatewayCert, gatewayKey, secret)

	go startGatewaySessionCleanup(shutdownCtx, log)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down mmogbrain")
	shutdownCancel()

	httpCtx, httpCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer httpCancel()
	if err := srv.Shutdown(httpCtx); err != nil {
		log.WithError(err).Warn("shutdown mmogbrain")
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// requireAdminKey refuses to start with the well-known placeholder admin
// key committed in this repo's source — silently falling back to it would
// let anyone who has read the source authenticate to every /admin and
// /internal endpoint.
func requireAdminKey(log *logrus.Logger) string {
	key := os.Getenv("ADMIN_KEY")
	if key == "" || key == "changeme-admin-key" {
		log.Fatal(`ADMIN_KEY must be set to a real secret (not empty or the placeholder "changeme-admin-key")`)
	}
	return key
}

// requireJWTSecret refuses to start with the well-known placeholder JWT
// signing secret committed in this repo's source — silently falling back to
// it would let anyone who has read the source mint arbitrary valid JWTs and
// bypass authentication on both the HTTP gateway and the Firmament server.
func requireJWTSecret(log *logrus.Logger) []byte {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" || secret == "changeme-dreadnought-jwt-secret" {
		log.Fatal(`JWT_SECRET must be set to a real secret (not empty or the placeholder "changeme-dreadnought-jwt-secret")`)
	}
	return []byte(secret)
}

func jwtMiddleware(secret []byte, log *logrus.Logger) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			const bearerPrefix = "Bearer "
			if len(auth) < len(bearerPrefix) || !strings.EqualFold(auth[:len(bearerPrefix)], bearerPrefix) {
				http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
				return
			}
			tokenStr := strings.TrimSpace(auth[len(bearerPrefix):])
			if tokenStr == "" {
				http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
				return
			}
			type claims struct {
				UserID   string `json:"user_id"`
				Username string `json:"username"`
				jwt.RegisteredClaims
			}
			c := &claims{}
			token, err := jwt.ParseWithClaims(tokenStr, c, func(t *jwt.Token) (interface{}, error) {
				return secret, nil
			}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(protocol.GatewayJWTIssuer), jwt.WithLeeway(time.Minute))
			if err != nil || !token.Valid {
				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
				return
			}
			hasAud := false
			for _, a := range c.Audience {
				if a == "dreadnought" {
					hasAud = true
					break
				}
			}
			if !hasAud {
				http.Error(w, `{"error":"invalid audience"}`, http.StatusUnauthorized)
				return
			}
			if c.RegisteredClaims.Issuer != protocol.GatewayJWTIssuer {
				http.Error(w, `{"error":"invalid issuer"}`, http.StatusUnauthorized)
				return
			}
			if c.UserID == "" {
				c.UserID = c.Subject
			}
			ctx := context.WithValue(r.Context(), middleware.UserIDKey, c.UserID)
			ctx = context.WithValue(ctx, middleware.UsernameKey, c.Username)
			r = r.WithContext(ctx)
			r.Header.Set("X-User-ID", c.UserID)
			r.Header.Set("X-Username", c.Username)
			next.ServeHTTP(w, r)
		})
	}
}

func loggingMiddleware(log *logrus.Logger) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(rw, r)
			log.WithFields(logrus.Fields{
				fieldMethod: r.Method,
				fieldPath:   r.URL.Path,
				fieldStatus: rw.status,
				"latency":   time.Since(start).Milliseconds(),
			}).Info("request")
		})
	}
}

func internalKeyMiddleware(key string) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			provided := r.Header.Get("X-Internal-Key")
			if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(key)) != 1 {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func adminKeyMiddleware(key string) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Admin-Key") != key {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

const defaultMmogPlayerPID = "00000000000000000000000000000001"

// techTreeSwitches are the env switches that change what the tech tree looks
// like on the wire. They are listed in one place so a running server can be
// asked what it is actually doing, rather than inferred from what somebody
// meant to set.
var techTreeSwitches = []string{
	"DN_TECHTREE_SELF_CLASSID",
	"DN_TECHTREE_NO_UNTIERED_ABILITIES",
	"DN_TECHTREE_UNGATED",
	"DN_TECHTREE_LIMIT",
	"DN_TECHTREE_NO_MODULES",
}

// logTechTreeSwitches prints the build stamp and every tech-tree switch at
// startup, so "did the experiment actually run?" is answerable from the log.
func logTechTreeSwitches(log *logrus.Logger) {
	fields := logrus.Fields{"build": buildStamp()}
	on := 0
	for _, name := range techTreeSwitches {
		v := os.Getenv(name)
		fields[name] = v
		if v != "" && v != "0" {
			on++
		}
	}
	entry := log.WithFields(fields)
	if on == 0 {
		entry.Info("tech tree switches: all default")
		return
	}
	entry.Warn("tech tree switches ACTIVE -- this server is not serving default data")
}

// buildStamp reports when this executable was built, which is the other half of
// the question: a switch cannot take effect in a binary that predates it.
func buildStamp() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return "unknown"
	}
	return fi.ModTime().UTC().Format(time.RFC3339)
}

// newRouter registers every HTTP route. Split out of main so the route table --
// in particular what a PLAYER token can reach -- is testable.
func newRouter(h *handlers.Handler, secret []byte, adminKey, internalAPIKey string, log *logrus.Logger) *mux.Router {
	r := mux.NewRouter()
	r.Use(loggingMiddleware(log))

	// Public
	r.HandleFunc("/health", h.Health).Methods(http.MethodGet)
	r.Handle("/metrics", promhttp.Handler())
	r.HandleFunc("/mmog/chat", h.ChatHistory).Methods(http.MethodGet)
	// Battle-server mod: a player's loadout by the id their client picked.
	// Loopback only; see battle_loadout.go.
	r.HandleFunc("/battle/loadout", battleLoadoutHandler).Methods(http.MethodGet)
	// Battle-server mod: a player's match result, once per match. Loopback
	// only; see battle_result.go.
	r.HandleFunc("/battle/result", battleResultHandler).Methods(http.MethodGet)

	// Admin endpoints
	adminSub := r.PathPrefix("/admin").Subrouter()
	adminSub.Use(adminKeyMiddleware(adminKey))
	adminSub.HandleFunc("/queue", h.AdminQueue).Methods(http.MethodGet)
	adminSub.HandleFunc("/players", h.AdminPlayers).Methods(http.MethodGet)
	adminSub.HandleFunc("/grant", h.AdminGrant).Methods(http.MethodPost)

	// Authenticated
	auth := r.PathPrefix("/mmog").Subrouter()
	auth.Use(jwtMiddleware(secret, log))
	auth.HandleFunc("/queue", h.QueueJoin).Methods(http.MethodPost)
	auth.HandleFunc("/queue/status", h.QueueStatus).Methods(http.MethodGet)
	auth.HandleFunc("/queue", h.QueueLeave).Methods(http.MethodDelete)
	auth.HandleFunc("/match/{id}", h.GetMatch).Methods(http.MethodGet)
	auth.HandleFunc("/chat", h.ChatSend).Methods(http.MethodPost)
	// NO /mmog/progression here. It used to be registered on this JWT
	// subrouter, where any logged-in player could POST {"user_id": <anyone>,
	// "xp": <any>} and grant XP, rank, season/ship XP and credits to any
	// account (the handler trusts the body's user_id). Nothing called it.
	// Progression is awarded only through the key-guarded /internal route.
	internal := r.PathPrefix("/internal").Subrouter()
	internal.Use(internalKeyMiddleware(internalAPIKey))
	internal.HandleFunc("/progression", h.UpdateProgression).Methods(http.MethodPost)
	return r
}

// configureMatchAutoscale sizes matches by who is around instead of a fixed
// PLAYERS_PER_MATCH: a match waits for every player who is online and not in a
// battle (capped at DN_MATCH_MAX_PLAYERS, default 10 = the host's -maxplayers),
// or DN_MATCH_MAX_WAIT (default 60s) after its first player queued. A lone
// player still starts at once; two players online land in ONE match instead of
// two private ones. DN_MATCH_AUTOSCALE=0 restores the fixed size.
func configureMatchAutoscale(mm *matchmaker.Matchmaker, log *logrus.Logger) {
	if os.Getenv("DN_MATCH_AUTOSCALE") == "0" {
		log.WithField("players_per_match", mm.PlayersPerMatch).Info("matchmaker: fixed match size (DN_MATCH_AUTOSCALE=0)")
		return
	}
	maxPlayers := 10
	if v, err := strconv.Atoi(os.Getenv("DN_MATCH_MAX_PLAYERS")); err == nil && v > 0 {
		maxPlayers = v
	}
	maxWait := 60 * time.Second
	if v, err := time.ParseDuration(os.Getenv("DN_MATCH_MAX_WAIT")); err == nil && v >= 0 {
		maxWait = v
	}
	mm.PlayersPerMatch = maxPlayers
	mm.MaxWait = maxWait
	mm.OnlinePlayers = socialHubInstance.onlinePlayerIDs
	log.WithFields(logrus.Fields{"max_players": maxPlayers, "max_wait": maxWait}).
		Info("matchmaker: auto-scaled match size (online players not in a battle)")
}
