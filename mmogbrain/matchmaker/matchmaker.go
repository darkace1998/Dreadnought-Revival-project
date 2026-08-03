package matchmaker

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
	// seenInstance remembers which instance ids the control plane has ever
	// answered a 200 for. Without it a 404 cannot be read: it means "the host
	// is gone" on dn-dedicated and "there is no such route" on game-manager,
	// and acting on the second would end every live match.
	seenInstance map[string]bool
	ticker       *time.Ticker
	stop         chan struct{}
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

// pollBattleServers asks the control plane whether the battle server behind
// each young active match has finished loading, and stamps matches.server_ready_at
// the first time it says yes.
//
// This is what lets the YA_Connect travel push stop guessing. YA_Connect makes
// the client travel the instant it arrives, so pushing it early lands the player
// on a server that is not accepting connections; DN_CONNECT_PUSH_DELAY covered
// that by waiting long enough for the slowest launch, which is dead time on the
// "Battle server starting" screen for every launch faster than that.
//
// Readiness lives here, on the matchmaker's own goroutine, rather than in the
// connection handler: the handler runs per client frame and must not make
// blocking HTTP calls, and one poller for all matches is cheaper than one per
// connected player.
//
// A control plane that does not report readiness (game-manager has no per-
// instance route at all) simply never sets the stamp, and the push falls back to
// the delay. That fallback is why a failure here is logged at debug and not
// treated as an error.
func (m *Matchmaker) pollBattleServers() {
	if m.GameMgrURL == "" {
		return
	}
	cutoff := time.Now().UTC().Add(-MaxMatchLifetime).Format(time.RFC3339)
	rows, err := m.DB.Query(`
		SELECT id, instance_id, server_ready_at IS NOT NULL FROM matches
		WHERE status='active' AND instance_id != ''
		  AND datetime(created_at) >= datetime(?)`, cutoff)
	if err != nil {
		m.Log.WithError(err).Debug("query matches awaiting readiness")
		return
	}
	type pending struct {
		matchID, instanceID string
		alreadyReady        bool
	}
	var waiting []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.matchID, &p.instanceID, &p.alreadyReady); err != nil {
			break
		}
		waiting = append(waiting, p)
	}
	_ = rows.Close()

	for _, p := range waiting {
		state, err := m.instanceState(p.instanceID)
		if err != nil {
			m.Log.WithError(err).WithField("instance_id", p.instanceID).
				Debug("instance poll failed; YA_Connect will fall back to the fixed delay")
			continue
		}
		if state.gone && m.seenInstance[p.instanceID] {
			m.endMatchWithNoHost(p.matchID, p.instanceID)
			continue
		}
		if !state.known {
			continue
		}
		if m.seenInstance == nil {
			m.seenInstance = map[string]bool{}
		}
		m.seenInstance[p.instanceID] = true
		if !state.running {
			m.endMatchWithNoHost(p.matchID, p.instanceID)
			continue
		}
		if !state.ready || p.alreadyReady {
			continue
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := m.DB.Exec(
			`UPDATE matches SET server_ready_at=? WHERE id=? AND server_ready_at IS NULL`,
			now, p.matchID); err != nil {
			m.Log.WithError(err).WithField("match_id", p.matchID).Warn("record server readiness")
			continue
		}
		m.Log.WithFields(logrus.Fields{
			"match_id":    p.matchID,
			"instance_id": p.instanceID,
		}).Info("battle server reports ready; client may travel now")
	}
}

// endMatchWithNoHost ends a match whose battle server is no longer there.
//
// Nothing else does this in time. sweepStaleMatches only ends a match once it is
// older than MaxMatchLifetime (45 minutes), which is the right backstop and far
// too slow for the case that actually happens: the host dies, and every player
// in it stays "matched" -- unable to queue again, and now, since mmogbrain arms
// the travel push for a player who logs in mid-match, liable to be sent straight
// back at an address with nothing behind it.
func (m *Matchmaker) endMatchWithNoHost(matchID, instanceID string) {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := m.DB.Exec(`UPDATE matches SET status='ended', ended_at=? WHERE id=? AND status='active'`, now, matchID); err != nil {
		m.Log.WithError(err).WithField("match_id", matchID).Warn("end match with no host")
		return
	}
	if _, err := m.DB.Exec(`DELETE FROM match_slots WHERE match_id=?`, matchID); err != nil {
		m.Log.WithError(err).WithField("match_id", matchID).Warn("clear slots for match with no host")
		return
	}
	delete(m.seenInstance, instanceID)
	m.Log.WithFields(logrus.Fields{
		"match_id":    matchID,
		"instance_id": instanceID,
	}).Info("battle server is gone; ended the match and freed its players")
}

