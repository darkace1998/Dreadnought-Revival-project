package main

import (
	"context"
	"crypto/subtle"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/master-server/db"
	"github.com/darkace1998/Dreadnought-Revival-project/master-server/handlers"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

func main() {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})

	dbPath := getenv("DB_PATH", "master.db")
	addr := getenv("ADDR", ":8084")

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

	internalKey := requireInternalKey(log)

	r := mux.NewRouter()
	r.Use(loggingMiddleware(log))

	// Register/Deregister/Heartbeat are only meant to be called by
	// game-manager (and, in a real deployment, the dedicated server
	// binaries it spawns) — not by arbitrary clients. List/Health stay
	// open since the server browser needs to read them unauthenticated.
	write := r.PathPrefix("/servers").Subrouter()
	write.Use(internalKeyMiddleware(internalKey))
	write.HandleFunc("/register", h.Register).Methods(http.MethodPost)
	write.HandleFunc("/{id}", h.Deregister).Methods(http.MethodDelete)
	write.HandleFunc("/{id}/heartbeat", h.Heartbeat).Methods(http.MethodPost)

	r.HandleFunc("/servers", h.List).Methods(http.MethodGet)
	r.HandleFunc("/health", h.Health).Methods(http.MethodGet)
	r.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.WithField("addr", addr).Info("master-server starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Error("listen")
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
	log.Info("shutting down master-server")
	if err := srv.Shutdown(ctx); err != nil {
		log.WithError(err).Warn("shutdown master-server")
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// requireInternalKey refuses to start without a real shared secret for the
// server-registry write endpoints (Register/Deregister/Heartbeat), which
// previously had no authentication at all — any client that could reach
// this service (directly, or via the gateway before that was restricted)
// could register fake servers, deregister real ones, or forge heartbeats
// for any known server id. Falls back to ADMIN_KEY, matching the pattern
// used by mmogbrain's own internal endpoints, so operators don't need a
// second secret to manage.
func requireInternalKey(log *logrus.Logger) string {
	key := os.Getenv("INTERNAL_API_KEY")
	if key == "" {
		key = os.Getenv("ADMIN_KEY")
	}
	if key == "" || key == "changeme-admin-key" {
		log.Fatal(`INTERNAL_API_KEY (or ADMIN_KEY) must be set to a real secret (not empty or the placeholder "changeme-admin-key")`)
	}
	return key
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
