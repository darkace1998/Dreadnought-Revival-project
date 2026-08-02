package matchmaker

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// GameMap is a playable map: the name the server records and the package path
// the engine actually loads.
type GameMap struct {
	Name string
	Path string
}

// availableMaps is the multiplayer rotation, taken from the client's own
// GlobalUI.uasset (UYUIData::m_multiplayerMaps), which pairs each m_mapName with
// the m_mapPath the engine loads.
//
// The previous list -- Charon, Medusa, Procyon, DS-75, Onyx, Vesta, Kylo, Spree
// -- was invented. None of those names appears anywhere in the game, so every
// match was formed against a map that does not exist. Only the five flagged
// m_avaliableTM are used here; the rest of the table is night variants, Havoc
// maps and the tutorial.
var availableMaps = []GameMap{
	{Name: "Highlands", Path: "/Game/Maps/MP/Highlands/MP_Highlands_P"},
	{Name: "Glacier", Path: "/Game/Maps/MP/Glacier/MP_Glacier_P"},
	{Name: "Gorge", Path: "/Game/Maps/MP/Gorge/MP_Gorge_P"},
	{Name: "Space", Path: "/Game/Maps/MP/Space01/MP_Space01_P"},
	{Name: "Skybridge", Path: "/Game/Maps/MP/Skybridge/MP_Skybridge_P"},
}

// GameModeConfig is the mmogbrain game-mode row shape consumed by the client.
type GameModeConfig struct {
	Name     string
	TeamSize int32
}

var clientGameModeConfigs = []GameModeConfig{
	{Name: "TDM", TeamSize: 5},
	{Name: "PodTDM", TeamSize: 5},
	{Name: "TE", TeamSize: 5},
	{Name: "TM", TeamSize: 5},
	{Name: "TER", TeamSize: 5},
	{Name: "Territory", TeamSize: 5},
	{Name: "Onslaught", TeamSize: 5},
	{Name: "BC", TeamSize: 1},
	{Name: "Bootcamp", TeamSize: 1},
	{Name: "TurboTDM", TeamSize: 5},
}

var gameModeAliases = map[string]string{
	"TeamDeathMatch":   "TDM",
	"TeamDeathmatch":   "TDM",
	"TeamElimination":  "TE",
	"TerritoryControl": "TER",
	"BootCamp":         "BC",
	"HAVOC":            "Onslaught",
	"PvE_Standard":     "Onslaught",
	"PvE_Havoc":        "Onslaught",
	"PvE_Onslaught":    "Onslaught",
	"PvE_Coop":         "Onslaught",
	"Training":         "BC",
}

// validGameModes lists client names plus legacy server aliases accepted by the server.
var validGameModes = buildValidGameModes()

func buildValidGameModes() map[string]bool {
	modes := make(map[string]bool, len(clientGameModeConfigs)+len(gameModeAliases))
	for _, mode := range clientGameModeConfigs {
		modes[mode.Name] = true
	}
	for alias := range gameModeAliases {
		modes[alias] = true
	}
	return modes
}

// DefaultGameMode is what a wildcard request resolves to. The queue groups by
// exact mode, so a request for "any" has to land on a concrete one.
const DefaultGameMode = "TDM"

// IsWildcardGameMode reports whether the client asked for no particular mode.
// The quick-play button sends "ANY"; the mode field can also arrive empty or as
// the request's own name, "*matchmaking".
func IsWildcardGameMode(mode string) bool {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case "", "ANY", "*MATCHMAKING", "ALL":
		return true
	}
	return false
}

// NormalizeGameMode returns the client-config alias used by the dedicated server.
func NormalizeGameMode(mode string) string {
	if canonical, ok := gameModeAliases[mode]; ok {
		return canonical
	}
	return mode
}

// pveMaps lists maps available for PvE modes. Amirani and Derelict are real
// entries in the same client table; "Iapetus" and "Kalyke", which used to sit
// beside them here, are not maps this game has.
// mapsByGameMode pins modes that only work on particular maps, and wins over
// the availableMaps/pveMaps split below.
//
// TM (Training Match) is the one mode whose game info supplies the PLAYER's
// loadout itself: GameInfo_TM_BP sets m_trainingMatchLoadout to
// /Game/Generic/Loadouts/Precast/Tutorial/VH_DreadnoughtMedium_TutorialPlayer_PrecastLoadout_BP.
// That matters because AYGameMode's spawn path takes the game mode's loadout
// when it has one and only falls back to the player's own fleet otherwise --
// and the fallback needs backend fleet data the battle server does not have.
// So TM is currently the only mode that can spawn a pawn at all.
//
// It has to run on Highlands: that is the only map in the build shipping a TM
// level variation (MP_Highlands_TM.umap). On other maps the orbit manager logs
// "ActivateBattlePlayerStarts: no orbit spawn locations set!".
var mapsByGameMode = map[string][]GameMap{
	"TM":      {{Name: "Highlands", Path: "/Game/Maps/MP/Highlands/MP_Highlands_P"}},
	"TMBasic": {{Name: "Highlands", Path: "/Game/Maps/MP/Highlands/MP_Highlands_P"}},
}

