package main

import (
	"context"
	"crypto/subtle"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/master-master/db"
	"github.com/darkace1998/Dreadnought-Revival-project/master-master/handlers"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

func main() {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})

	dbPath := getenv("DB_PATH", "master-master.db")
	addr := getenv("ADDR", ":8091")
	// The admin panel listens separately from cluster traffic, on its own
	// port and (like everything here) on every interface. Operators who
	// want it loopback-only set ADMIN_ADDR=127.0.0.1:8092.
	adminAddr := getenv("ADMIN_ADDR", ":8092")

	database, err := db.Open(dbPath)
	if err != nil {
		log.WithError(err).Fatal("open database")
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.WithError(err).Warn("close database")
		}
	}()

	h := &handlers.Handler{DB: database, Log: log}
	h.StartCleanup()

	// Operator dashboard (you only): embedded admin page + JSON API behind
	// HTTP Basic auth (MASTER_ADMIN_PASSWORD). Through it you see every
	// cluster including offline/blocked ones, edit MOTDs, and block or
	// remove clusters.
	adminPassword := requireAdminPassword(log)

	apiSrv := &http.Server{
		Addr:         addr,
		Handler:      newAPIRouter(h, log),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	adminSrv := &http.Server{
		Addr:         adminAddr,
		Handler:      newAdminRouter(h, adminPassword, log),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		log.WithField("addr", addr).Info("master-master starting")
		if err := apiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Error("listen")
			errCh <- err
		}
	}()
	go func() {
		log.WithField("addr", adminAddr).Info("master-master admin panel starting")
		if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Error("listen admin")
			errCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-quit:
	case err := <-errCh:
		log.WithError(err).Fatal("server startup failed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log.Info("shutting down master-master")
	if err := apiSrv.Shutdown(ctx); err != nil {
		log.WithError(err).Warn("shutdown master-master")
	}
	if err := adminSrv.Shutdown(ctx); err != nil {
		log.WithError(err).Warn("shutdown master-master admin")
	}
}

// newAPIRouter serves cluster traffic: open registration, heartbeats,
// public browser list, health and metrics.
func newAPIRouter(h *handlers.Handler, log *logrus.Logger) http.Handler {
	r := mux.NewRouter()
	r.Use(loggingMiddleware(log))

	// Cluster writes are intentionally open: any cluster can list itself
	// without asking the directory operator for a key first. Abuse (fake
	// clusters, forged counts) is handled by removal, not by prevention:
	// the admin dashboard blocks names, and a blocked name is refused at
	// register time. The browser list stays public — it is directory data
	// (names, addresses, a CA certificate), never secrets.
	r.HandleFunc("/clusters/register", h.Register).Methods(http.MethodPost)
	r.HandleFunc("/clusters/{id}", h.Deregister).Methods(http.MethodDelete)
	r.HandleFunc("/clusters/{id}/heartbeat", h.Heartbeat).Methods(http.MethodPost)

	r.HandleFunc("/clusters", h.List).Methods(http.MethodGet)
	r.HandleFunc("/health", h.Health).Methods(http.MethodGet)
	r.Handle("/metrics", promhttp.Handler())

	// Account roaming (stage 3): clusters push snapshots and pull everyone
	// else's. Authentication is per-cluster (X-Sync-Key against the stored
	// hash), checked inside the handlers — there is deliberately no shared
	// key for the whole mesh.
	r.HandleFunc("/sync/push", h.SyncPush).Methods(http.MethodPost)
	r.HandleFunc("/sync/pull", h.SyncPull).Methods(http.MethodGet)
	// Launcher presence check: public (see SyncPresence for the reasoning).
	r.HandleFunc("/presence/{user_id}", h.SyncPresence).Methods(http.MethodGet)
	return r
}

// newAdminRouter serves the operator dashboard and its JSON API, everything
// behind HTTP Basic auth. Nothing here is reachable through the cluster
// listener, and nothing cluster-facing is reachable here.
func newAdminRouter(h *handlers.Handler, password string, log *logrus.Logger) http.Handler {
	r := mux.NewRouter()
	r.Use(loggingMiddleware(log))
	r.HandleFunc("/health", h.Health).Methods(http.MethodGet)
	admin := r.PathPrefix("/admin").Subrouter()
	admin.Use(adminBasicAuth(password))
	admin.HandleFunc("", serveAdminPage).Methods(http.MethodGet)
	admin.HandleFunc("/", serveAdminPage).Methods(http.MethodGet)
	admin.HandleFunc("/api/clusters", h.AdminListAll).Methods(http.MethodGet)
	admin.HandleFunc("/api/clusters/{id}/motd", h.AdminSetMOTD).Methods(http.MethodPost)
	admin.HandleFunc("/api/clusters/{id}/block", h.AdminBlock).Methods(http.MethodPost)
	admin.HandleFunc("/api/clusters/{id}/unblock", h.AdminUnblock).Methods(http.MethodPost)
	admin.HandleFunc("/api/clusters/{id}", h.AdminDelete).Methods(http.MethodDelete)
	admin.HandleFunc("/api/clusters/{id}/secret", h.AdminSecret).Methods(http.MethodPost)
	admin.HandleFunc("/api/synclog", h.AdminSyncLog).Methods(http.MethodGet)
	admin.HandleFunc("/api/syncusers", h.AdminSyncUsers).Methods(http.MethodGet)
	admin.HandleFunc("/api/clusters/{id}/secret", h.AdminSecret).Methods(http.MethodPost)
	admin.HandleFunc("/api/synclog", h.AdminSyncLog).Methods(http.MethodGet)
	admin.HandleFunc("/api/syncusers", h.AdminSyncUsers).Methods(http.MethodGet)
	return r
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// requireAdminPassword refuses to start without the operator password for
// the embedded admin dashboard. This is the only secret this service has.
func requireAdminPassword(log *logrus.Logger) string {
	pass := os.Getenv("MASTER_ADMIN_PASSWORD")
	if pass == "" || pass == "changeme-admin-password" {
		log.Fatal(`MASTER_ADMIN_PASSWORD must be set to a real password (not empty or the placeholder) — it guards the admin dashboard`)
	}
	return pass
}

func adminBasicAuth(password string) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, pass, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="master-master admin"`)
				http.Error(w, `{"error":"admin auth required"}`, http.StatusUnauthorized)
				return
			}
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
				"method":  r.Method,
				"path":    r.URL.Path,
				"status":  rw.status,
				"latency": time.Since(start).Milliseconds(),
			}).Info("request")
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
