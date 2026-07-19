package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
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

	h := &handlers.Handler{DB: database, Log: log}

	r := mux.NewRouter()
	r.Use(loggingMiddleware(log))

	// Public endpoints (used by launcher before auth)
	r.HandleFunc("/v2/dreadnought/launcher/dn/tiles/", h.Tiles).Methods(http.MethodGet)
	r.HandleFunc("/v2/dreadnought/launcher/dn/tiles/{lang}/", h.Tiles).Methods(http.MethodGet)
	r.HandleFunc("/v2/dreadnought/ageconsent/", h.AgeConsent).Methods(http.MethodGet, http.MethodPost)
	r.HandleFunc("/health", h.Health).Methods(http.MethodGet)
	r.Handle("/metrics", promhttp.Handler())

	internalKey := requireInternalKey(log)

	sessionChecker := &sessionChecker{
		authServerURL: getenv("AUTH_SERVER_URL", "http://127.0.0.1:8081"),
		internalKey:   internalKey,
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}

	// Authenticated endpoints
	auth := r.PathPrefix("/v2/dreadnought").Subrouter()
	auth.Use(jwtMiddleware(secret, log, sessionChecker))
	auth.HandleFunc("/player/{id}/profile", h.GetProfile).Methods(http.MethodGet)
	auth.HandleFunc("/player/{id}/inventory", h.GetInventory).Methods(http.MethodGet)

	// Match-result reporting mutates player stats/XP on behalf of an entire
	// match roster and was previously reachable with any ordinary player's
	// JWT (same aud=dreadnought issued at login) — nothing distinguished a
	// trusted match-reporting caller from any logged-in player. Require the
	// same internal service credential used elsewhere, not a player token.
	internalRoutes := r.PathPrefix("/v2/dreadnought").Subrouter()
	internalRoutes.Use(internalKeyMiddleware(internalKey))
	internalRoutes.HandleFunc("/match/result", h.PostMatchResult).Methods(http.MethodPost)

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

// requireInternalKey refuses to start without a real shared secret for
// service-to-service endpoints (currently just match-result reporting).
// Falls back to ADMIN_KEY, matching the pattern used by mmogbrain/
// master-server/game-manager's own internal endpoints.
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

func jwtMiddleware(secret []byte, log *logrus.Logger, sessionChecker *sessionChecker) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			const bearerPrefix = "Bearer "
			if !strings.HasPrefix(auth, bearerPrefix) {
				http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
				return
			}
			tokenStr := strings.TrimPrefix(auth, bearerPrefix)
			if tokenStr == "" {
				http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
				return
			}
			type claims struct {
				UserID   string `json:"sub"`
				Username string `json:"username"`
				jwt.RegisteredClaims
			}
			c := &claims{}
			token, err := jwt.ParseWithClaims(tokenStr, c, func(t *jwt.Token) (interface{}, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
				}
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
		// A signature/expiry-valid JWT doesn't mean the session is still
		// live — auth-server tracks logout/ban revocation via its own
		// sessions table, which legacy-api has no visibility into. Ask
		// auth-server directly rather than silently accepting any
		// not-yet-expired token for its full lifetime after logout/ban.
		valid, err := sessionChecker.isValid(tokenStr)
		if err != nil {
			log.WithError(err).Warn("session validation check failed")
			http.Error(w, `{"error":"session validation unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		if !valid {
			http.Error(w, `{"error":"session revoked"}`, http.StatusUnauthorized)
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

// sessionChecker calls auth-server's internal session-validation endpoint
// so legacy-api can honor logout/ban revocation, not just signature/expiry.
type sessionChecker struct {
	authServerURL string
	internalKey   string
	httpClient    *http.Client
}

func (sc *sessionChecker) isValid(token string) (bool, error) {
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequest(http.MethodPost, sc.authServerURL+"/internal/session/valid", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", sc.internalKey)
	resp, err := sc.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("auth-server returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Valid bool `json:"valid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}
	return result.Valid, nil
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
