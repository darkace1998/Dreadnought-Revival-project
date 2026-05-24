package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
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

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type playerProfile struct {
	DisplayName string
	CreatedAt   string
}

func ensurePlayerStatsExec(exec execer, userID string) error {
	_, err := exec.Exec(`INSERT OR IGNORE INTO player_stats(user_id) VALUES(?)`, userID)
	return err
}

func (h *Handler) ensurePlayerStats(userID string) error {
	return ensurePlayerStatsExec(h.DB, userID)
}

// ensurePlayerProfile returns the player profile for the given user.
// starterReady is true when the profile already existed with stats
// initialized, meaning starter inventory has been bootstrapped.
func (h *Handler) ensurePlayerProfile(userID string) (profile playerProfile, starterReady bool, err error) {
	err = h.DB.QueryRow(
		`SELECT display_name, created_at FROM player_profiles WHERE user_id=?`, userID,
	).Scan(&profile.DisplayName, &profile.CreatedAt)
	if err == nil {
		starterReady = true
		if err = h.ensurePlayerStats(userID); err != nil {
			return
		}
		return
	}
	if err != sql.ErrNoRows {
		return
	}

	displayPrefix := userID
	if len(displayPrefix) > 8 {
		displayPrefix = userID[:8]
	}
	profile = playerProfile{
		DisplayName: "Player_" + displayPrefix,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	result, err := h.DB.Exec(
		`INSERT OR IGNORE INTO player_profiles(id,user_id,display_name) VALUES(?,?,?)`,
		uuid.New().String(), userID, profile.DisplayName,
	)
	if err != nil {
		return
	}
	if err = h.ensurePlayerStats(userID); err != nil {
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return
	}
	if rowsAffected > 0 {
		return
	}
	err = h.DB.QueryRow(
		`SELECT display_name, created_at FROM player_profiles WHERE user_id=?`, userID,
	).Scan(&profile.DisplayName, &profile.CreatedAt)
	return
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Tiles handles GET /v2/dreadnought/launcher/dn/tiles/
// Returns launcher news tiles to the Dreadnought launcher.
// legacy.js postApiCall() requires response.data.result to be non-null.
func (h *Handler) Tiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"result": map[string]interface{}{
			"tiles": []map[string]interface{}{
				{
					"id":           "welcome",
					"title":        "Welcome to the Private Server",
					"body":         "Community-operated private server. Have fun!",
					"type":         "announcement",
					"active":       true,
					"section_size": "full",
				},
				{
					"id":           fieldStatus,
					"title":        "Server Status",
					"body":         "Server is online. Connect and play!",
					"type":         "announcement",
					"active":       true,
					"section_size": "half",
				},
			},
		},
	})
}

// AgeConsent handles GET/POST /v2/dreadnought/ageconsent/
// legacy.js postApiCall() requires response.data.result to be non-null.
func (h *Handler) AgeConsent(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"result": map[string]interface{}{
			"consent_required": false,
			"consented":        true,
			"display_text":     "By playing on this private server you agree to have fun.",
			"region_detected":  false,
			"target_region":    nil,
		},
	})
}

// GetProfile handles GET /v2/dreadnought/player/{id}/profile
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := vars["id"]

	profile, _, err := h.ensurePlayerProfile(userID)
	if err != nil {
		h.Log.WithError(err).Error("get profile: ensure profile")
		writeError(w, http.StatusInternalServerError, "failed to fetch profile")
		return
	}

	var kills, deaths, matchesPlayed, wins, xpTotal, credits int
	err = h.DB.QueryRow(
		`SELECT kills,deaths,matches_played,wins,xp_total,credits FROM player_stats WHERE user_id=?`, userID,
	).Scan(&kills, &deaths, &matchesPlayed, &wins, &xpTotal, &credits)
	if err == sql.ErrNoRows {
		if err := h.ensurePlayerStats(userID); err != nil {
			h.Log.WithError(err).Error("get profile: initialize stats")
			writeError(w, http.StatusInternalServerError, "failed to initialize stats")
			return
		}
		if err := h.DB.QueryRow(
			`SELECT kills,deaths,matches_played,wins,xp_total,credits FROM player_stats WHERE user_id=?`, userID,
		).Scan(&kills, &deaths, &matchesPlayed, &wins, &xpTotal, &credits); err != nil {
			h.Log.WithError(err).Error("get profile: read default stats")
			writeError(w, http.StatusInternalServerError, "failed to fetch stats")
			return
		}
	} else if err != nil {
		h.Log.WithError(err).Error("get profile: stats query")
		writeError(w, http.StatusInternalServerError, "failed to fetch profile stats")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":      userID,
		"display_name": profile.DisplayName,
		"created_at":   profile.CreatedAt,
		"stats": map[string]interface{}{
			"kills":          kills,
			"deaths":         deaths,
			"matches_played": matchesPlayed,
			"wins":           wins,
			"xp_total":       xpTotal,
			"credits":        credits,
		},
	})
}

