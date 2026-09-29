package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
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
//
// Tiles live in launcher_tiles (edited via /admin/tiles), seeded with the
// two defaults below on a fresh database so an upgrade serves exactly what
// the hardcoded version served.
func (h *Handler) Tiles(w http.ResponseWriter, r *http.Request) {
	h.ensureDefaultTiles()
	rows, err := h.DB.Query(`SELECT id,title,body,type,active,section_size FROM launcher_tiles ORDER BY rowid`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	tiles := []map[string]interface{}{}
	for rows.Next() {
		var id, title, body, tileType, sectionSize string
		var active int
		if err := rows.Scan(&id, &title, &body, &tileType, &active, &sectionSize); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		tiles = append(tiles, map[string]interface{}{
			"id": id, "title": title, "body": body, "type": tileType,
			"active": active != 0, "section_size": sectionSize,
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"result": map[string]interface{}{"tiles": tiles},
	})
}

// defaultLauncherTiles is the seed content: what the hardcoded handler
// served before tiles became editable.
var defaultLauncherTiles = []map[string]string{
	{
		"id": "welcome", "title": "Welcome to the Private Server",
		"body": "Community-operated private server. Have fun!",
		"type": "announcement", "section_size": "full",
	},
	{
		"id": "status", "title": "Server Status",
		"body": "Server is online. Connect and play!",
		"type": "announcement", "section_size": "half",
	},
}

func (h *Handler) ensureDefaultTiles() {
	for _, t := range defaultLauncherTiles {
		_, _ = h.DB.Exec(`INSERT OR IGNORE INTO launcher_tiles(id,title,body,type,active,section_size)
			VALUES(?,?,?,?,1,?)`, t["id"], t["title"], t["body"], t["type"], t["section_size"])
	}
}

// validTileID keeps ids URL-safe: they appear in no URL today, but the
// launcher keys tiles by id and a slash or quote would corrupt its lookup.
func validTileID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// AdminTiles handles GET /admin/tiles — every tile including inactive ones,
// for the operator editor (the public Tiles endpoint serves the same rows).
func (h *Handler) AdminTiles(w http.ResponseWriter, r *http.Request) {
	h.ensureDefaultTiles()
	rows, err := h.DB.Query(`SELECT id,title,body,type,active,section_size,updated_at
		FROM launcher_tiles ORDER BY rowid`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	type tile struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Body        string `json:"body"`
		Type        string `json:"type"`
		Active      bool   `json:"active"`
		SectionSize string `json:"section_size"`
		UpdatedAt   string `json:"updated_at"`
	}
	out := []tile{}
	for rows.Next() {
		var t tile
		var active int
		if err := rows.Scan(&t.ID, &t.Title, &t.Body, &t.Type, &active, &t.SectionSize, &t.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		t.Active = active != 0
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tiles": out, "count": len(out)})
}

// AdminUpsertTile handles POST /admin/tiles — creates or replaces one tile.
// Missing optional fields keep their defaults; the launcher reads the result
// on its next home-screen load, no restart needed anywhere.
func (h *Handler) AdminUpsertTile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Body        string `json:"body"`
		Type        string `json:"type"`
		Active      *bool  `json:"active"`
		SectionSize string `json:"section_size"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !validTileID(req.ID) {
		writeError(w, http.StatusBadRequest, "id must be 1-64 chars of letters, digits, _ or -")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" || len([]rune(title)) > 120 {
		writeError(w, http.StatusBadRequest, "title required (max 120 chars)")
		return
	}
	if len([]rune(req.Body)) > 2000 {
		writeError(w, http.StatusBadRequest, "body too long (max 2000 chars)")
		return
	}
	tileType := strings.TrimSpace(req.Type)
	if tileType == "" {
		tileType = "announcement"
	}
	if tileType != "announcement" && tileType != "event" && tileType != "maintenance" {
		writeError(w, http.StatusBadRequest, "type must be announcement, event or maintenance")
		return
	}
	sectionSize := strings.TrimSpace(req.SectionSize)
	if sectionSize == "" {
		sectionSize = "full"
	}
	if sectionSize != "full" && sectionSize != "half" {
		writeError(w, http.StatusBadRequest, "section_size must be full or half")
		return
	}
	active := 1
	if req.Active != nil && !*req.Active {
		active = 0
	}
	if _, err := h.DB.Exec(`INSERT INTO launcher_tiles(id,title,body,type,active,section_size,updated_at)
		VALUES(?,?,?,?,?,?,datetime('now'))
		ON CONFLICT(id) DO UPDATE SET title=excluded.title, body=excluded.body, type=excluded.type,
			active=excluded.active, section_size=excluded.section_size, updated_at=datetime('now')`,
		req.ID, title, req.Body, tileType, active, sectionSize); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	h.Log.WithField("tile", req.ID).Info("launcher tile saved")
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "ok", "id": req.ID})
}

// AdminDeleteTile handles DELETE /admin/tiles/{id}.
func (h *Handler) AdminDeleteTile(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !validTileID(id) {
		writeError(w, http.StatusBadRequest, "invalid tile id")
		return
	}
	res, err := h.DB.Exec(`DELETE FROM launcher_tiles WHERE id=?`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "no such tile")
		return
	}
	h.Log.WithField("tile", id).Info("launcher tile deleted")
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "deleted", "id": id})
}

// AdminHistory handles GET /admin/matches — legacy match history (newest
// first) with each match's roster. match_history/match_players are written by
// PostMatchResult; this is their operator-readable view.
func (h *Handler) AdminHistory(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := h.DB.Query(`SELECT id,mode,map,started_at,ended_at FROM match_history
		ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	type participant struct {
		UserID string `json:"user_id"`
		Team   int    `json:"team"`
		Kills  int    `json:"kills"`
		Deaths int    `json:"deaths"`
		Damage int    `json:"damage"`
	}
	type match struct {
		ID       string        `json:"id"`
		Mode     string        `json:"mode"`
		Map      string        `json:"map"`
		Started  string        `json:"started_at"`
		Ended    string        `json:"ended_at"`
		Players  []participant `json:"players"`
	}
	out := []match{}
	for rows.Next() {
		var m match
		var ended sql.NullString
		if err := rows.Scan(&m.ID, &m.Mode, &m.Map, &m.Started, &ended); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		m.Ended = ended.String
		m.Players = []participant{}
		prows, err := h.DB.Query(`SELECT user_id,team,kills,deaths,damage FROM match_players
			WHERE match_id=? ORDER BY kills DESC`, m.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		for prows.Next() {
			var p participant
			if err := prows.Scan(&p.UserID, &p.Team, &p.Kills, &p.Deaths, &p.Damage); err != nil {
				_ = prows.Close()
				writeError(w, http.StatusInternalServerError, "db error")
				return
			}
			m.Players = append(m.Players, p)
		}
		_ = prows.Close()
		if err := prows.Err(); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"matches": out, "count": len(out)})
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
	if userID != r.Header.Get("X-User-ID") {
		writeError(w, http.StatusForbidden, "cannot access another player's profile")
		return
	}

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
	if userID != r.Header.Get("X-User-ID") {
		writeError(w, http.StatusForbidden, "cannot access another player's inventory")
		return
	}

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
			UserID      string `json:"user_id"`
			Team        int    `json:"team"`
			Score       int    `json:"score"`
			Kills       int    `json:"kills"`
			Deaths      int    `json:"deaths"`
			Damage      int    `json:"damage"`
			Won         bool   `json:"won"`
			Assists     int    `json:"assists"`
			HealingDone int    `json:"healing_done"`
			DamageTaken int    `json:"damage_taken"`
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
			damage_dealt=damage_dealt+?,
			damage_taken=damage_taken+?,
			assists=assists+?,
			healing_done=healing_done+?,
			updated_at=datetime('now')
			WHERE user_id=?`,
			p.Kills, p.Deaths, winVal, scoreXP+50, p.Damage, p.DamageTaken, p.Assists, p.HealingDone, p.UserID,
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
		go func(userID string, xp, kills, deaths int, won bool, gameMode string) {
			winVal := 0
			if won {
				winVal = 1
			}
			body, _ := json.Marshal(map[string]interface{}{
				"user_id":   userID,
				"xp":        xp,
				"kills":     kills,
				"deaths":    deaths,
				"wins":      winVal,
				"match_xp":  xp,
				"game_mode": gameMode,
			})
			req, reqErr := newMmogProgressionRequest(body)
			if reqErr != nil {
				h.Log.WithError(reqErr).WithField("user_id", userID).Warn("post match: build mmog progression request failed")
				return
			}
			resp, callErr := http.DefaultClient.Do(req)
			if callErr != nil {
				h.Log.WithError(callErr).WithField("user_id", userID).Warn("post match: mmog progression call failed")
				return
			}
			_ = resp.Body.Close()
		}(p.UserID, scoreXP, p.Kills, p.Deaths, p.Won, req.Mode)
	}

	writeJSON(w, http.StatusOK, map[string]string{"match_id": matchID, fieldStatus: "recorded"})
}

// Health handles GET /health
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	if err := h.DB.Ping(); err != nil {
		h.Log.WithError(err).Warn("health: database ping failed")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			fieldStatus: "error",
			"service":   "legacy-api",
			"database":  "error",
			"error":     "database unreachable",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "ok", "service": "legacy-api", "database": "ok"})
}

func newMmogProgressionRequest(body []byte) (*http.Request, error) {
	internalKey := getenv("INTERNAL_API_KEY", getenv("ADMIN_KEY", "changeme-admin-key"))
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:8083/internal/progression", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", internalKey)
	return req, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ServerStatus handles GET /v2/dreadnought/server/status
func (h *Handler) ServerStatus(w http.ResponseWriter, r *http.Request) {
	var playersOnline, matchesActive int
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM player_stats WHERE matches_played > 0`).Scan(&playersOnline)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		fieldStatus:      "ok",
		"players_online": playersOnline,
		"matches_active": matchesActive,
		"server_load":    "low",
		"maintenance":    false,
	})
}

