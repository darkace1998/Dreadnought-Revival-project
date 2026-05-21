package handlers

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

const fieldStatus = "status"

type Handler struct {
	DB  *sql.DB
	Log *logrus.Logger
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

type GameServer struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	IP             string `json:"ip"`
	Port           int    `json:"port"`
	GameMode       string `json:"game_mode"`
	Map            string `json:"map"`
	CurrentPlayers int    `json:"current_players"`
	MaxPlayers     int    `json:"max_players"`
	Status         string `json:"status"`
	LastHeartbeat  string `json:"last_heartbeat"`
	RegisteredAt   string `json:"registered_at"`
}

// Register handles POST /servers/register
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		IP         string `json:"ip"`
		Port       int    `json:"port"`
		GameMode   string `json:"game_mode"`
		Map        string `json:"map"`
		MaxPlayers int    `json:"max_players"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.IP == "" || req.Port == 0 {
		writeError(w, http.StatusBadRequest, "ip and port required")
		return
	}
	if req.MaxPlayers == 0 {
		req.MaxPlayers = 10
	}
	if req.Map == "" {
		req.Map = "Unknown"
	}

	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := h.DB.Exec(
		`INSERT INTO game_servers(id,name,ip,port,game_mode,map,max_players,last_heartbeat,registered_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		id, req.Name, req.IP, req.Port, req.GameMode, req.Map, req.MaxPlayers, now, now,
	)
	if err != nil {
		h.Log.WithError(err).Error("register server: db insert")
		writeError(w, http.StatusInternalServerError, "registration failed")
		return
	}
	if _, err := h.DB.Exec(
		`INSERT INTO server_events(id,server_id,event_type,detail) VALUES(?,?,?,?)`,
		uuid.New().String(), id, "registered", req.Name,
	); err != nil {
		h.Log.WithError(err).Warn("register server: insert event")
	}
	h.Log.WithFields(logrus.Fields{"server_id": id, "ip": req.IP, "port": req.Port}).Info("server registered")
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, fieldStatus: "registered"})
}

// Deregister handles DELETE /servers/{id}
func (h *Handler) Deregister(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	res, err := h.DB.Exec(`DELETE FROM game_servers WHERE id=?`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "deregistration failed")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if _, err := h.DB.Exec(
		`INSERT INTO server_events(id,server_id,event_type) VALUES(?,?,?)`,
		uuid.New().String(), id, "deregistered",
	); err != nil {
		h.Log.WithError(err).Warn("deregister server: insert event")
	}
	h.Log.WithField("server_id", id).Info("server deregistered")
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "deregistered"})
}

// Heartbeat handles POST /servers/{id}/heartbeat
func (h *Handler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req struct {
		CurrentPlayers int `json:"current_players"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := h.DB.Exec(
		`UPDATE game_servers SET last_heartbeat=?, current_players=?, status='online' WHERE id=?`,
		now, req.CurrentPlayers, id,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "heartbeat failed")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "ok"})
}

// List handles GET /servers — returns all online servers (server browser).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	// Mark servers stale if no heartbeat in 60s
	if _, err := h.DB.Exec(`UPDATE game_servers SET status='offline'
		WHERE status='online' AND datetime(last_heartbeat,'+60 seconds') < datetime('now')`); err != nil {
		h.Log.WithError(err).Warn("list servers: mark stale servers offline")
	}

	rows, err := h.DB.Query(
		`SELECT id,name,ip,port,game_mode,map,current_players,max_players,status,last_heartbeat,registered_at
		 FROM game_servers WHERE status='online' ORDER BY registered_at DESC`,
	)
	if err != nil {
		h.Log.WithError(err).Error("list servers: db query")
		writeError(w, http.StatusInternalServerError, "failed to fetch server list")
		return
	}
	defer func() {
		_ = rows.Close()
	}()

	servers := []GameServer{}
	for rows.Next() {
		var s GameServer
		if err := rows.Scan(&s.ID, &s.Name, &s.IP, &s.Port, &s.GameMode, &s.Map,
			&s.CurrentPlayers, &s.MaxPlayers, &s.Status, &s.LastHeartbeat, &s.RegisteredAt); err != nil {
			h.Log.WithError(err).Error("list servers: scan server")
			writeError(w, http.StatusInternalServerError, "failed to fetch server list")
			return
		}
		servers = append(servers, s)
	}
	if err := rows.Err(); err != nil {
		h.Log.WithError(err).Error("list servers: iterate rows")
		writeError(w, http.StatusInternalServerError, "failed to fetch server list")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"servers": servers, "count": len(servers)})
}

// Health handles GET /health
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	var count int
	if err := h.DB.QueryRow(`SELECT COUNT(*) FROM game_servers WHERE status='online'`).Scan(&count); err != nil {
		h.Log.WithError(err).Warn("health: count online servers")
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		fieldStatus:      "ok",
		"service":        "master-server",
		"servers_online": count,
	})
}