// GetInventory handles GET /v2/dreadnought/player/{id}/inventory
func (h *Handler) GetInventory(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := vars["id"]

	_, starterReady, err := h.ensurePlayerProfile(userID)
	if err != nil {
		h.Log.WithError(err).Error("get inventory: ensure profile")
		writeError(w, http.StatusInternalServerError, "failed to fetch inventory")
		return
	}

	rows, err := h.DB.Query(
		`SELECT id,item_type,item_id,acquired_at FROM player_inventory WHERE user_id=?`, userID,
	)
	if err != nil {
		h.Log.WithError(err).Error("get inventory: db query")
		writeError(w, http.StatusInternalServerError, "failed to fetch inventory")
		return
	}
	defer func() {
		_ = rows.Close()
	}()

	seedByKey := make(map[string]inventoryBootstrapSeed)
	for _, seed := range starterInventoryBootstrapSeeds() {
		seedByKey[inventorySeedKey(seed.ItemType, seed.ItemID)] = seed
	}
	legacySeedAliases := legacyStarterInventorySeedAliases()

	items := []InventoryItem{}
	existing := make(map[string]struct{})
	staleStarterRows := map[string]inventoryBootstrapSeed{}
	duplicateRows := map[string]struct{}{}
	for rows.Next() {
		var item InventoryItem
		if err := rows.Scan(&item.ID, &item.ItemType, &item.ItemID, &item.AcquiredAt); err != nil {
			h.Log.WithError(err).Error("get inventory: scan inventory item")
			writeError(w, http.StatusInternalServerError, "failed to read inventory")
			return
		}
		key := inventorySeedKey(item.ItemType, item.ItemID)
		seed, ok := seedByKey[key]
		if !ok {
			if aliasedSeed, aliased := legacySeedAliases[key]; aliased {
				item.ItemType = aliasedSeed.ItemType
				item.ItemID = aliasedSeed.ItemID
				key = inventorySeedKey(item.ItemType, item.ItemID)
				seed = aliasedSeed
				ok = true
				staleStarterRows[item.ID] = aliasedSeed
			}
		}
		if _, seen := existing[key]; seen {
			duplicateRows[item.ID] = struct{}{}
			continue
		}
		item.Owned = true
		if ok {
			item.Name = seed.Name
			item.ShipID = seed.ShipID
			item.LoadoutID = seed.LoadoutID
			item.SlotName = seed.SlotName
		}
		if !ok || starterReady {
			items = append(items, item)
		}
		existing[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		h.Log.WithError(err).Error("get inventory: iterate inventory")
		writeError(w, http.StatusInternalServerError, "failed to read inventory")
		return
	}

	for id, seed := range staleStarterRows {
		if _, err := h.DB.Exec(`UPDATE player_inventory SET item_type=?, item_id=? WHERE id=?`, seed.ItemType, seed.ItemID, id); err != nil {
			h.Log.WithError(err).Error("get inventory: normalize legacy starter inventory type")
			writeError(w, http.StatusInternalServerError, "failed to normalize inventory")
			return
		}
	}
	for id := range duplicateRows {
		if _, err := h.DB.Exec(`DELETE FROM player_inventory WHERE id=?`, id); err != nil {
			h.Log.WithError(err).Error("get inventory: remove duplicate starter inventory row")
			writeError(w, http.StatusInternalServerError, "failed to normalize inventory")
			return
		}
	}

	if starterReady {
		for _, seed := range starterInventoryBootstrapSeeds() {
			key := inventorySeedKey(seed.ItemType, seed.ItemID)
			if _, ok := existing[key]; ok {
				continue
			}
			itemID := uuid.New().String()
			acquiredAt := time.Now().UTC().Format(time.RFC3339)
			if _, err := h.DB.Exec(
				`INSERT INTO player_inventory(id,user_id,item_type,item_id) VALUES(?,?,?,?)`,
				itemID, userID, seed.ItemType, seed.ItemID,
			); err != nil {
				h.Log.WithError(err).Error("get inventory: failed to seed starter inventory item")
				writeError(w, http.StatusInternalServerError, "failed to seed inventory")
				return
			}
			items = append(items, inventoryItemFromSeed(seed, itemID, acquiredAt))
		}
	}

	starterShipIDs := []int32{}
	starterLoadoutIDs := []int32{}
	if starterReady {
		starterShipIDs = starterInventoryShipIDs()
		starterLoadoutIDs = starterInventoryLoadoutIDs()
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":             userID,
		"items":               items,
		"starter_ship_ids":    starterShipIDs,
		"starter_loadout_ids": starterLoadoutIDs,
	})
}

