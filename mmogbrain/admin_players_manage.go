package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"

	"github.com/gorilla/mux"
)

// Player management for the admin dashboard: read one account, change its
// credits / GP / free XP, and grant hero ships. Every change is logged at warn
// level ("admin: action") and in the admin_audit table, and, when the player is online, pushed to their client
// with the same in-session pushes the game flow uses, so no relog is needed.

var adminPIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// adminMaxAmount bounds one change so a typo cannot overflow a balance.
const adminMaxAmount = 1_000_000_000

func registerAdminPlayerManagement(api *mux.Router) {
	api.HandleFunc("/players/{pid}", adminAPIPlayerDetail).Methods(http.MethodGet)
	api.HandleFunc("/players/{pid}/currency", adminAPIPlayerCurrency).Methods(http.MethodPost)
	api.HandleFunc("/players/{pid}/ships", adminAPIPlayerGrantShip).Methods(http.MethodPost)
}

func adminPlayerPID(w http.ResponseWriter, r *http.Request) (string, *sql.DB, bool) {
	pid := mux.Vars(r)["pid"]
	if !adminPIDPattern.MatchString(pid) {
		http.Error(w, `{"error":"player id must be 32 lowercase hex characters"}`, http.StatusBadRequest)
		return "", nil, false
	}
	database := currentMmogPlayerStateDB()
	if database == nil {
		http.Error(w, `{"error":"database unavailable"}`, http.StatusServiceUnavailable)
		return "", nil, false
	}
	var exists int
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_state WHERE user_id=?`, pid).Scan(&exists); err != nil || exists == 0 {
		// Never created here: a typo'd id would make a ghost account.
		http.Error(w, `{"error":"no such player"}`, http.StatusNotFound)
		return "", nil, false
	}
	return pid, database, true
}

type adminShip struct {
	ID    int32  `json:"id"`
	Name  string `json:"name"`
	Tier  int32  `json:"tier"`
	Hero  bool   `json:"hero"`
	Owned bool   `json:"owned"`
}

func adminAPIPlayerDetail(w http.ResponseWriter, r *http.Request) {
	pid, database, ok := adminPlayerPID(w, r)
	if !ok {
		return
	}
	var out struct {
		PID     string `json:"pid"`
		Name    string `json:"name"`
		Rank    int    `json:"rank"`
		Credits int64  `json:"credits"`
		Premium int64  `json:"premium"`
		FreeXP  int64  `json:"free_xp"`
		Online  bool   `json:"online"`
		Created string `json:"created"`
		// Banned: banned_at / ban_reason from admin_bans (admin_moderation.go).
		Banned    bool        `json:"banned"`
		BanReason string      `json:"ban_reason,omitempty"`
		BannedAt  string      `json:"banned_at,omitempty"`
		Matches   int         `json:"matches"`
		Wins      int         `json:"wins"`
		Kills     int         `json:"kills"`
		Deaths    int         `json:"deaths"`
		LastMatch string      `json:"last_match,omitempty"`
		Ships     []adminShip `json:"ships"`
		Heroes    []adminShip `json:"heroes"`
	}
	out.PID = pid
	if err := database.QueryRow(`SELECT COALESCE(display_name,''), current_rank, soft_currency, premium_currency, free_xp, created_at
		FROM player_state WHERE user_id=?`, pid).Scan(&out.Name, &out.Rank, &out.Credits, &out.Premium, &out.FreeXP, &out.Created); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if database.QueryRow(`SELECT reason, created_at FROM admin_bans WHERE user_id=?`, pid).Scan(&out.BanReason, &out.BannedAt) == nil {
		out.Banned = true
	}
	_ = database.QueryRow(`SELECT COUNT(*), COALESCE(SUM(outcome='win'),0), COALESCE(SUM(kills),0), COALESCE(SUM(deaths),0),
		COALESCE(MAX(created_at),'') FROM battle_results WHERE user_id=?`, pid).Scan(&out.Matches, &out.Wins, &out.Kills, &out.Deaths, &out.LastMatch)
	for _, p := range socialHubInstance.onlinePlayers() {
		if p.PID == pid {
			out.Online = true
		}
	}
	owned := map[int32]bool{}
	out.Ships = []adminShip{}
	for _, l := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(pid), pid) {
		owned[l.precastLoadoutID] = true
		tier, _ := shipTierForIDChecked(l.precastLoadoutID)
		out.Ships = append(out.Ships, adminShip{ID: l.precastLoadoutID, Name: l.loadoutName, Tier: tier,
			Hero: (l.precastLoadoutID>>24)&0xff == mmogItemCategoryShipLoadoutHero, Owned: true})
	}
	sort.Slice(out.Ships, func(i, j int) bool {
		if out.Ships[i].Tier != out.Ships[j].Tier {
			return out.Ships[i].Tier < out.Ships[j].Tier
		}
		return out.Ships[i].Name < out.Ships[j].Name
	})
	out.Heroes = []adminShip{}
	for _, h := range heroShipLoadouts {
		out.Heroes = append(out.Heroes, adminShip{ID: h.loadoutID, Name: h.name, Tier: h.tier, Hero: true, Owned: owned[h.loadoutID]})
	}
	sort.Slice(out.Heroes, func(i, j int) bool {
		if out.Heroes[i].Tier != out.Heroes[j].Tier {
			return out.Heroes[i].Tier < out.Heroes[j].Tier
		}
		return out.Heroes[i].Name < out.Heroes[j].Name
	})
	writeAdminJSON(w, out)
}

// adminAPIPlayerCurrency changes credits, GP (premium) and free XP. mode "add"
// adds the given amounts (negative subtracts, never below 0); mode "set"
// replaces the ones given. An omitted amount is left alone.
func adminAPIPlayerCurrency(w http.ResponseWriter, r *http.Request) {
	pid, database, ok := adminPlayerPID(w, r)
	if !ok {
		return
	}
	var req struct {
		Mode    string `json:"mode"`
		Credits *int64 `json:"credits"`
		Premium *int64 `json:"premium"`
		FreeXP  *int64 `json:"free_xp"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	if req.Mode != "add" && req.Mode != "set" {
		http.Error(w, `{"error":"mode must be add or set"}`, http.StatusBadRequest)
		return
	}
	if req.Credits == nil && req.Premium == nil && req.FreeXP == nil {
		http.Error(w, `{"error":"nothing to change"}`, http.StatusBadRequest)
		return
	}
	for _, v := range []*int64{req.Credits, req.Premium, req.FreeXP} {
		if v == nil {
			continue
		}
		if *v > adminMaxAmount || *v < -adminMaxAmount || (req.Mode == "set" && *v < 0) {
			http.Error(w, `{"error":"amount out of range"}`, http.StatusBadRequest)
			return
		}
	}
	column := func(name string, v *int64) error {
		if v == nil {
			return nil
		}
		query := `UPDATE player_state SET ` + name + `=?, updated_at=datetime('now') WHERE user_id=?`
		if req.Mode == "add" {
			query = `UPDATE player_state SET ` + name + `=MAX(0, ` + name + `+?), updated_at=datetime('now') WHERE user_id=?`
		}
		_, err := database.Exec(query, *v, pid)
		return err
	}
	for _, c := range []struct {
		name string
		v    *int64
	}{{"soft_currency", req.Credits}, {"premium_currency", req.Premium}, {"free_xp", req.FreeXP}} {
		if err := column(c.name, c.v); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	adminAudit(database, r, "balances", pid, map[string]any{"mode": req.Mode, "credits": req.Credits,
		"premium": req.Premium, "free_xp": req.FreeXP})
	// Online: credits/GP through YA_RewardCurrencies (assigned), free XP
	// through YA_ConvertShipXP with no ship entries (FreeXp assigned).
	squadHubInstance.push(pid, buildMmogRewardCurrenciesPayload(pid))
	if req.FreeXP != nil {
		squadHubInstance.push(pid, buildMmogShipXPSyncPush(pid, nil))
	}
	adminAPIPlayerDetail(w, r)
}

// adminAPIPlayerGrantShip grants a hero ship through the same path the admin
// provisioning tool uses (a zero-price "admin" purchase row plus the loadout).
func adminAPIPlayerGrantShip(w http.ResponseWriter, r *http.Request) {
	pid, database, ok := adminPlayerPID(w, r)
	if !ok {
		return
	}
	var req struct {
		ShipID int32 `json:"ship_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	isHero := false
	for _, h := range heroShipLoadouts {
		isHero = isHero || h.loadoutID == req.ShipID
	}
	if !isHero {
		http.Error(w, `{"error":"not a hero ship"}`, http.StatusBadRequest)
		return
	}
	for _, l := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(pid), pid) {
		if l.precastLoadoutID == req.ShipID {
			http.Error(w, `{"error":"the player already owns this ship"}`, http.StatusConflict)
			return
		}
	}
	if err := adminGrantShip(database, pid, req.ShipID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	adminAudit(database, r, "grant ship", pid, map[string]any{"ship_id": req.ShipID, "name": adminShipName(req.ShipID)})
	// Online: the ship itself (YA_ClaimItem addedLoadouts), then the fleets,
	// which a hero ship can unlock.
	if payload, ok := buildMmogShipClaimPush(pid, req.ShipID); ok {
		squadHubInstance.push(pid, payload)
	}
	squadHubInstance.push(pid, buildMmogFleetUpdatePush(pid))
	adminAPIPlayerDetail(w, r)
}

func adminGrantShip(database *sql.DB, pid string, shipID int32) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency)
		SELECT ?,?,'loadout',0,'admin' WHERE NOT EXISTS
		(SELECT 1 FROM player_purchases WHERE user_id=? AND item_id=?)`, pid, shipID, pid, shipID); err != nil {
		return err
	}
	if err := grantUnlockedShipLoadout(tx, pid, shipID); err != nil {
		return err
	}
	var granted int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM player_ship_loadouts WHERE user_id=? AND precast_loadout_id=?`,
		pid, shipID).Scan(&granted); err != nil {
		return err
	}
	if granted == 0 {
		return errors.New("the ship could not be granted (no loadout data for it)")
	}
	return tx.Commit()
}

func adminShipName(id int32) string {
	for _, h := range heroShipLoadouts {
		if h.loadoutID == id {
			return h.name
		}
	}
	return ""
}
