package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/auth-server/db"
	"github.com/darkace1998/Dreadnought-Revival-project/auth-server/handlers"
	jwtPkg "github.com/darkace1998/Dreadnought-Revival-project/auth-server/jwt"
	"github.com/darkace1998/Dreadnought-Revival-project/shared/middleware"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

func main() {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})
	log.WithField("service", "auth-server")

	dbPath := getenv("DB_PATH", "auth.db")
	addr := getenv("ADDR", ":8081")
	secret := requireJWTSecret(log)

	database, err := db.Open(dbPath)
	if err != nil {
		log.WithError(err).Fatal("open database")
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.WithError(err).Warn("close database")
		}
	}()

	h := &handlers.Handler{DB: database, Log: log, Secret: secret}

	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := database.Exec(`DELETE FROM sessions WHERE expires_at < datetime('now')`); err != nil {
				log.WithError(err).Warn("session cleanup")
			}
		}
	}()

	r := mux.NewRouter()
	r.Use(loggingMiddleware(log))

	// Login/register are unauthenticated by nature, so they're the
	// brute-force-relevant surface — rate-limit per IP.
	loginLimiter := middleware.NewRateLimiter(10, time.Minute)

	// Dreadnought profile-api compatible endpoints
	r.Handle("/auth/", loginLimiter.Middleware(http.HandlerFunc(h.Login))).Methods(http.MethodPost)
	r.Handle("/auth/register", loginLimiter.Middleware(http.HandlerFunc(h.Register))).Methods(http.MethodPost)
	r.HandleFunc("/auth/me", jwtMiddleware(secret, database, h.Me)).Methods(http.MethodGet)
	r.HandleFunc("/auth/logout", h.Logout).Methods(http.MethodPost)
	r.HandleFunc("/health", h.Health).Methods(http.MethodGet)
	r.Handle("/metrics", promhttp.Handler())

	// Admin endpoints (protected by X-Admin-Key header middleware)
	admin := r.PathPrefix("/admin").Subrouter()
	admin.Use(adminKeyMiddleware(requireAdminKey(log)))
	admin.HandleFunc("/ban", h.AdminBan).Methods(http.MethodPost)
	admin.HandleFunc("/unban", h.AdminUnban).Methods(http.MethodPost)

	// Internal endpoints (protected by X-Internal-Key header middleware) —
	// for other services to check session revocation state, not for players.
	internalRoutes := r.PathPrefix("/internal").Subrouter()
	internalRoutes.Use(internalKeyMiddleware(requireInternalKey(log)))
	internalRoutes.HandleFunc("/session/valid", h.ValidateSession).Methods(http.MethodPost)

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.WithField("addr", addr).Info("auth-server starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("listen")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log.Info("shutting down auth-server")
	if err := srv.Shutdown(ctx); err != nil {
		log.WithError(err).Warn("shutdown auth-server")
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// requireJWTSecret refuses to start with the well-known placeholder JWT
// signing secret committed in this repo's source — silently falling back to
// it would let anyone who has read the source mint arbitrary valid JWTs.
func requireJWTSecret(log *logrus.Logger) []byte {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" || secret == "changeme-dreadnought-jwt-secret" {
		log.Fatal(`JWT_SECRET must be set to a real secret (not empty or the placeholder "changeme-dreadnought-jwt-secret")`)
	}
	return []byte(secret)
}

// requireAdminKey refuses to start with the well-known placeholder admin
// key committed in this repo's source — silently falling back to it would
// let anyone who has read the source authenticate to every /admin endpoint.
func requireAdminKey(log *logrus.Logger) string {
	key := os.Getenv("ADMIN_KEY")
	if key == "" || key == "changeme-admin-key" {
		log.Fatal(`ADMIN_KEY must be set to a real secret (not empty or the placeholder "changeme-admin-key")`)
	}
	return key
}

// requireInternalKey refuses to start without a real shared secret for
// service-to-service endpoints (currently just session validation). Falls
// back to ADMIN_KEY, matching the pattern used elsewhere in this codebase.
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

func jwtMiddleware(secret []byte, database *sql.DB, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		tokenStr, ok := jwtPkg.ExtractBearerToken(authHeader)
		if !ok {
			http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
			return
		}
		claims, err := jwtPkg.Parse(secret, tokenStr)
		if err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		if claims.UserID == "" {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}

		sum := sha256.Sum256([]byte(tokenStr))
		tokenHash := fmt.Sprintf("%x", sum[:])
		var found int
		if err := database.QueryRow(
			`SELECT COUNT(*) FROM sessions WHERE token_hash=? AND datetime(expires_at) > datetime('now')`,
			tokenHash,
		).Scan(&found); err != nil || found == 0 {
			http.Error(w, `{"error":"token revoked"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), middleware.UserIDKey, claims.UserID)
		ctx = context.WithValue(ctx, middleware.UsernameKey, claims.Username)
		r = r.WithContext(ctx)
		r.Header.Set("X-User-ID", claims.UserID)
		r.Header.Set("X-Username", claims.Username)
		next(w, r)
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
