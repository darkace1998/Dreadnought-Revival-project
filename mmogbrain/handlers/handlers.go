package handlers

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"

	"github.com/dreadnought-ps/mmogbrain/matchmaker"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

const (
	fieldGameMode = "game_mode"
	fieldStatus   = "status"
)

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

// QueueJoin handles POST /mmog/queue — player joins the matchmaking queue.
func (h *Handler) QueueJoin(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		GameMode string `json:"game_mode"`
		TierMin  int    `json:"tier_min"`
		TierMax  int    `json:"tier_max"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.GameMode == "" {
		req.GameMode = "TeamDeathmatch"
	}
	if !matchmaker.ValidGameMode(req.GameMode) {
		writeError(w, http.StatusBadRequest, "unsupported game mode: "+req.GameMode)
		return
	}
	if req.TierMin == 0 {
		req.TierMin = 1
	}
	if req.TierMax == 0 {
		req.TierMax = 5
	}

	// Check if already queued
	var existing string
	err := h.DB.QueryRow(
		`SELECT id FROM queue_entries WHERE user_id=? AND status='waiting'`, userID,
	).Scan(&existing)
	if err == nil {
		writeError(w, http.StatusConflict, "already in queue")
		return
	}

	id := uuid.New().String()
	_, err = h.DB.Exec(
		`INSERT INTO queue_entries(id,user_id,game_mode,tier_min,tier_max) VALUES(?,?,?,?,?)`,
		id, userID, req.GameMode, req.TierMin, req.TierMax,
	)
	if err != nil {
		h.Log.WithError(err).Error("queue join: db insert")
		writeError(w, http.StatusInternalServerError, "failed to join queue")
		return
	}

	h.Log.WithFields(logrus.Fields{"user_id": userID, fieldGameMode: req.GameMode}).Info("player queued")
	writeJSON(w, http.StatusCreated, map[string]string{
		"entry_id":    id,
		fieldStatus:   "waiting",
		fieldGameMode: req.GameMode,
	})
}

// QueueStatus handles GET /mmog/queue/status — poll for match assignment.
func (h *Handler) QueueStatus(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Check if the player has been assigned to a match
	var matchID, serverIP, gameMode, mapName string
	var serverPort int
	err := h.DB.QueryRow(`
		SELECT m.id, m.server_ip, m.server_port, m.game_mode, m.map
		FROM match_slots ms
		JOIN matches m ON ms.match_id = m.id
		WHERE ms.user_id=? AND m.status='active'
		ORDER BY ms.joined_at DESC
		LIMIT 1
	`, userID).Scan(&matchID, &serverIP, &serverPort, &gameMode, &mapName)

	if err == sql.ErrNoRows {
		// Still waiting — check queue entry
		var entryID, status string
		qErr := h.DB.QueryRow(
			`SELECT id, status FROM queue_entries WHERE user_id=? ORDER BY queued_at DESC LIMIT 1`, userID,
		).Scan(&entryID, &status)
		if qErr == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "not in queue")
			return
		}
		if qErr != nil {
			h.Log.WithError(qErr).Error("queue status: queue entry query")
			writeError(w, http.StatusInternalServerError, "status check failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			fieldStatus: "waiting",
			"entry_id":  entryID,
		})
		return
	}
	if err != nil {
		h.Log.WithError(err).Error("queue status: db query")
		writeError(w, http.StatusInternalServerError, "status check failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		fieldStatus:   "matched",
		"match_id":    matchID,
		"server_ip":   serverIP,
		"server_port": serverPort,
		"game_mode":   gameMode,
		"map":         mapName,
	})
}

// QueueLeave handles DELETE /mmog/queue — player leaves the queue.
func (h *Handler) QueueLeave(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if _, err := h.DB.Exec(`DELETE FROM queue_entries WHERE user_id=? AND status='waiting'`, userID); err != nil {
		h.Log.WithError(err).Error("queue leave: delete queue entry")
		writeError(w, http.StatusInternalServerError, "failed to leave queue")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "left queue"})
}

// GetMatch handles GET /mmog/match/{id}
func (h *Handler) GetMatch(w http.ResponseWriter, r *http.Request) {
	matchID := mux.Vars(r)["id"]
	var gameMode, mapName, serverIP, status string
	var serverPort int
	err := h.DB.QueryRow(
		`SELECT game_mode,map,server_ip,server_port,status FROM matches WHERE id=?`, matchID,
	).Scan(&gameMode, &mapName, &serverIP, &serverPort, &status)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "match not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	rows, err := h.DB.Query(`SELECT user_id,team FROM match_slots WHERE match_id=?`, matchID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	type slot struct {
		UserID string `json:"user_id"`
		Team   int    `json:"team"`
	}
	var slots []slot
	for rows.Next() {
		var s slot
		if err := rows.Scan(&s.UserID, &s.Team); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		slots = append(slots, s)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"match_id":    matchID,
		"game_mode":   gameMode,
		"map":         mapName,
		"server_ip":   serverIP,
		"server_port": serverPort,
		fieldStatus:   status,
		"players":     slots,
	})
}

// ChatSend handles POST /mmog/chat
func (h *Handler) ChatSend(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req struct {
		Channel string `json:"channel"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Channel == "" {
		req.Channel = "global"
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content required")
		return
	}
	id := uuid.New().String()
	if _, err := h.DB.Exec(
		`INSERT INTO chat_messages(id,channel,sender_id,content) VALUES(?,?,?,?)`,
		id, req.Channel, userID, req.Content,
	); err != nil {
		h.Log.WithError(err).Error("chat send: insert message")
		writeError(w, http.StatusInternalServerError, "failed to send message")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"message_id": id})
}