// Store handles GET /store — returns catalog items with prices
func (h *Handler) Store(w http.ResponseWriter, r *http.Request) {
	type storeItem struct {
		ItemID      int32  `json:"item_id"`
		ItemType    string `json:"item_type"`
		DisplayName string `json:"display_name"`
		Price       int32  `json:"price"`
		Currency    string `json:"currency"`
	}
	items := []storeItem{
		{33489265, "ship", "Valcour", 5000, "gp"},
		{33489266, "ship", "Leipzig", 5000, "gp"},
		{33489267, "ship", "Trieste", 5000, "gp"},
		{33489268, "ship", "Ceres", 5000, "gp"},
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		fieldStatus: "ok",
		"items":     items,
	})
}

// TechTree handles GET /v2/dreadnought/techtree
func (h *Handler) TechTree(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		fieldStatus: "ok",
		"techtree":  "see mmogbrain YA_GetTechTree for full data",
	})
}

// Season handles GET /season
func (h *Handler) Season(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		fieldStatus: "ok",
		"seasons":   "see mmogbrain YA_GetSeasonData for full data",
	});
}

// Projectiles handles GET /v2/dreadnought/projectiles
// Returns all projectile data for the game client
func (h *Handler) Projectiles(w http.ResponseWriter, r *http.Request) {
	projectiles := dreadconfig.AllProjectiles()
	
	// Convert projectiles to a format suitable for the client
	projectileData := make([]map[string]interface{}, 0, len(projectiles))
	for rowName, projectile := range projectiles {
		projectileMap := make(map[string]interface{})
		// Use reflection to convert all fields to a map
		val := reflect.ValueOf(projectile)
		typeOf := val.Type()
		
		for i := 0; i < val.NumField(); i++ {
			field := val.Field(i)
			fieldName := typeOf.Field(i).Name
			// Convert field name to snake_case for JSON
			jsonName := toSnakeCase(fieldName)
			projectileMap[jsonName] = field.Interface()
		}
		projectileMap["row_name"] = rowName
		projectileData = append(projectileData, projectileMap)
	}
	
	writeJSON(w, http.StatusOK, map[string]interface{}{
		fieldStatus:    "ok",
		"projectiles": projectileData,
	});
}

