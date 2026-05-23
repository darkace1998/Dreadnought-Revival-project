package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dreadnought-ps/game-manager/portpool"
	"github.com/dreadnought-ps/game-manager/spawner"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

const fieldStatus = "status"

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})

	addr := getenv("ADDR", ":8085")
	gameBinary := getenv("GAME_BINARY", "/src/Dreadnought/DreadGame/DreadGame/Binaries/Win64/DreadGame-Win64-Shipping.exe")
	wineExe := getenv("WINE_EXE", "wine")
	masterURL := getenv("MASTER_URL", "http://127.0.0.1:8084")
	serverIP := getenv("SERVER_IP", "127.0.0.1")
	portStart := 7777
	portEnd := 7877

	pool := portpool.New(portStart, portEnd)
	sp := spawner.New(gameBinary, wineExe, masterURL, serverIP, log, pool.Release)

	r := mux.NewRouter()
	r.Use(loggingMiddleware(log))

	// Launch a new match instance
	r.HandleFunc("/instances", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<17) // 128KB limit
		var req struct {
			GameMode string   `json:"game_mode"`
			Map      string   `json:"map"`
			Players  []string `json:"players"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if len(req.Players) > 20 {
			http.Error(w, `{"error":"too many players"}`, http.StatusBadRequest)
			return
		}
		if req.GameMode == "" {
			req.GameMode = "TeamDeathmatch"
		}
		if req.Map == "" {
			req.Map = "Charon"
		}
		port, err := pool.Acquire()
		if err != nil {
			http.Error(w, `{"error":"no ports available"}`, http.StatusServiceUnavailable)
			return
		}
		inst, err := sp.Launch(req.GameMode, req.Map, port, req.Players)
		if err != nil {
			pool.Release(port)
			http.Error(w, `{"error":"launch failed"}`, http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"instance_id": inst.ID,
			"match_id":    inst.MatchID,
			"ip":          serverIP,
			"port":        port,
			"game_mode":   inst.GameMode,
			"map":         inst.Map,
		})
	}).Methods(http.MethodPost)

	// List running instances
	r.HandleFunc("/instances", func(w http.ResponseWriter, r *http.Request) {
		instances := sp.List()
		type instView struct {
			ID        string   `json:"id"`
			MatchID   string   `json:"match_id"`
			Port      int      `json:"port"`
			GameMode  string   `json:"game_mode"`
			Map       string   `json:"map"`
			Players   []string `json:"players"`
			StartedAt string   `json:"started_at"`
		}
		views := make([]instView, 0, len(instances))
		for _, inst := range instances {
			views = append(views, instView{
				ID:        inst.ID,
				MatchID:   inst.MatchID,
				Port:      inst.Port,
				GameMode:  inst.GameMode,
				Map:       inst.Map,
				Players:   inst.Players,
				StartedAt: inst.StartedAt.Format(time.RFC3339),
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"instances":  views,
			"count":      len(views),
			"ports_used": pool.InUse(),
		})
	}).Methods(http.MethodGet)

	// Stop a specific instance
	r.HandleFunc("/instances/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		if err := sp.Stop(id); err != nil {
			http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "stopped"})
	}).Methods(http.MethodDelete)

	r.Handle("/metrics", promhttp.Handler())
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			fieldStatus:  "ok",
			"service":    "game-manager",
			"instances":  len(sp.List()),
			"ports_used": pool.InUse(),
		})
	}).Methods(http.MethodGet)

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.WithField("addr", addr).Info("game-manager starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("listen")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log.Info("shutting down game-manager")
	sp.Shutdown()
	if err := srv.Shutdown(ctx); err != nil {
		log.WithError(err).Warn("shutdown game-manager")
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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