var pveMaps = []GameMap{
	{Name: "Amirani", Path: "/Game/Maps/MP/Amirani/MP_Amirani_P"},
	{Name: "Derelict", Path: "/Game/Maps/MP/Derelict/MP_Derelict_P"},
}

// ValidGameMode returns true if the given mode is supported.
func ValidGameMode(mode string) bool {
	return validGameModes[mode]
}

// GameModeList returns all supported game modes.
func GameModeList() []string {
	modes := make([]string, 0, len(clientGameModeConfigs))
	for _, mode := range clientGameModeConfigs {
		modes = append(modes, mode.Name)
	}
	return modes
}

// GameModeConfigs returns the deterministic client-facing game mode rows.
func GameModeConfigs() []GameModeConfig {
	return append([]GameModeConfig(nil), clientGameModeConfigs...)
}

// Matchmaker polls the queue and fires match creation when enough players are present.
type Matchmaker struct {
	DB         *sql.DB
	Log        *logrus.Logger
	GameMgrURL string // e.g. http://127.0.0.1:8085
	// InternalKey authenticates to game-manager. Its /instances route sits
	// behind internalKeyMiddleware and answers 403 without the X-Internal-Key
	// header, which is exactly what happened: every tick formed a match, got a
	// 403, rolled the queue entries back to 'waiting' and tried again three
	// seconds later, forever.
	InternalKey     string
	PlayersPerMatch int
	ticker          *time.Ticker
	stop            chan struct{}
}

// New creates a Matchmaker.
func New(db *sql.DB, log *logrus.Logger, gameMgrURL, internalKey string, playersPerMatch int) *Matchmaker {
	if playersPerMatch < 1 {
		playersPerMatch = 2 // minimum for testing; production is 10
	}
	return &Matchmaker{
		DB:              db,
		Log:             log,
		GameMgrURL:      gameMgrURL,
		InternalKey:     internalKey,
		PlayersPerMatch: playersPerMatch,
		stop:            make(chan struct{}),
	}
}

// Start begins the matchmaking loop.
func (m *Matchmaker) Start() {
	m.ticker = time.NewTicker(3 * time.Second)
	go func() {
		for {
			select {
			case <-m.ticker.C:
				if err := m.tick(); err != nil {
					m.Log.WithError(err).Warn("matchmaker tick error")
				}
			case <-m.stop:
				m.ticker.Stop()
				return
			}
		}
	}()
	m.Log.WithField("players_per_match", m.PlayersPerMatch).Info("matchmaker started")
}

// Stop halts the matchmaking loop.
func (m *Matchmaker) Stop() {
	close(m.stop)
}

// MaxMatchLifetime is how long a match may stay 'active' before the sweep ends
// it.
//
// Nothing else ever ends a match. game-manager knows when a battle server exits
// but does not tell mmogbrain, so every match ever formed stayed 'active'
// forever -- and currentMmogMatchmakingStatus reports a player with a slot in an
// active match as already "matched". The effect: once a player had been in ONE
// match they could never queue again. Their client would go straight from
// Searching to "Battle server starting" and wait forever for a server that had
// died, in one observed case, a day earlier.
//
// A real match cannot outlive this by any sane margin, so anything older is
// wreckage.
const MaxMatchLifetime = 45 * time.Minute

