package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// Admin presence and operator broadcast over the Firmament hub.
//
// These live in the main package (like battleResultHandler) rather than in
// handlers/ because they need socialHubInstance, which is main-package state.
// They sit behind the same X-Admin-Key subrouter as the other /admin routes.

// operatorSenderID is stored as the sender of operator broadcasts. It is
// deliberately not a 32-hex pid, so it can never collide with a real account
// in player_state, match_slots or the ignore lists.
const operatorSenderID = "operator"

// maxBroadcastLength bounds one operator message. Chat is not worth
// unbounded rows, and a pasted wall of text would fill every connected
// client's chat box at once.
const maxBroadcastLength = 500

func writeAdminLiveJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAdminLiveError(w http.ResponseWriter, status int, msg string) {
	writeAdminLiveJSON(w, status, map[string]string{"error": msg})
}

type peerSnapshot struct {
	playerID string
	name     string
	channels []string
}

// snapshotPeers copies who is connected right now. A copy, because the
// caller renders it after the lock is released and a peer can vanish or
// rename itself at any moment.
func (h *socialHub) snapshotPeers() []peerSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]peerSnapshot, 0, len(h.peers))
	for id, p := range h.peers {
		p.mu.Lock()
		ch := make([]string, 0, len(p.channels))
		for c := range p.channels {
			ch = append(ch, c)
		}
		name := p.name
		p.mu.Unlock()
		sort.Strings(ch)
		out = append(out, peerSnapshot{playerID: id, name: name, channels: ch})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].playerID < out[j].playerID })
	return out
}

// adminOnline handles GET /admin/online — every connected Firmament peer with
// queue and battle state from the DB: online, queued for a mode, or already
// in a battle (with team). This is what the matchmaker's autoscale counts,
// made visible.
func adminOnline(w http.ResponseWriter, _ *http.Request) {
	type presence struct {
		PlayerID string   `json:"player_id"`
		Name     string   `json:"name"`
		Channels []string `json:"channels"`
		Queued   string   `json:"queued_mode"`
		InBattle string   `json:"match_id"`
		Mode     string   `json:"game_mode"`
		Team     int      `json:"team"`
	}
	out := []presence{}
	database := currentMmogPlayerStateDB()
	for _, p := range socialHubInstance.snapshotPeers() {
		e := presence{PlayerID: p.playerID, Name: p.name, Channels: p.channels}
		if database != nil {
			_ = database.QueryRow(`SELECT game_mode FROM queue_entries
				WHERE user_id=? AND status='waiting' ORDER BY queued_at DESC LIMIT 1`,
				p.playerID).Scan(&e.Queued)
			_ = database.QueryRow(`SELECT s.match_id, m.game_mode, s.team FROM match_slots s
				JOIN matches m ON m.id=s.match_id
				WHERE s.user_id=? AND m.status='active' ORDER BY m.created_at DESC LIMIT 1`,
				p.playerID).Scan(&e.InBattle, &e.Mode, &e.Team)
		}
		out = append(out, e)
	}
	writeAdminLiveJSON(w, http.StatusOK, map[string]interface{}{"online": out, "count": len(out)})
}

// operatorSenderEntry mimics presenceEntry for the operator pseudo-sender: the
// same keys the client's chat line renderer reads (name/display_name), with a
// non-hex pid that cannot be mistaken for a player.
func operatorSenderEntry() map[string]interface{} {
	return map[string]interface{}{
		"pid": "operator", "PID": "operator", "guid": "operator",
		"name": "Operator", "display_name": "Operator", "full_display_name": "Operator",
		"number": 0, "status": "online", "message": "", "online": true,
	}
}

