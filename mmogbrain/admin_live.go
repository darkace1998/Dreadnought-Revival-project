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

// adminCatalog handles GET /admin/catalog — every buyable hull and hero with
// the price the store charges for it (purchasePriceForItem, the same
// derivation the till uses) plus how many accounts own it. Read-only market
// overview for the dashboard; the authoritative definitions stay in the
// roster and the catalog.
func adminCatalog(w http.ResponseWriter, _ *http.Request) {
	owners := map[int32]int64{}
	if database := currentMmogPlayerStateDB(); database != nil {
		rows, err := database.Query(`SELECT item_id,COUNT(*) FROM player_purchases GROUP BY item_id`)
		if err == nil {
			for rows.Next() {
				var id int32
				var n int64
				if err := rows.Scan(&id, &n); err != nil {
					break
				}
				owners[id] = n
			}
			_ = rows.Close()
		}
	}
	type offer struct {
		ID           int32  `json:"id"`
		Name         string `json:"name"`
		Tier         int32  `json:"tier"`
		Line         string `json:"line"`
		Manufacturer string `json:"manufacturer"`
		Hero         bool   `json:"hero"`
		Price        int32  `json:"price_credits"`
		Owners       int64  `json:"owners"`
	}
	out := []offer{}
	for _, hull := range baseShipLoadouts {
		out = append(out, offer{
			ID: hull.loadoutID, Name: hull.name, Tier: hull.tier, Line: hull.hullLine,
			Manufacturer: baseShipManufacturerByClassSize[hull.hullLine],
			Price:        purchasePriceForItem(hull.loadoutID), Owners: owners[hull.loadoutID],
		})
	}
	for _, hero := range heroShipLoadouts {
		out = append(out, offer{
			ID: hero.loadoutID, Name: hero.name, Tier: hero.tier, Line: hero.hullLine,
			Manufacturer: hero.manufacturer, Hero: true,
			Price:        purchasePriceForItem(hero.loadoutID), Owners: owners[hero.loadoutID],
		})
	}
	writeAdminLiveJSON(w, http.StatusOK, map[string]interface{}{"ships": out, "count": len(out)})
}

// adminPlayerProgress handles GET /admin/player/{id}/progress — career goals
// with live progress, season levels and contracts. The static career
// catalogue lives in code (careerGoalsConfig); progress counters live in the
// DB. Companion to the handlers-package player detail, which cannot see the
// goal catalogue from its package.
func adminPlayerProgress(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	pid := protocol.NormalizePlayerPID(id)
	if pid == "" {
		writeAdminLiveError(w, http.StatusBadRequest, "id must be a 32-hex player id")
		return
	}
	type stage struct {
		Amount int32  `json:"amount"`
		Reward int32  `json:"reward"`
		Type   string `json:"reward_type"`
	}
	type goal struct {
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		Category string  `json:"category"`
		Progress int32   `json:"progress"`
		Stages   []stage `json:"stages"`
	}
	goals := []goal{}
	for _, g := range careerGoalsConfig() {
		gg := goal{ID: g.id, Title: g.title, Category: g.category,
			Progress: careerGoalProgressForPlayer(pid, g.id)}
		for _, s := range g.stages {
			gg.Stages = append(gg.Stages, stage{Amount: s.amountToComplete, Reward: s.reward, Type: s.rewardType})
		}
		goals = append(goals, gg)
	}
	type season struct {
		SeasonID string `json:"season_id"`
		XP       int64  `json:"xp"`
		Level    int    `json:"level"`
	}
	seasons := []season{}
	type contract struct {
		ID       string `json:"contract_id"`
		State    string `json:"state"`
		Progress int    `json:"progress"`
		Updated  string `json:"updated_at"`
	}
	contracts := []contract{}
	type counter struct {
		Counter string `json:"counter_id"`
		Sub     string `json:"counter_sub_id"`
		Value   int64  `json:"value"`
	}
	counters := []counter{}
	if database := currentMmogPlayerStateDB(); database != nil {
		if rows, err := database.Query(`SELECT season_id,xp,level FROM player_season_progress
			WHERE user_id=? ORDER BY season_id`, pid); err == nil {
			for rows.Next() {
				var s season
				if err := rows.Scan(&s.SeasonID, &s.XP, &s.Level); err != nil {
					break
				}
				seasons = append(seasons, s)
			}
			_ = rows.Close()
		}
		if rows, err := database.Query(`SELECT contract_id,state,progress,updated_at FROM player_contracts
			WHERE user_id=? ORDER BY updated_at DESC`, pid); err == nil {
			for rows.Next() {
				var c contract
				if err := rows.Scan(&c.ID, &c.State, &c.Progress, &c.Updated); err != nil {
					break
				}
				contracts = append(contracts, c)
			}
			_ = rows.Close()
		}
		if rows, err := database.Query(`SELECT counter_id,counter_sub_id,value FROM player_stats_counters
			WHERE user_id=? ORDER BY value DESC LIMIT 20`, pid); err == nil {
			for rows.Next() {
				var c counter
				if err := rows.Scan(&c.Counter, &c.Sub, &c.Value); err != nil {
					break
				}
				counters = append(counters, c)
			}
			_ = rows.Close()
		}
	}
	writeAdminLiveJSON(w, http.StatusOK, map[string]interface{}{
		"goals": goals, "seasons": seasons, "contracts": contracts, "counters": counters,
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