// ChatHistory handles GET /mmog/chat?channel=global
func (h *Handler) ChatHistory(w http.ResponseWriter, r *http.Request) {
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		channel = "global"
	}
	rows, err := h.DB.Query(
		`SELECT id,sender_id,content,sent_at FROM chat_messages WHERE channel=? ORDER BY sent_at DESC LIMIT 50`, channel,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	type msg struct {
		ID       string `json:"id"`
		SenderID string `json:"sender_id"`
		Content  string `json:"content"`
		SentAt   string `json:"sent_at"`
	}
	messages := []msg{}
	for rows.Next() {
		var m msg
		if err := rows.Scan(&m.ID, &m.SenderID, &m.Content, &m.SentAt); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"channel": channel, "messages": messages})
}

// Health handles GET /health
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	var queuedPlayers int
	if err := h.DB.QueryRow(`SELECT COUNT(*) FROM queue_entries WHERE status='waiting'`).Scan(&queuedPlayers); err != nil {
		h.Log.WithError(err).Warn("health: count queued players")
	}
	var activeMatches int
	if err := h.DB.QueryRow(`SELECT COUNT(*) FROM matches WHERE status='active'`).Scan(&activeMatches); err != nil {
		h.Log.WithError(err).Warn("health: count active matches")
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		fieldStatus:      "ok",
		"service":        "mmogbrain",
		"queued_players": queuedPlayers,
		"active_matches": activeMatches,
	})
}

// AdminQueue handles GET /admin/queue — lists all queue entries (admin only).
func (h *Handler) AdminQueue(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Query(
		`SELECT id,user_id,game_mode,tier_min,tier_max,queued_at,status FROM queue_entries ORDER BY queued_at`,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	type entry struct {
		ID       string `json:"id"`
		UserID   string `json:"user_id"`
		GameMode string `json:"game_mode"`
		TierMin  int    `json:"tier_min"`
		TierMax  int    `json:"tier_max"`
		QueuedAt string `json:"queued_at"`
		Status   string `json:"status"`
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.ID, &e.UserID, &e.GameMode, &e.TierMin, &e.TierMax, &e.QueuedAt, &e.Status); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"queue": entries, "count": len(entries)})
}
