package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

// AdminResults handles GET /admin/results — lists recent battle results
// (newest first), as reported by battle-server-mod at the end of each match.
// This is the operator's view of who played, who won, and what was paid.
func (h *Handler) AdminResults(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := h.DB.Query(`SELECT match_id,user_id,team,outcome,kills,deaths,assists,damage,credits,xp,created_at
		FROM battle_results ORDER BY created_at DESC, match_id DESC LIMIT ?`, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	type result struct {
		MatchID  string `json:"match_id"`
		UserID   string `json:"user_id"`
		Team     int    `json:"team"`
		Outcome  string `json:"outcome"`
		Kills    int    `json:"kills"`
		Deaths   int    `json:"deaths"`
		Assists  int    `json:"assists"`
		Damage   int    `json:"damage"`
		Credits  int    `json:"credits"`
		XP       int    `json:"xp"`
		Reported string `json:"reported_at"`
	}
	out := []result{}
	for rows.Next() {
		var e result
		if err := rows.Scan(&e.MatchID, &e.UserID, &e.Team, &e.Outcome, &e.Kills,
			&e.Deaths, &e.Assists, &e.Damage, &e.Credits, &e.XP, &e.Reported); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"results": out, "count": len(out)})
}

// AdminPlayerDetail handles GET /admin/player/{id} — one account's full
// operator-visible state: balances and rank, fleets, ship loadouts and ship
// XP, purchase counts, queue and live-match status, and recent battle
// results. Read-only; everything here already exists in some table, this only
// joins it for the dashboard's drill-down.
func (h *Handler) AdminPlayerDetail(w http.ResponseWriter, r *http.Request) {
	pid := mux.Vars(r)["id"]
	if !playerPIDPattern.MatchString(pid) {
		writeError(w, http.StatusBadRequest, "id must be a 32-character hex player id")
		return
	}
	pid = strings.ToLower(pid)

	var state struct {
		UserID           string `json:"user_id"`
		DisplayName      string `json:"display_name"`
		Credits          int64  `json:"credits"`
		Premium          int64  `json:"premium"`
		FreeXP           int64  `json:"free_xp"`
		CurrentXP        int64  `json:"current_xp"`
		Rank             int    `json:"current_rank"`
		RankXP           int64  `json:"rank_xp"`
		CreatedAt        string `json:"created_at"`
	}
	if err := h.DB.QueryRow(`SELECT user_id,display_name,soft_currency,premium_currency,free_xp,
		current_xp,current_rank,rank_xp,created_at FROM player_state WHERE user_id=?`, pid).
		Scan(&state.UserID, &state.DisplayName, &state.Credits, &state.Premium, &state.FreeXP,
			&state.CurrentXP, &state.Rank, &state.RankXP, &state.CreatedAt); err != nil {
		writeError(w, http.StatusNotFound, "no such player")
		return
	}

	type fleet struct {
		FleetID   int    `json:"fleet_id"`
		Name      string `json:"name"`
		Type      int    `json:"fleet_type"`
		Active    bool   `json:"active"`
		FlagShip  int    `json:"flagship_ship_id"`
		Loadouts  int    `json:"loadouts"`
	}
	fleets := []fleet{}
	rows, err := h.DB.Query(`SELECT f.fleet_id,f.display_name,f.fleet_type,f.active,f.flagship_ship_id,
		(SELECT COUNT(*) FROM player_fleet_loadouts l WHERE l.user_id=f.user_id AND l.fleet_id=f.fleet_id)
		FROM player_fleets f WHERE f.user_id=? ORDER BY f.fleet_id`, pid)
	if err == nil {
		for rows.Next() {
			var f fleet
			var active int
			if err := rows.Scan(&f.FleetID, &f.Name, &f.Type, &active, &f.FlagShip, &f.Loadouts); err != nil {
				break
			}
			f.Active = active != 0
			fleets = append(fleets, f)
		}
		_ = rows.Close()
	}

	type ship struct {
		LoadoutID int    `json:"loadout_id"`
		ShipID    int    `json:"ship_id"`
		Name      string `json:"name"`
		Active    bool   `json:"active"`
		XP        int64  `json:"ship_xp"`
	}
	ships := []ship{}
	rows, err = h.DB.Query(`SELECT s.loadout_id,s.ship_id,s.loadout_name,s.active,
		COALESCE((SELECT x.xp FROM player_ship_xp x WHERE x.user_id=s.user_id AND x.ship_id=s.ship_id),0)
		FROM player_ship_loadouts s WHERE s.user_id=? ORDER BY s.loadout_id`, pid)
	if err == nil {
		for rows.Next() {
			var s ship
			var active int
			if err := rows.Scan(&s.LoadoutID, &s.ShipID, &s.Name, &active, &s.XP); err != nil {
				break
			}
			s.Active = active != 0
			ships = append(ships, s)
		}
		_ = rows.Close()
	}

	purchaseTypes := map[string]int64{}
	var purchases int64
	prows, err := h.DB.Query(`SELECT item_type,COUNT(*) FROM player_purchases WHERE user_id=? GROUP BY item_type`, pid)
	if err == nil {
		for prows.Next() {
			var kind string
			var n int64
			if err := prows.Scan(&kind, &n); err != nil {
				break
			}
			purchaseTypes[kind] = n
			purchases += n
		}
		_ = prows.Close()
	}

	var queuedMode, queuedSince string
	_ = h.DB.QueryRow(`SELECT game_mode,queued_at FROM queue_entries
		WHERE user_id=? AND status='waiting' ORDER BY queued_at DESC LIMIT 1`, pid).Scan(&queuedMode, &queuedSince)

	var live struct {
		MatchID string `json:"match_id"`
		Mode    string `json:"game_mode"`
		Map     string `json:"map"`
		Team    int    `json:"team"`
		IP      string `json:"server_ip"`
		Port    int    `json:"server_port"`
	}
	liveFound := h.DB.QueryRow(`SELECT s.match_id,m.game_mode,m.map,s.team,m.server_ip,m.server_port
		FROM match_slots s JOIN matches m ON m.id=s.match_id
		WHERE s.user_id=? AND m.status='active' ORDER BY m.created_at DESC LIMIT 1`, pid).
		Scan(&live.MatchID, &live.Mode, &live.Map, &live.Team, &live.IP, &live.Port) == nil

	type result struct {
		MatchID string `json:"match_id"`
		Outcome string `json:"outcome"`
		Kills   int    `json:"kills"`
		Credits int    `json:"credits"`
		XP      int    `json:"xp"`
	}
	results := []result{}
	rrows, err := h.DB.Query(`SELECT match_id,outcome,kills,credits,xp FROM battle_results
		WHERE user_id=? ORDER BY created_at DESC LIMIT 5`, pid)
	if err == nil {
		for rrows.Next() {
			var e result
			if err := rrows.Scan(&e.MatchID, &e.Outcome, &e.Kills, &e.Credits, &e.XP); err != nil {
				break
			}
			results = append(results, e)
		}
		_ = rrows.Close()
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"player": state, "fleets": fleets, "ships": ships,
		"purchases": purchases, "purchase_types": purchaseTypes,
		"queued_mode": queuedMode, "queued_since": queuedSince,
		"live_match": live, "in_match": liveFound,
		"recent_results": results,
	})
}