// battleServerState is what the control plane says about one instance.
//
// `known` separates "the control plane answered about this instance" from "it
// does not implement the route at all", which matters because game-manager has
// no per-instance route and 404s every id. `gone` is only actionable together
// with having seen a 200 for that same instance earlier -- see the caller.
type battleServerState struct {
	known   bool
	gone    bool
	ready   bool
	running bool
}

// instanceState reads one instance's status from the control plane.
//
// A 404 is ambiguous on its own: dn-dedicated deletes an instance from its map
// when the process exits, so 404 there means the host is gone -- but
// game-manager has no per-instance route, so 404 there means every id, forever.
// This reports the 404 and lets the caller disambiguate with what it has seen
// before. A body with no "ready" key means the control plane does not implement
// readiness, which is an error so the caller logs it at debug and keeps using
// the fixed-delay fallback.
func (m *Matchmaker) instanceState(instanceID string) (battleServerState, error) {
	req, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf("%s/instances/%s", m.GameMgrURL, url.PathEscape(instanceID)), nil)
	if err != nil {
		return battleServerState{}, err
	}
	req.Header.Set("X-Internal-Key", m.InternalKey)
	resp, err := gameManagerHTTPClient.Do(req)
	if err != nil {
		return battleServerState{}, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusNotFound {
		return battleServerState{gone: true}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return battleServerState{}, fmt.Errorf("instance status returned %d", resp.StatusCode)
	}
	var view map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		return battleServerState{}, fmt.Errorf("decode instance status: %w", err)
	}
	ready, ok := view["ready"].(bool)
	if !ok {
		return battleServerState{}, fmt.Errorf("instance status has no ready field")
	}
	// running predates nothing -- both control planes report it alongside
	// ready -- but treat its absence as "running" rather than as "dead",
	// because guessing "dead" would end live matches.
	running := true
	if value, present := view["running"].(bool); present {
		running = value
	}
	return battleServerState{known: true, ready: ready, running: running}, nil
}