// PostMatchResult handles POST /v2/dreadnought/match/result
func (h *Handler) PostMatchResult(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<17) // 128KB limit
	var req struct {
		MatchID string `json:"match_id"`
		Mode    string `json:"mode"`
		Map     string `json:"map"`
		Players []struct {
			UserID string `json:"user_id"`
			Team   int    `json:"team"`
			Score  int    `json:"score"`
			Kills  int    `json:"kills"`
			Deaths int    `json:"deaths"`
			Damage int    `json:"damage"`
			Won    bool   `json:"won"`
		} `json:"players"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	matchID := req.MatchID
	if matchID == "" {
		matchID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := h.DB.Begin()
	if err != nil {
		h.Log.WithError(err).Error("post match result: begin tx")
		writeError(w, http.StatusInternalServerError, "failed to record match")
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec(
		`INSERT OR IGNORE INTO match_history(id,mode,map,started_at,ended_at) VALUES(?,?,?,?,?)`,
		matchID, req.Mode, req.Map, now, now,
	); err != nil {
		h.Log.WithError(err).Error("post match result: insert match history")
		writeError(w, http.StatusInternalServerError, "failed to record match")
		return
	}

	for _, p := range req.Players {
		if _, err = tx.Exec(
			`INSERT OR REPLACE INTO match_players(match_id,user_id,team,score,kills,deaths,damage) VALUES(?,?,?,?,?,?,?)`,
			matchID, p.UserID, p.Team, p.Score, p.Kills, p.Deaths, p.Damage,
		); err != nil {
			h.Log.WithError(err).Error("post match result: upsert match player")
			writeError(w, http.StatusInternalServerError, "failed to record match")
			return
		}
		winVal := 0
		if p.Won {
			winVal = 1
		}
		if err = ensurePlayerStatsExec(tx, p.UserID); err != nil {
			h.Log.WithError(err).Error("post match result: ensure player stats")
			writeError(w, http.StatusInternalServerError, "failed to record match")
			return
		}
		scoreXP := p.Score / 10
		if scoreXP < 1 {
			scoreXP = 1
		}
		if _, err = tx.Exec(`UPDATE player_stats SET
			kills=kills+?,
			deaths=deaths+?,
			matches_played=matches_played+1,
			wins=wins+?,
			xp_total=xp_total+?,
			updated_at=datetime('now')
			WHERE user_id=?`,
			p.Kills, p.Deaths, winVal, scoreXP+50, p.UserID,
		); err != nil {
			h.Log.WithError(err).Error("post match result: update player stats")
			writeError(w, http.StatusInternalServerError, "failed to record match")
			return
		}
	}

	if err = tx.Commit(); err != nil {
		h.Log.WithError(err).Error("post match result: commit tx")
		writeError(w, http.StatusInternalServerError, "failed to record match")
		return
	}
	err = nil

	for _, p := range req.Players {
		scoreXP := p.Score/10 + 50
		if scoreXP < 1 {
			scoreXP = 1
		}
		go func(userID string, xp, kills, deaths int, won bool) {
			winVal := 0
			if won {
				winVal = 1
			}
			body, _ := json.Marshal(map[string]interface{}{
				"user_id":  userID,
				"xp":       xp,
				"kills":    kills,
				"deaths":   deaths,
				"wins":     winVal,
				"match_xp": xp,
			})
			resp, callErr := http.Post("http://127.0.0.1:8083/mmog/progression", "application/json", bytes.NewReader(body))
			if callErr != nil {
				h.Log.WithError(callErr).WithField("user_id", userID).Warn("post match: mmog progression call failed")
				return
			}
			_ = resp.Body.Close()
		}(p.UserID, scoreXP, p.Kills, p.Deaths, p.Won)
	}

	writeJSON(w, http.StatusOK, map[string]string{"match_id": matchID, fieldStatus: "recorded"})
}

// Health handles GET /health
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	dbOK := "ok"
	if err := h.DB.Ping(); err != nil {
		dbOK = "error"
	}
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "ok", "service": "legacy-api", "database": dbOK})
}