// adminBroadcast handles POST /admin/broadcast — one operator message to a
// chat channel, delivered live to every connected member AND persisted to
// chat history, exactly like a player's chat.channel.message (same notice
// shape, same table). Used for restart warnings and event announcements.
func adminBroadcast(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel string `json:"channel"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeAdminLiveError(w, http.StatusBadRequest, "invalid body")
		return
	}
	channel := strings.TrimSpace(req.Channel)
	if channel == "" {
		channel = "dreadnought.global"
	}
	if _, ok := chatChannelType(channel); !ok {
		writeAdminLiveError(w, http.StatusBadRequest, "unknown channel: the client would drop it")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeAdminLiveError(w, http.StatusBadRequest, "content required")
		return
	}
	if len([]rune(content)) > maxBroadcastLength {
		writeAdminLiveError(w, http.StatusBadRequest, "content too long (max 500 characters)")
		return
	}
	persistMmogChatMessage(operatorSenderID, channel, content)
	socialHubInstance.broadcast(channel, operatorSenderID,
		chatMessageNotice("chat.channel.message", channel, operatorSenderEntry(), content))
	reached := len(socialHubInstance.peersIn(channel))
	logrus.WithFields(logrus.Fields{
		"channel": channel, "reached": reached,
	}).Warn("operator broadcast")
	writeAdminLiveJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ok", "channel": channel, "reached": reached,
	})
}

// adminReset handles POST /admin/reset — put an account back to a fresh
// state. Two independent switches: currencies (balances, XP and rank back to
// the exact values seedMmogPlayerState gives a new account) and research
// (all purchases, ship XP, fleets and loadouts wiped, then the starter fleet
// re-seeded so the hangar keeps working). Values are SET, like provision.
//
// What it deliberately keeps: the account itself, save blobs (onboarding
// stays done), contracts, stats, bans and battle history. Resetting those
// would rewrite the past instead of clearing progress.
func adminReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID     string `json:"user_id"`
		Currencies *bool  `json:"currencies"`
		Research   *bool  `json:"research"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeAdminLiveError(w, http.StatusBadRequest, "invalid body")
		return
	}
	pid := protocol.NormalizePlayerPID(req.UserID)
	if pid == "" {
		writeAdminLiveError(w, http.StatusBadRequest, "user_id must be a 32-hex player id")
		return
	}
	doCurrencies := req.Currencies == nil || *req.Currencies
	doResearch := req.Research == nil || *req.Research
	if !doCurrencies && !doResearch {
		writeAdminLiveError(w, http.StatusBadRequest, "nothing to reset")
		return
	}
	database := currentMmogPlayerStateDB()
	if database == nil {
		writeAdminLiveError(w, http.StatusInternalServerError, "database unavailable")
		return
	}
	if doCurrencies {
		res, err := database.Exec(`UPDATE player_state SET soft_currency=10000, premium_currency=0,
			free_xp=0, current_xp=100, current_rank=1, rank_xp=100, updated_at=datetime('now')
			WHERE user_id=?`, pid)
		if err != nil {
			writeAdminLiveError(w, http.StatusInternalServerError, "db error")
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			writeAdminLiveError(w, http.StatusNotFound, "no such player")
			return
		}
	}
	if doResearch {
		for _, stmt := range []string{
			`DELETE FROM player_purchases WHERE user_id=?`,
			`DELETE FROM player_ship_xp WHERE user_id=?`,
			`DELETE FROM player_fleet_loadouts WHERE user_id=?`,
			`DELETE FROM player_ship_loadouts WHERE user_id=?`,
			`DELETE FROM player_fleets WHERE user_id=?`,
		} {
			if _, err := database.Exec(stmt, pid); err != nil {
				writeAdminLiveError(w, http.StatusInternalServerError, "db error")
				return
			}
		}
		// Re-seed so the account keeps a working starter fleet: seed only
		// inserts missing rows, and with the fleets above deleted every
		// starter fleet is missing, so membership is rebuilt too.
		if err := seedMmogPlayerState(database, pid); err != nil {
			writeAdminLiveError(w, http.StatusInternalServerError, "re-seed failed: "+err.Error())
			return
		}
	}
	// The connected client still shows the old figures; same mechanism as
	// battle rewards (currency_dirty.go).
	markCurrencyDirty(pid)
	logrus.WithFields(logrus.Fields{
		"user_id": pid, "currencies": doCurrencies, "research": doResearch,
	}).Warn("operator reset an account")
	writeAdminLiveJSON(w, http.StatusOK, map[string]interface{}{
		"status": "reset", "user_id": pid,
		"currencies": doCurrencies, "research": doResearch,
	})
}

// validQueueEntryID keeps the kick target to plausible queue-entry ids
// (UUIDs): the id goes into a DELETE, and while that is parameterised, a
// garbage id is always a caller bug worth a 400 rather than a silent no-op.
func validQueueEntryID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	return !strings.ContainsAny(id, " \t\r\n/")
}

// adminQueueKick handles DELETE /admin/queue/{entry} — take one player out of
// the waiting line. Their account and any live match slot are untouched.
func adminQueueKick(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["entry"]
	if !validQueueEntryID(id) {
		writeAdminLiveError(w, http.StatusBadRequest, "invalid queue entry id")
		return
	}
	database := currentMmogPlayerStateDB()
	if database == nil {
		writeAdminLiveError(w, http.StatusInternalServerError, "database unavailable")
		return
	}
	res, err := database.Exec(`DELETE FROM queue_entries WHERE id=? AND status='waiting'`, id)
	if err != nil {
		writeAdminLiveError(w, http.StatusInternalServerError, "db error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeAdminLiveError(w, http.StatusNotFound, "no such waiting entry")
		return
	}
	logrus.WithField("queue_entry", id).Warn("operator removed a player from the queue")
	writeAdminLiveJSON(w, http.StatusOK, map[string]interface{}{"status": "removed", "id": id})
}

// adminQueueClear handles POST /admin/queue/clear — empty the whole waiting
// line (matched entries and live slots are untouched).
func adminQueueClear(w http.ResponseWriter, r *http.Request) {
	database := currentMmogPlayerStateDB()
	if database == nil {
		writeAdminLiveError(w, http.StatusInternalServerError, "database unavailable")
		return
	}
	res, err := database.Exec(`DELETE FROM queue_entries WHERE status='waiting'`)
	if err != nil {
		writeAdminLiveError(w, http.StatusInternalServerError, "db error")
		return
	}
	n, _ := res.RowsAffected()
	logrus.WithField("cleared", n).Warn("operator cleared the matchmaking queue")
	writeAdminLiveJSON(w, http.StatusOK, map[string]interface{}{"status": "cleared", "cleared": n})
}
