package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// Moderation for the admin dashboard: kick, ban/unban, a player's match
// history, and the audit log of every admin action.
//
// Bans are enforced HERE, not only by the auth server. The auth server's ban
// stops new launcher sign-ins, but mmogbrain checks a launcher JWT on its own
// (stateless, valid 24 h), so a player who signed in before the ban could keep
// playing and reconnect all day. mmogbrain keeps its own list (admin_bans),
// refuses banned players at the gateway login and the Firmament handshake,
// and drops their live connections. The auth server is told as well, so the
// launcher refuses them too.

var errAdminDisconnected = errors.New("disconnected by an admin")

// adminModeration is the in-memory side of admin_bans plus recent kicks. The
// handshake paths read it without touching the database: mmogbrain's SQLite
// has ONE connection, and a lookup from inside someone else's open rows loop
// would deadlock the server.
var adminModeration = struct {
	mu     sync.Mutex
	loaded bool
	banned map[string]bool
	kicked map[string]time.Time // pid -> when; drops connections older than this
}{banned: map[string]bool{}, kicked: map[string]time.Time{}}

func adminLoadBans() {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return
	}
	adminModeration.mu.Lock()
	loaded := adminModeration.loaded
	adminModeration.mu.Unlock()
	if loaded {
		return
	}
	set := map[string]bool{}
	rows, err := database.Query(`SELECT user_id FROM admin_bans`)
	if err != nil {
		return
	}
	for rows.Next() {
		var pid string
		if rows.Scan(&pid) == nil {
			set[pid] = true
		}
	}
	_ = rows.Close()
	adminModeration.mu.Lock()
	if !adminModeration.loaded {
		adminModeration.banned = set
		adminModeration.loaded = true
	}
	adminModeration.mu.Unlock()
}

// adminPlayerBanned reports whether a player (any id form) is banned.
func adminPlayerBanned(pid string) bool {
	adminLoadBans()
	pid = normalizedPlayerStatePID(pid)
	adminModeration.mu.Lock()
	defer adminModeration.mu.Unlock()
	return adminModeration.banned[pid]
}

// adminConnectionRevoked reports whether a game connection that registered at
// since must be dropped: the player is banned, or was kicked after it opened.
func adminConnectionRevoked(pid string, since time.Time) bool {
	adminLoadBans()
	pid = normalizedPlayerStatePID(pid)
	adminModeration.mu.Lock()
	defer adminModeration.mu.Unlock()
	if adminModeration.banned[pid] {
		return true
	}
	at, ok := adminModeration.kicked[pid]
	return ok && !at.Before(since)
}

// adminDisconnect drops every connection the player holds: the Firmament
// (social) socket now, the game socket on its next push pass (within one
// client ping, ~5 s), and the gateway session.
func adminDisconnect(pid string) (wasOnline bool) {
	adminModeration.mu.Lock()
	adminModeration.kicked[pid] = time.Now()
	for p, at := range adminModeration.kicked { // keep the map small
		if time.Since(at) > time.Hour {
			delete(adminModeration.kicked, p)
		}
	}
	adminModeration.mu.Unlock()

	socialHubInstance.mu.RLock()
	peer := socialHubInstance.peers[pid]
	socialHubInstance.mu.RUnlock()
	if peer != nil {
		wasOnline = true
		_ = peer.conn.Close()
		socialHubInstance.leave(peer)
	}
	sessionsMu.Lock()
	for id, sess := range sessions {
		if normalizedPlayerStatePID(sess.UserID) == pid {
			delete(sessions, id)
		}
	}
	sessionsMu.Unlock()
	return wasOnline
}

// adminAudit records one admin action. actor is the caller's address.
func adminAudit(database *sql.DB, r *http.Request, action, target string, details any) {
	actor := r.RemoteAddr
	if host, _, err := net.SplitHostPort(actor); err == nil {
		actor = host
	}
	text := ""
	if details != nil {
		if b, err := json.Marshal(details); err == nil {
			text = string(b)
		}
	}
	logrus.WithFields(logrus.Fields{"actor": actor, "action": action, "target": target, "details": text}).Warn("admin: action")
	if database != nil {
		_, _ = database.Exec(`INSERT INTO admin_audit(actor,action,target,details) VALUES(?,?,?,?)`, actor, action, target, text)
	}
}

