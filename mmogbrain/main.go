//nolint:goconst,gosec,ineffassign,unused,unconvert // Reverse-engineered protocol compatibility code intentionally keeps explicit literals, casts, helper scaffolding, and legacy crypto/TLS details.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dreadnought-ps/mmogbrain/db"
	"github.com/dreadnought-ps/mmogbrain/handlers"
	"github.com/dreadnought-ps/mmogbrain/matchmaker"
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
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})

	dbPath := getenv("DB_PATH", "mmog.db")
	addr := getenv("ADDR", ":8083")
	secret := []byte(getenv("JWT_SECRET", "changeme-dreadnought-jwt-secret"))
	gameMgrURL := getenv("GAME_MGR_URL", "http://127.0.0.1:8085")
	playersPerMatch := 2 // default; override with PLAYERS_PER_MATCH env
	if v, err := strconv.Atoi(os.Getenv("PLAYERS_PER_MATCH")); err == nil && v > 0 {
		playersPerMatch = v
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

	mm := matchmaker.New(database, log, gameMgrURL, playersPerMatch)
	mm.Start()
	defer mm.Stop()

	r := mux.NewRouter()
	r.Use(loggingMiddleware(log))

	// Public
	r.HandleFunc("/health", h.Health).Methods(http.MethodGet)
	r.Handle("/metrics", promhttp.Handler())
	r.HandleFunc("/mmog/chat", h.ChatHistory).Methods(http.MethodGet)

	// Admin endpoints
	adminSub := r.PathPrefix("/admin").Subrouter()
	adminSub.Use(adminKeyMiddleware(getenv("ADMIN_KEY", "changeme-admin-key")))
	adminSub.HandleFunc("/queue", h.AdminQueue).Methods(http.MethodGet)

	// Authenticated
	auth := r.PathPrefix("/mmog").Subrouter()
	auth.Use(jwtMiddleware(secret, log))
	auth.HandleFunc("/queue", h.QueueJoin).Methods(http.MethodPost)
	auth.HandleFunc("/queue/status", h.QueueStatus).Methods(http.MethodGet)
	auth.HandleFunc("/queue", h.QueueLeave).Methods(http.MethodDelete)
	auth.HandleFunc("/match/{id}", h.GetMatch).Methods(http.MethodGet)
	auth.HandleFunc("/chat", h.ChatSend).Methods(http.MethodPost)

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

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
	go startFirmamentServer(shutdownCtx, log, getenv("FIRMAMENT_ADDR", ":48843"), firmamentCert, firmamentKey)

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
