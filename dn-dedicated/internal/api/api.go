// Package api exposes a game-manager-compatible HTTP control plane.
//
// The routes, request shapes, response shapes, status codes and validation
// limits are deliberately identical to game-manager/main.go, so mmogbrain's
// matchmaker can POST /instances here without any change on its side. Where
// this differs from game-manager it is called out in a comment; the two
// intentional differences are the default map (game-manager defaults to the
// invented "Charon") and the registration heartbeat (game-manager has none).
package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"dn-dedicated/internal/gamedata"
	"dn-dedicated/internal/server"
)

// gameModeMapPattern restricts game_mode and map before they are interpolated
// into the engine's argv. exec.Command never invokes a shell, so shell
// metacharacter injection is not reachable, but under Wine CreateProcess
// reconstructs a single Win32 command line from the argv slice using Windows
// quoting rules -- an unsanitized value containing a quote could break out of
// its token and inject additional engine flags.
//
// map_path is deliberately NOT checked against this pattern: it is a package
// path and legitimately contains "/". It is validated by prefix instead.
var gameModeMapPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// maxPlayersPerInstance bounds the accepted player list. Production match size
// caps at 10; twice that is headroom without allowing an unbounded payload.
const maxPlayersPerInstance = 20

// maxBodyBytes matches game-manager's 128KB limit.
const maxBodyBytes = 1 << 17

// Server is the HTTP control plane.
type Server struct {
	Manager     *server.Manager
	InternalKey string
	ServerIP    string
	Log         io.Writer
}

// Handler builds the router.
//
// Uses net/http's method+wildcard patterns (Go 1.22+) rather than gorilla/mux,
// which is why this module needs no dependencies.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /instances", s.requireKey(s.createInstance))
	mux.HandleFunc("GET /instances", s.listInstances)
	mux.HandleFunc("DELETE /instances/{id}", s.requireKey(s.stopInstance))
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /metrics", s.metrics)

	return s.logRequests(mux)
}