func registerAdminModeration(api *mux.Router) {
	api.HandleFunc("/players/{pid}/kick", adminAPIPlayerKick).Methods(http.MethodPost)
	api.HandleFunc("/players/{pid}/ban", adminAPIPlayerBan).Methods(http.MethodPost)
	api.HandleFunc("/players/{pid}/unban", adminAPIPlayerUnban).Methods(http.MethodPost)
	api.HandleFunc("/players/{pid}/matches", adminAPIPlayerMatches).Methods(http.MethodGet)
	api.HandleFunc("/players/{pid}/audit", adminAPIPlayerAudit).Methods(http.MethodGet)
	api.HandleFunc("/bans", adminAPIBans).Methods(http.MethodGet)
	api.HandleFunc("/audit", adminAPIAudit).Methods(http.MethodGet)
}

func adminReason(w http.ResponseWriter, r *http.Request, required bool) (string, bool) {
	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return "", false
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if len(req.Reason) > 200 {
		http.Error(w, `{"error":"reason is limited to 200 characters"}`, http.StatusBadRequest)
		return "", false
	}
	if required && req.Reason == "" {
		http.Error(w, `{"error":"a reason is required"}`, http.StatusBadRequest)
		return "", false
	}
	return req.Reason, true
}

func adminAPIPlayerKick(w http.ResponseWriter, r *http.Request) {
	pid, database, ok := adminPlayerPID(w, r)
	if !ok {
		return
	}
	reason, ok := adminReason(w, r, false)
	if !ok {
		return
	}
	online := adminDisconnect(pid)
	adminAudit(database, r, "kick", pid, map[string]any{"reason": reason, "was_online": online})
	writeAdminJSON(w, map[string]any{"kicked": true, "was_online": online})
}

func adminAPIPlayerBan(w http.ResponseWriter, r *http.Request) {
	pid, database, ok := adminPlayerPID(w, r)
	if !ok {
		return
	}
	reason, ok := adminReason(w, r, true)
	if !ok {
		return
	}
	adminLoadBans()
	if _, err := database.Exec(`INSERT INTO admin_bans(user_id,reason) VALUES(?,?)
		ON CONFLICT(user_id) DO UPDATE SET reason=excluded.reason`, pid, reason); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	adminModeration.mu.Lock()
	adminModeration.banned[pid] = true
	adminModeration.mu.Unlock()
	online := adminDisconnect(pid)
	authErr := adminAuthCall("/admin/ban", pid, reason)
	details := map[string]any{"reason": reason, "was_online": online, "auth": "ok"}
	if authErr != nil {
		details["auth"] = authErr.Error()
	}
	adminAudit(database, r, "ban", pid, details)
	adminAPIPlayerDetail(w, r)
}

func adminAPIPlayerUnban(w http.ResponseWriter, r *http.Request) {
	pid, database, ok := adminPlayerPID(w, r)
	if !ok {
		return
	}
	adminLoadBans()
	if _, err := database.Exec(`DELETE FROM admin_bans WHERE user_id=?`, pid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	adminModeration.mu.Lock()
	delete(adminModeration.banned, pid)
	adminModeration.mu.Unlock()
	authErr := adminAuthCall("/admin/unban", pid, "")
	details := map[string]any{"auth": "ok"}
	if authErr != nil {
		details["auth"] = authErr.Error()
	}
	adminAudit(database, r, "unban", pid, details)
	adminAPIPlayerDetail(w, r)
}

// adminAuthCall forwards a ban/unban to the auth server so the launcher
// refuses the account too. Best effort: mmogbrain's own ban already holds.
func adminAuthCall(path, pid, reason string) error {
	if adminDash.authURL == "" {
		return errors.New("auth server not configured")
	}
	body, _ := json.Marshal(map[string]string{"user_id": pid, "reason": reason})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(adminDash.authURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Key", adminDash.adminKey)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth server answered %d", resp.StatusCode)
	}
	return nil
}