// sweepStaleMatches ends matches that have outlived MaxMatchLifetime and frees
// the players bound to them.
func (m *Matchmaker) sweepStaleMatches() error {
	cutoff := time.Now().UTC().Add(-MaxMatchLifetime).Format(time.RFC3339)

	// Compare through SQLite's datetime() on BOTH sides. created_at is written
	// in two different formats -- the schema default is datetime('now')
	// ("2026-08-01 21:33:00") while formMatch writes RFC3339
	// ("2026-08-01T21:33:00Z") -- and a plain string comparison between them is
	// meaningless: ' ' sorts before 'T', so every datetime()-format row looked
	// older than any RFC3339 cutoff and would have been swept immediately,
	// including live matches. datetime() parses both.
	rows, err := m.DB.Query(
		`SELECT id FROM matches WHERE status='active' AND datetime(created_at) < datetime(?)`, cutoff)
	if err != nil {
		return fmt.Errorf("query stale matches: %w", err)
	}
	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan stale match: %w", err)
		}
		stale = append(stale, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate stale matches: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close stale match rows: %w", err)
	}

	for _, id := range stale {
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := m.DB.Exec(`UPDATE matches SET status='ended', ended_at=? WHERE id=?`, now, id); err != nil {
			return fmt.Errorf("end stale match %s: %w", id, err)
		}
		// The slots must go too: they are what binds a player to the match, and
		// leaving them behind would keep reporting the player as matched.
		if _, err := m.DB.Exec(`DELETE FROM match_slots WHERE match_id=?`, id); err != nil {
			return fmt.Errorf("clear slots for stale match %s: %w", id, err)
		}
		m.Log.WithField("match_id", id).Info("ended stale match and freed its players")
	}

	// Slots whose match row no longer exists at all. These cannot pin a player
	// -- the status lookup inner-joins matches -- but nothing else ever removes
	// them, so they would grow without bound.
	if _, err := m.DB.Exec(
		`DELETE FROM match_slots WHERE match_id NOT IN (SELECT id FROM matches)`); err != nil {
		return fmt.Errorf("clear orphaned match slots: %w", err)
	}
	return nil
}