// ShipFeats returns all ship feat data
func (h *Handler) ShipFeats(w http.ResponseWriter, r *http.Request) {
	shipFeats := dreadconfig.AllShipFeats()
	
	// Convert ship feats to a format suitable for the client
	shipFeatData := make([]map[string]interface{}, 0, len(shipFeats))
	for compositeName, feat := range shipFeats {
		featMap := make(map[string]interface{})
		// Use reflection to convert all fields to a map
		val := reflect.ValueOf(feat)
		typeOf := val.Type()
		
		for i := 0; i < val.NumField(); i++ {
			field := val.Field(i)
			fieldName := typeOf.Field(i).Name
			// Convert field name to snake_case for JSON
			jsonName := toSnakeCase(fieldName)
			featMap[jsonName] = field.Interface()
		}
		featMap["composite_name"] = compositeName
		shipFeatData = append(shipFeatData, featMap)
	}
	
	writeJSON(w, http.StatusOK, map[string]interface{}{
		fieldStatus: "ok",
		"ship_feats": shipFeatData,
	});
}

// Abilities returns all ability data (E5)
func (h *Handler) Abilities(w http.ResponseWriter, r *http.Request) {
	abilities := dreadconfig.AllAbilities()
	
	// Convert abilities to a format suitable for the client
	abilityData := make([]map[string]interface{}, 0, len(abilities))
	for compositeName, ability := range abilities {
		abilityMap := make(map[string]interface{})
		// Use reflection to convert all fields to a map
		val := reflect.ValueOf(ability)
		typeOf := val.Type()
		
		for i := 0; i < val.NumField(); i++ {
			field := val.Field(i)
			fieldName := typeOf.Field(i).Name
			// Convert field name to snake_case for JSON
			jsonName := toSnakeCase(fieldName)
			// Skip unexported fields
			if fieldName == "AbilityStats" {
				continue
			}
			abilityMap[jsonName] = field.Interface()
		}
		abilityMap["composite_name"] = compositeName
		abilityData = append(abilityData, abilityMap)
	}
	
	writeJSON(w, http.StatusOK, map[string]interface{}{
		fieldStatus: "ok",
		"abilities": abilityData,
	});
}

// toSnakeCase converts CamelCase to snake_case
func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				result.WriteRune('_')
			}
			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// XPConvert handles POST /xp/convert
func (h *Handler) XPConvert(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req struct {
		ShipID int32 `json:"ship_id"`
		Amount int32 `json:"amount"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		fieldStatus:    "ok",
		"converted_xp": req.Amount / 2,
		"free_xp":      req.Amount / 2,
	})
}