func adminAPIPlayerMatches(w http.ResponseWriter, r *http.Request) {
	pid, database, ok := adminPlayerPID(w, r)
	if !ok {
		return
	}
	type row struct {
		MatchID string `json:"match_id"`
		At      string `json:"at"`
		Mode    string `json:"mode"`
		Map     string `json:"map"`
		Fleet   string `json:"fleet"`
		Outcome string `json:"outcome"`
		Kills   int    `json:"kills"`
		Deaths  int    `json:"deaths"`
		Assists int    `json:"assists"`
		Damage  int64  `json:"damage"`
		Credits int64  `json:"credits"`
		XP      int64  `json:"xp"`
	}
	out := []row{}
	rows, err := database.Query(`SELECT b.match_id, b.created_at, COALESCE(m.game_mode,''), COALESCE(m.map,''),
		b.fleet_type, b.outcome, b.kills, b.deaths, b.assists, b.damage, b.credits, b.xp
		FROM battle_results b
		-- battle_results.match_id is the battle server's id (matches.battle_match_id)
		LEFT JOIN matches m ON m.battle_match_id=b.match_id AND b.match_id<>''
		WHERE b.user_id=? ORDER BY b.created_at DESC LIMIT 50`, pid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for rows.Next() {
		var x row
		var fleet int32
		if rows.Scan(&x.MatchID, &x.At, &x.Mode, &x.Map, &fleet, &x.Outcome, &x.Kills, &x.Deaths, &x.Assists,
			&x.Damage, &x.Credits, &x.XP) == nil {
			x.Map = adminMapName(x.Map)
			x.Fleet = fleetTypeName(fleet)
			out = append(out, x)
		}
	}
	_ = rows.Close()
	writeAdminJSON(w, map[string]any{"matches": out})
}

// adminMapName shortens a map path (/Game/Maps/MP/X/MP_X_P) to its folder.
func adminMapName(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return path
}

type adminAuditRow struct {
	ID      int64  `json:"id"`
	At      string `json:"at"`
	Actor   string `json:"actor"`
	Action  string `json:"action"`
	Target  string `json:"target"`
	Name    string `json:"name,omitempty"`
	Details string `json:"details"`
}

func adminAuditRows(database *sql.DB, where string, args ...any) ([]adminAuditRow, error) {
	out := []adminAuditRow{}
	rows, err := database.Query(`SELECT id, created_at, actor, action, target, details FROM admin_audit `+where+
		` ORDER BY id DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a adminAuditRow
		if rows.Scan(&a.ID, &a.At, &a.Actor, &a.Action, &a.Target, &a.Details) == nil {
			out = append(out, a)
		}
	}
	_ = rows.Close()
	// Names after the rows are closed: the lookup uses the same (single)
	// database connection.
	for i := range out {
		if adminPIDPattern.MatchString(out[i].Target) {
			out[i].Name = adminPlayerName(out[i].Target)
		}
	}
	return out, nil
}

func adminAPIPlayerAudit(w http.ResponseWriter, r *http.Request) {
	pid, database, ok := adminPlayerPID(w, r)
	if !ok {
		return
	}
	rows, err := adminAuditRows(database, `WHERE target=?`, pid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeAdminJSON(w, map[string]any{"audit": rows})
}

func adminAPIAudit(w http.ResponseWriter, _ *http.Request) {
	database := currentMmogPlayerStateDB()
	if database == nil {
		http.Error(w, `{"error":"database unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	rows, err := adminAuditRows(database, ``)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeAdminJSON(w, map[string]any{"audit": rows})
}

func adminAPIBans(w http.ResponseWriter, _ *http.Request) {
	database := currentMmogPlayerStateDB()
	if database == nil {
		http.Error(w, `{"error":"database unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	type row struct {
		PID    string `json:"pid"`
		Name   string `json:"name"`
		Reason string `json:"reason"`
		At     string `json:"at"`
	}
	out := []row{}
	rows, err := database.Query(`SELECT user_id, reason, created_at FROM admin_bans ORDER BY created_at DESC`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for rows.Next() {
		var b row
		if rows.Scan(&b.PID, &b.Reason, &b.At) == nil {
			out = append(out, b)
		}
	}
	_ = rows.Close()
	for i := range out {
		out[i].Name = adminPlayerName(out[i].PID)
	}
	writeAdminJSON(w, map[string]any{"bans": out})
}