func (m *Matchmaker) tick() error {
	if err := m.sweepStaleMatches(); err != nil {
		m.Log.WithError(err).Warn("stale match sweep failed")
	}

	// Group by game_mode and tier_min to match players within compatible tier ranges.
	// Players with the same tier_min are treated as compatible for matchmaking.
	rows, err := m.DB.Query(`
		SELECT game_mode, tier_min, COUNT(*) as cnt
		FROM queue_entries WHERE status='waiting'
		GROUP BY game_mode, tier_min
		HAVING cnt >= ?
	`, m.PlayersPerMatch)
	if err != nil {
		return fmt.Errorf("queue query: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	type bucket struct {
		GameMode string
		TierMin  int
		Count    int
	}
	var ready []bucket
	for rows.Next() {
		var b bucket
		if err := rows.Scan(&b.GameMode, &b.TierMin, &b.Count); err != nil {
			return fmt.Errorf("scan ready queue counts: %w", err)
		}
		ready = append(ready, b)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate ready queue counts: %w", err)
	}

	for _, b := range ready {
		if err := m.formMatch(b.GameMode, b.TierMin); err != nil {
			m.Log.WithError(err).WithFields(logrus.Fields{
				"game_mode": b.GameMode,
				"tier_min":  b.TierMin,
			}).Warn("form match failed")
		}
	}
	return nil
}

func (m *Matchmaker) formMatch(gameMode string, tierMin int) error {
	// Pull the oldest waiting players for this mode and tier
	rows, err := m.DB.Query(`
		SELECT id, user_id FROM queue_entries
		WHERE status='waiting' AND game_mode=? AND tier_min=?
		ORDER BY queued_at ASC
		LIMIT ?
	`, gameMode, tierMin, m.PlayersPerMatch)
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	type entry struct {
		ID     string
		UserID string
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.ID, &e.UserID); err != nil {
			return fmt.Errorf("scan queue entries: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate queue entries: %w", err)
	}

	if len(entries) < m.PlayersPerMatch {
		return nil
	}

	// Mark them as matched
	for _, e := range entries {
		if _, err := m.DB.Exec(`UPDATE queue_entries SET status='matched' WHERE id=?`, e.ID); err != nil {
			return fmt.Errorf("mark queue entry matched %s: %w", e.ID, err)
		}
	}

	// Pick a map
	maps := availableMaps
	if modeMaps, ok := mapsByGameMode[gameMode]; ok {
		maps = modeMaps
	} else {
		for _, pveMode := range []string{"Onslaught", "BC"} {
			if gameMode == pveMode {
				maps = pveMaps
				break
			}
		}
	}
	chosen := maps[time.Now().UnixNano()%int64(len(maps))]
	mapName := chosen.Name

	// Request a game instance from the game manager
	playerIDs := make([]string, len(entries))
	for i, e := range entries {
		playerIDs[i] = e.UserID
	}
	serverIP, serverPort, instanceID, err := m.requestGameInstance(gameMode, mapName, chosen.Path, playerIDs)
	if err != nil {
		// Rollback queue entries on failure
		for _, e := range entries {
			if _, rollbackErr := m.DB.Exec(`UPDATE queue_entries SET status='waiting' WHERE id=?`, e.ID); rollbackErr != nil {
				m.Log.WithError(rollbackErr).WithField("queue_entry_id", e.ID).Warn("rollback queue entry")
			}
		}
		return fmt.Errorf("request game instance: %w", err)
	}

	// Record match in DB
	matchID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := m.DB.Exec(
		`INSERT INTO matches(id,game_mode,map,server_ip,server_port,status,created_at,started_at) VALUES(?,?,?,?,?,?,?,?)`,
		matchID, gameMode, mapName, serverIP, serverPort, "active", now, now,
	); err != nil {
		return fmt.Errorf("insert match %s: %w", matchID, err)
	}
	for i, e := range entries {
		team := i % 2
		if _, err := m.DB.Exec(
			`INSERT INTO match_slots(match_id,user_id,team) VALUES(?,?,?)`,
			matchID, e.UserID, team,
		); err != nil {
			return fmt.Errorf("insert match slot for user %s: %w", e.UserID, err)
		}
	}

	// Remove matched queue entries
	for _, e := range entries {
		if _, err := m.DB.Exec(`DELETE FROM queue_entries WHERE id=?`, e.ID); err != nil {
			return fmt.Errorf("delete matched queue entry %s: %w", e.ID, err)
		}
	}

	m.Log.WithFields(logrus.Fields{
		"match_id":    matchID,
		"instance_id": instanceID,
		"game_mode":   gameMode,
		"tier_min":    tierMin,
		"map":         mapName,
		"players":     len(entries),
		"server":      fmt.Sprintf("%s:%d", serverIP, serverPort),
	}).Info("match formed")

	return nil
}

// gameManagerHTTPClient replaces http.DefaultClient, which has NO timeout.
//
// The matchmaker runs every tick on one goroutine, so a game-manager that
// accepts the connection and then never answers blocks that goroutine forever:
// no further ticks, no stale-match sweep, no new matches for anyone, and the
// queue entries for the in-flight match stay 'matched' with nothing to roll
// them back. Only restarting mmogbrain recovers it.
//
// The timeout is generous because a spawn can legitimately take a while, but it
// is finite. DN_GAME_MGR_TIMEOUT overrides it for a control plane that is
// slower still -- e.g. one that waits for the engine to report ready before
// answering.
var gameManagerHTTPClient = &http.Client{Timeout: gameManagerRequestTimeout()}

func gameManagerRequestTimeout() time.Duration {
	if raw := os.Getenv("DN_GAME_MGR_TIMEOUT"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

func (m *Matchmaker) requestGameInstance(gameMode, mapName, mapPath string, players []string) (string, int, string, error) {
	body, err := json.Marshal(map[string]interface{}{
		"game_mode": gameMode,
		"map":       mapName,
		"map_path":  mapPath,
		"players":   players,
	})
	if err != nil {
		return "", 0, "", fmt.Errorf("marshal game manager request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/instances", m.GameMgrURL), bytes.NewReader(body))
	if err != nil {
		return "", 0, "", fmt.Errorf("build game manager request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", m.InternalKey)
	resp, err := gameManagerHTTPClient.Do(req)
	if err != nil {
		return "", 0, "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusCreated {
		if resp.StatusCode == http.StatusForbidden {
			return "", 0, "", fmt.Errorf("game manager returned 403: INTERNAL_API_KEY (or ADMIN_KEY) must match game-manager's")
		}
		return "", 0, "", fmt.Errorf("game manager returned %d", resp.StatusCode)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, "", fmt.Errorf("decode game manager response: %w", err)
	}

	ip, _ := result["ip"].(string)
	portF, _ := result["port"].(float64)
	instID, _ := result["instance_id"].(string)
	// A 201 with no usable address is worse than an error: the match is
	// recorded as active, the player is pushed at ":0" or at nothing, and they
	// wait on "Battle server starting" with everything server-side looking
	// healthy. Refuse it here so formMatch rolls the queue entries back and the
	// player can simply try again.
	if ip == "" || int(portF) <= 0 {
		return "", 0, "", fmt.Errorf("game manager returned no usable address (ip=%q port=%v)", ip, result["port"])
	}
	return ip, int(portF), instID, nil
}