func (m *Matchmaker) tick() error {
	if err := m.sweepStaleMatches(); err != nil {
		m.Log.WithError(err).Warn("stale match sweep failed")
	}
	m.pollBattleServers()

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

// runnableGameMode maps the mode a player queued for onto one their pawn can
// actually spawn in.
//
// Why this exists, from the client and battle server logs:
//
// A player joins, the client asks the server to spawn it with the loadout id of
// its active ship, and the server answers
//
//	ServerSpawnNearActor | Could not Set the active Loadout.
//	                       Given loadout ID does not exist in loadout manager!
//	AYGameMode::SpawnDefaultPawn: Active Loadout not found. Can't spawn
//
// The server's UYLoadoutManagerComponent is empty, and it cannot be anything
// else here. It populates itself from the LOCAL process's mmogbrain player data
// (UYLoadoutManager::InitializeFromPlayerData reads the YMmogbrain module
// singleton at +0x3898), and it only reaches the code that adds anything -- the
// player data path or the m_installerLoadoutList fallback -- once that data is
// valid. Our battle server has none: it makes zero contact with mmogbrain, even
// when handed -GatewayAddress/-YFirmamentAddress exactly as dn-launcher passes
// them to the client, because the backend connection is driven by the frontend
// login flow and a process booted straight into a map never runs it. There is
// also no RPC by which a client could send its loadout: the only Server*Loadout
// function in the whole binary is ServerPlayerClickedShipLoadout, an analytics
// call.
//
// That leaves exactly one source of a loadout on a backend-less server:
// AYGameMode::GetGameModeLoadout, which the game mode supplies itself. Only
// AYGameMode_TrainingMatch overrides it (m_trainingMatchLoadout, ->
// VH_DreadnoughtMedium_TutorialPlayer_PrecastLoadout_BP). In every other mode it
// is null, GetLoadoutForPlayer returns nothing, and the player stays a
// spectator.
//
// This USED TO redirect every queued mode to TM by default. That was a bad
// trade and it is off now.
//
// TM is the only mode whose host fails to set up player starts. Measured on a
// host with no client attached, same map, same binary, only the URL's game
// option differing:
//
//	mode on Highlands        sublevels loaded   "no orbit spawn locations set!"
//	TM                       12                 yes
//	TDM                      13                 no
//	BC                       13                 no
//	Onslaught (map default)  13                 no
//
// TM loads MP_Highlands_TM and does not load MP_Highlands_Light or
// MP_Highlands_Onslaught, and it is the one configuration where
// AYOrbitTransitionManager::ActivateBattlePlayerStarts finds nothing to
// activate. Downstream of that the player has no player start, drops to world
// origin -- under the terrain on Highlands -- and never gets the ship selection
// screen at all.
//
// So the two failures are mutually exclusive, and forcing TM chose the worse
// one:
//
//   - TM: the game mode supplies a loadout, so a pawn COULD spawn, but the
//     player never reaches ship selection.
//   - anything else: ship selection works, but the host's loadout manager is
//     empty, so the spawn is refused and the player stays a spectator.
//
// The second is what this server did before the redirect, and it is what the
// operator reported having: orbit, and a ship selection menu. Getting a pawn out
// of TM was speculative; losing the screen was not. Both are blocked on the same
// root -- a host with no player data -- and that is where the fix belongs.
//
// DN_FORCE_GAME_MODE is kept as an escape hatch: set it to a mode name to force
// that mode, or to "1" for DefaultSpawnableGameMode. Unset or empty honours what
// the player queued for.
func runnableGameMode(queued string) string {
	forced := strings.TrimSpace(os.Getenv("DN_FORCE_GAME_MODE"))
	switch strings.ToLower(forced) {
	case "":
		return substituteBrokenGameMode(queued)
	case "1", "on", "true", "yes":
		forced = DefaultSpawnableGameMode
	}
	if forced == "" || forced == queued {
		return queued
	}
	if !engineGameModeAliases[forced] {
		return queued
	}
	return forced
}

// engineGameModeAliases is what the engine can resolve from a map URL's
// "game=" option: the ShortName column of DefaultGame.ini's
// [/Script/Engine.GameMode] GameModeClassAliases table, which is cooked into the
// pak and live at runtime (a match requested as BC demonstrably ran
// GameInfo_BC_BP_C).
//
// Deliberately NOT ValidGameMode: that is the set of modes a CLIENT may queue
// for, which is a different list. TMBasic and Demo are resolvable by the engine
// but are not queue modes, and several queue aliases are not engine aliases.
var engineGameModeAliases = map[string]bool{
	"TDM": true, "PodTDM": true, "TE": true, "TM": true, "Onslaught": true,
	"Territory": true, "TER": true, "Benchmark": true, "VisualAttraction": true,
	"Tutorial": true, "Demo": true, "Bootcamp": true, "BC": true,
	"TMBasic": true, "TurboTDM": true,
}

// brokenHostGameModes maps a mode whose HOST does not work onto one that does.
//
// This is the opposite of the blanket redirect that used to live here, and it is
// narrow on purpose: exactly one mode is substituted, and only because it is
// measurably broken on a battle server.
//
// TM is what the front end's "Proving Grounds" button queues -- the ordinary
// button, not an exotic option -- and TM is the one mode of four whose host logs
//
//	AYOrbitTransitionManager::ActivateBattlePlayerStarts: no orbit spawn locations set!
//	GetObjectiveState - Id:Move to the battlezone not found!
//
// Measured on a host with no client attached, same map and binary, only the map
// URL's game option differing: TM yes, TDM no, BC no, the map's own default no.
// Reproduced independently on Windows by the client side (AGENT-CHAT C13.1), and
// the consequence measured there too (C14.1): under TM the player is left under
// the terrain with no ship selection, while the same client in TDM gets the
// "CHOOSE YOUR SHIP" screen, a working ready toggle and the orbit backdrop.
//
// So a player pressing the normal button lands in the one mode that cannot work.
// Substituting it is strictly better than what they get otherwise, and it is
// reversible: DN_KEEP_BROKEN_MODES=1 sends the queued mode through untouched.
//
// Remove this the moment TM's host works. It is a workaround for a defect, not a
// statement about what these modes are.
var brokenHostGameModes = map[string]string{
	"TM": "TDM",
}

// substituteBrokenGameMode swaps a mode that cannot work on a host for one that
// can. Anything not in the table is returned untouched.
func substituteBrokenGameMode(queued string) string {
	if os.Getenv("DN_KEEP_BROKEN_MODES") == "1" {
		return queued
	}
	if replacement, broken := brokenHostGameModes[queued]; broken {
		return replacement
	}
	return queued
}

// DefaultSpawnableGameMode is what DN_FORCE_GAME_MODE selects when it is set to
// "1" or "on" rather than to a mode name. It is NOT applied by default -- see
// runnableGameMode.
const DefaultSpawnableGameMode = "TM"

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

	// The mode the battle server is actually told to run, which is not always
	// the one the player queued for. See runnableGameMode.
	runMode := runnableGameMode(gameMode)
	if runMode != gameMode {
		m.Log.WithFields(logrus.Fields{
			"queued": gameMode,
			"run_as": runMode,
		}).Info("running the match in a spawnable game mode; the queued mode cannot spawn a pawn on a battle server with no backend")
	}
	gameMode = runMode

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
		`INSERT INTO matches(id,game_mode,map,server_ip,server_port,status,created_at,started_at,instance_id) VALUES(?,?,?,?,?,?,?,?,?)`,
		matchID, gameMode, mapName, serverIP, serverPort, "active", now, now, instanceID,
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
