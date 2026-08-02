package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"strings"

	"github.com/darkace1998/Dreadnought-Revival-project/game-manager/portpool"
	"github.com/darkace1998/Dreadnought-Revival-project/game-manager/spawner"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

const fieldStatus = "status"

// gameModeMapPattern restricts game_mode/map to alphanumeric/underscore
// before they're interpolated into the Wine-spawned dedicated server's
// argv (spawner.Launch builds "-GameMode=%s"/"-Map=%s" directly from these
// values). exec.Command never invokes a shell, so classic shell-metachar
// injection isn't reachable, but Wine's CreateProcess emulation
// reconstructs a single Win32 command-line string from the argv slice
// using Windows quoting rules — an unsanitized value containing a `"` or
// other quoting-sensitive character could break out of its token and
// inject additional engine command-line flags into the spawned process.
var gameModeMapPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// maxPlayersPerInstance bounds the player list accepted on instance creation.
// Production match size caps at 10 players (see spawner's "-maxplayers=10"
// and master-server's default max_players); allow up to 2x that as headroom
// without permitting an unbounded payload (M13).
const maxPlayersPerInstance = 20

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
	portStart := getenvInt("PORT_RANGE_START", 7777)
	portEnd := getenvInt("PORT_RANGE_END", 7877)
	internalKey := requireInternalKey(log)

	pool := portpool.New(portStart, portEnd)
	sp := spawner.New(gameBinary, wineExe, masterURL, serverIP, internalKey, log, pool.Release)

	r := mux.NewRouter()
	r.Use(loggingMiddleware(log))

	write := r.NewRoute().Subrouter()
	write.Use(internalKeyMiddleware(internalKey))

	// Launch a new match instance
	write.HandleFunc("/instances", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<17) // 128KB limit
		var req struct {
			GameMode string   `json:"game_mode"`
			Map      string   `json:"map"`
			MapPath  string   `json:"map_path"`
			Players  []string `json:"players"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if len(req.Players) == 0 {
			http.Error(w, `{"error":"players list is required and cannot be empty"}`, http.StatusBadRequest)
			return
		}
		if len(req.Players) > maxPlayersPerInstance {
			http.Error(w, `{"error":"too many players"}`, http.StatusBadRequest)
			return
		}
		for _, p := range req.Players {
			if strings.TrimSpace(p) == "" {
				http.Error(w, `{"error":"player id cannot be empty"}`, http.StatusBadRequest)
				return
			}
			if len(p) > 64 {
				http.Error(w, `{"error":"player id too long"}`, http.StatusBadRequest)
				return
			}
		}
		if len(req.GameMode) > 100 {
			http.Error(w, `{"error":"game_mode too long"}`, http.StatusBadRequest)
			return
		}
		if len(req.Map) > 100 {
			http.Error(w, `{"error":"map too long"}`, http.StatusBadRequest)
			return
		}
		if req.GameMode != "" && !gameModeMapPattern.MatchString(req.GameMode) {
			http.Error(w, `{"error":"game_mode contains invalid characters"}`, http.StatusBadRequest)
			return
		}
		if req.Map != "" && !gameModeMapPattern.MatchString(req.Map) {
			http.Error(w, `{"error":"map contains invalid characters"}`, http.StatusBadRequest)
			return
		}
		if pool.InUse() >= pool.Capacity() {
			http.Error(w, `{"error":"no ports available"}`, http.StatusServiceUnavailable)
			return
		}
		if req.GameMode == "" {
			req.GameMode = "TeamDeathMatch"
		}
		if req.Map == "" {
			req.Map = "Charon"
		}
		port, err := pool.Acquire()
		if err != nil {
			http.Error(w, `{"error":"no ports available"}`, http.StatusServiceUnavailable)
			return
		}
		inst, err := sp.Launch(req.GameMode, req.Map, req.MapPath, port, req.Players)
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

	// One instance's status. mmogbrain polls this to decide when the client may
	// be told to travel: YA_Connect makes it travel immediately, so pushing
	// before "ready" drops the player on a server still loading its map. The
	// alternative -- and what happens when this route is missing -- is waiting
	// out a fixed DN_CONNECT_PUSH_DELAY, which is a guess.
	//
	// A read route, so it sits outside the internal-key-guarded subrouter for
	// the same reason GET /instances does.
	r.HandleFunc("/instances/{id}", func(w http.ResponseWriter, r *http.Request) {
		inst, ok := sp.Get(mux.Vars(r)["id"])
		if !ok {
			http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":         inst.ID,
			"match_id":   inst.MatchID,
			"ip":         serverIP,
			"port":       inst.Port,
			"game_mode":  inst.GameMode,
			"map":        inst.Map,
			"players":    inst.Players,
			"started_at": inst.StartedAt.Format(time.RFC3339),
			"ready":      inst.Ready(),
		})
	}).Methods(http.MethodGet)

	// Stop a specific instance
	write.HandleFunc("/instances/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		if err := sp.Stop(id); err != nil {
			if strings.Contains(err.Error(), "not found") {
				http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
			} else {
				http.Error(w, `{"error":"failed to stop instance"}`, http.StatusInternalServerError)
			}
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

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// requireInternalKey refuses to start without a real shared secret for the
// instance-lifecycle write endpoints (POST/DELETE /instances), which
// previously had no authentication at all — any client that could reach
// this service could spawn unbounded dedicated-server processes or kill
// arbitrary running matches. Falls back to ADMIN_KEY, matching the pattern
// used by mmogbrain's and master-server's own internal endpoints.
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
