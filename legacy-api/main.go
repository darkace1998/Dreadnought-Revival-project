package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dreadnought-ps/legacy-api/db"
	"github.com/dreadnought-ps/legacy-api/handlers"
	"github.com/dreadnought-ps/shared/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

func main() {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})

	dbPath := getenv("DB_PATH", "legacy.db")
	addr := getenv("ADDR", ":8082")
	secret := []byte(getenv("JWT_SECRET", "changeme-dreadnought-jwt-secret"))

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

	r := mux.NewRouter()
	r.Use(loggingMiddleware(log))

	// Public endpoints (used by launcher before auth)
	r.HandleFunc("/v2/dreadnought/launcher/dn/tiles/", h.Tiles).Methods(http.MethodGet)
	r.HandleFunc("/v2/dreadnought/launcher/dn/tiles/{lang}/", h.Tiles).Methods(http.MethodGet)
	r.HandleFunc("/v2/dreadnought/ageconsent/", h.AgeConsent).Methods(http.MethodGet, http.MethodPost)
	r.HandleFunc("/health", h.Health).Methods(http.MethodGet)
	r.Handle("/metrics", promhttp.Handler())

	// Authenticated endpoints
	auth := r.PathPrefix("/v2/dreadnought").Subrouter()
	auth.Use(jwtMiddleware(secret, log))
	auth.HandleFunc("/player/{id}/profile", h.GetProfile).Methods(http.MethodGet)
	auth.HandleFunc("/player/{id}/inventory", h.GetInventory).Methods(http.MethodGet)
	auth.HandleFunc("/match/result", h.PostMatchResult).Methods(http.MethodPost)

	// Phase 7 — completeness endpoints
	r.HandleFunc("/v2/dreadnought/server/status", h.ServerStatus).Methods(http.MethodGet)
	r.HandleFunc("/v2/dreadnought/techtree", h.TechTree).Methods(http.MethodGet)
	auth.HandleFunc("/store", h.Store).Methods(http.MethodGet)
	auth.HandleFunc("/season", h.Season).Methods(http.MethodGet)
	auth.HandleFunc("/xp/convert", h.XPConvert).Methods(http.MethodPost)
	auth.HandleFunc("/v2/dreadnought/projectiles", h.Projectiles).Methods(http.MethodGet)
auth.HandleFunc("/v2/dreadnought/shipfeats", h.ShipFeats).Methods(http.MethodGet)
auth.HandleFunc("/v2/dreadnought/abilities", h.Abilities).Methods(http.MethodGet)

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.WithField("addr", addr).Info("legacy-api starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("listen")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log.Info("shutting down legacy-api")
	if err := srv.Shutdown(ctx); err != nil {
		log.WithError(err).Warn("shutdown legacy-api")
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func jwtMiddleware(secret []byte, log *logrus.Logger) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
				return
			}
			tokenStr := auth[7:]
			type claims struct {
				UserID   string `json:"sub"`
				Username string `json:"username"`
				jwt.RegisteredClaims
			}
			c := &claims{}
			token, err := jwt.ParseWithClaims(tokenStr, c, func(t *jwt.Token) (interface{}, error) {
				return secret, nil
			})
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