// metrics answers in Prometheus text exposition format.
//
// game-manager serves this route via promhttp, and every service in the Revival
// stack is expected to expose it, so omitting it would break a scrape config
// that already works. This is a REDUCED set: the instance and port gauges that
// are actually specific to this service, without the Go runtime and process
// collectors promhttp adds for free. Anything alerting on go_* or process_*
// series from game-manager will not find them here.
func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	instances := s.Manager.List()
	mock := 0
	for _, inst := range instances {
		if inst.Mock {
			mock++
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP dn_instances_running Battle servers currently running.\n")
	fmt.Fprintf(w, "# TYPE dn_instances_running gauge\ndn_instances_running %d\n", len(instances))
	fmt.Fprintf(w, "# HELP dn_instances_mock Instances with no backing process.\n")
	fmt.Fprintf(w, "# TYPE dn_instances_mock gauge\ndn_instances_mock %d\n", mock)
	fmt.Fprintf(w, "# HELP dn_ports_used UDP ports allocated from the pool.\n")
	fmt.Fprintf(w, "# TYPE dn_ports_used gauge\ndn_ports_used %d\n", s.Manager.PortsInUse())
	fmt.Fprintf(w, "# HELP dn_ports_capacity Total UDP ports in the pool.\n")
	fmt.Fprintf(w, "# TYPE dn_ports_capacity gauge\ndn_ports_capacity %d\n", s.Manager.PortCapacity())
}

func (s *Server) createInstance(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req struct {
		GameMode   string   `json:"game_mode"`
		Map        string   `json:"map"`
		MapPath    string   `json:"map_path"`
		Players    []string `json:"players"`
		MaxPlayers int      `json:"max_players"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	// Validation order and messages mirror game-manager so a caller sees the
	// same failure for the same input.
	if len(req.Players) == 0 {
		writeError(w, http.StatusBadRequest, "players list is required and cannot be empty")
		return
	}
	if len(req.Players) > maxPlayersPerInstance {
		writeError(w, http.StatusBadRequest, "too many players")
		return
	}
	for _, p := range req.Players {
		if strings.TrimSpace(p) == "" {
			writeError(w, http.StatusBadRequest, "player id cannot be empty")
			return
		}
		if len(p) > 64 {
			writeError(w, http.StatusBadRequest, "player id too long")
			return
		}
	}
	if len(req.GameMode) > 100 {
		writeError(w, http.StatusBadRequest, "game_mode too long")
		return
	}
	if len(req.Map) > 100 {
		writeError(w, http.StatusBadRequest, "map too long")
		return
	}
	if req.GameMode != "" && !gameModeMapPattern.MatchString(req.GameMode) {
		writeError(w, http.StatusBadRequest, "game_mode contains invalid characters")
		return
	}
	if req.Map != "" && !gameModeMapPattern.MatchString(req.Map) {
		writeError(w, http.StatusBadRequest, "map contains invalid characters")
		return
	}
	if req.MapPath != "" && !strings.HasPrefix(req.MapPath, "/Game/") {
		writeError(w, http.StatusBadRequest, "map_path must be a /Game/... package path")
		return
	}

	if s.Manager.PortsInUse() >= s.Manager.PortCapacity() {
		writeError(w, http.StatusServiceUnavailable, "no ports available")
		return
	}

	mode, err := gamedata.NormalizeGameMode(req.GameMode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Resolve the map. A caller-supplied map_path wins, because that is the
	// value the engine actually loads and mmogbrain sends it straight from the
	// client's own table. Falling back to the name lookup covers callers that
	// send only a name.
	gameMap, err := resolveMap(req.Map, req.MapPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	inst, err := s.Manager.Start(server.StartOptions{
		Map:        gameMap,
		GameMode:   mode,
		MaxPlayers: req.MaxPlayers,
		Players:    req.Players,
	})
	if err != nil {
		s.logf("launch failed: %v", err)
		writeError(w, http.StatusInternalServerError, "launch failed")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"instance_id": inst.ID,
		"match_id":    inst.MatchID,
		"ip":          s.ServerIP,
		"port":        inst.Port,
		"game_mode":   inst.GameMode,
		"map":         inst.MapName,
		// map_path is additional to game-manager's response. Callers that only
		// read the documented fields are unaffected.
		"map_path": inst.MapPath,
	})
}

// resolveMap picks the map to load from the name and path a caller supplied.
func resolveMap(name, path string) (gamedata.Map, error) {
	if path != "" {
		display := name
		if display == "" {
			display = path
			if i := strings.LastIndex(display, "/"); i >= 0 {
				display = display[i+1:]
			}
		}
		return gamedata.Map{Name: display, Path: path}, nil
	}
	if name == "" {
		// game-manager defaults to "Charon" here, which is an invented name with
		// no package path, so the engine loads nothing. Default to a real map.
		name = gamedata.DefaultMap
	}
	return gamedata.LookupMap(name)
}

func (s *Server) listInstances(w http.ResponseWriter, _ *http.Request) {
	instances := s.Manager.List()
	views := make([]map[string]interface{}, 0, len(instances))
	for _, inst := range instances {
		views = append(views, map[string]interface{}{
			"id":         inst.ID,
			"match_id":   inst.MatchID,
			"port":       inst.Port,
			"game_mode":  inst.GameMode,
			"map":        inst.MapName,
			"map_path":   inst.MapPath,
			"players":    inst.Players,
			"started_at": inst.StartedAt.Format(time.RFC3339),
			"pid":        inst.PID(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"instances":  views,
		"count":      len(views),
		"ports_used": s.Manager.PortsInUse(),
	})
}

func (s *Server) stopInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Manager.Stop(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "instance not found")
			return
		}
		s.logf("stop %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, "failed to stop instance")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "ok",
		"service":    "dn-dedicated",
		"instances":  len(s.Manager.List()),
		"ports_used": s.Manager.PortsInUse(),
		"capacity":   s.Manager.PortCapacity(),
	})
}

// requireKey guards the write routes with the same X-Internal-Key check
// game-manager uses, compared in constant time.
func (s *Server) requireKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("X-Internal-Key")
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(s.InternalKey)) != 1 {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r)
	}
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logf("%s %s -> %d (%dms)", r.Method, r.URL.Path, rec.status, time.Since(start).Milliseconds())
	})
}

func (s *Server) logf(format string, args ...interface{}) {
	if s.Log == nil {
		return
	}
	fmt.Fprintf(s.Log, format+"\n", args...)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError emits {"error":"..."}, the shape game-manager produces.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
