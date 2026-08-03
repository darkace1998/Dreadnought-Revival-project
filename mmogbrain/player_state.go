package main

import (
	"database/sql"
	"errors"
	"fmt"
	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

var (
	mmogPlayerStateMu sync.RWMutex
	mmogPlayerStateDB *sql.DB
)

const defaultCaptainDisplayInfo = "GENDER_MALE;#iiS=872349903#iiH=872349830#iiHw=-1#iiEw=-1#iiMs=855572488#iiMt=855572501#iiMe=855572482#iiMn=855572504#bIam=0"

type mmogPlayerState struct {
	playerPID       string
	displayName     string
	displayInfo     string
	softCurrency    int32
	premiumCurrency int32
	freeXP          int32
	currentXP       int32
	currentRank     int32
	rankXP          int32
	fleets          []mmogFleetSeed
}

func setMmogPlayerStateDB(database *sql.DB) {
	mmogPlayerStateMu.Lock()
	defer mmogPlayerStateMu.Unlock()
	mmogPlayerStateDB = database
}

func currentMmogPlayerStateDB() *sql.DB {
	mmogPlayerStateMu.RLock()
	defer mmogPlayerStateMu.RUnlock()
	return mmogPlayerStateDB
}

func defaultMmogPlayerState(playerPID string) mmogPlayerState {
	return mmogPlayerState{
		playerPID:       normalizedPlayerStatePID(playerPID),
		displayName:     "Local",
		displayInfo:     defaultCaptainDisplayInfo,
		softCurrency:    10000,
		premiumCurrency: 0,
		freeXP:          0,
		// A brand-new player starts at rank 1 with 100 XP (verified against
		// the real game). rankXP tracks progress toward the next rank
		// (RankXPThreshold(2)=1000), so it starts equal to currentXP too —
		// they diverge once a player actually ranks up.
		currentXP:   100,
		currentRank: 1,
		rankXP:      100,
		fleets:      mmogFleetSeeds(),
	}
}

func normalizedPlayerStatePID(playerPID string) string {
	if normalized := protocol.NormalizePlayerPID(playerPID); normalized != "" {
		return normalized
	}
	return defaultMmogPlayerPID
}

// membershipExpiresAt returns the persisted elite-membership expiry (unix
// seconds), or 0 if the player has never purchased one / no DB is
// configured. 0 means "not active" — buildMmogPlayerDataPayload must not
// substitute a hardcoded always-active value for this.
func membershipExpiresAt(playerPID string) int32 {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return 0
	}
	pid := normalizedPlayerStatePID(playerPID)
	var expiresAt int32
	if err := database.QueryRow(`SELECT expires_at FROM player_membership WHERE user_id=?`, pid).Scan(&expiresAt); err != nil {
		return 0
	}
	return expiresAt
}

// extendMembershipTx adds durationDays to the player's current membership
// expiry (or to now, if the current expiry has already passed / never
// existed), persists it within the given transaction, and returns the new
// expiry. Callers must run this in the same transaction as any currency
// deduction for the purchase, so a failure here rolls back the deduction
// too instead of taking the player's currency for nothing.
func extendMembershipTx(tx *sql.Tx, playerPID string, durationDays int32, now int32) (int32, error) {
	pid := normalizedPlayerStatePID(playerPID)

	var currentExpiry int32
	_ = tx.QueryRow(`SELECT expires_at FROM player_membership WHERE user_id=?`, pid).Scan(&currentExpiry)

	base := now
	if currentExpiry > base {
		base = currentExpiry
	}
	newExpiry := base + durationDays*86400

	if _, err := tx.Exec(`INSERT INTO player_membership(user_id, expires_at, updated_at) VALUES(?,?,datetime('now'))
		ON CONFLICT(user_id) DO UPDATE SET expires_at=excluded.expires_at, updated_at=datetime('now')`, pid, newExpiry); err != nil {
		return 0, fmt.Errorf("extend membership: %w", err)
	}
	return newExpiry, nil
}

func normalizedCaptainDisplayInfo(displayInfo string) string {
	if trimmed := strings.TrimSpace(displayInfo); trimmed != "" {
		return trimmed
	}
	return defaultCaptainDisplayInfo
}

func mmogPlayerStateForPID(playerPID string) mmogPlayerState {
	state, err := loadMmogPlayerState(playerPID)
	if err != nil {
		return defaultMmogPlayerState(playerPID)
	}
	return state
}

func loadMmogPlayerState(playerPID string) (mmogPlayerState, error) {
	pid := normalizedPlayerStatePID(playerPID)
	database := currentMmogPlayerStateDB()
	if database == nil {
		return defaultMmogPlayerState(pid), nil
	}
	if err := seedMmogPlayerState(database, pid); err != nil {
		return mmogPlayerState{}, err
	}

	state := defaultMmogPlayerState(pid)
	if err := database.QueryRow(`SELECT display_name,display_info,soft_currency,premium_currency,free_xp,current_xp,current_rank,rank_xp FROM player_state WHERE user_id=?`, pid).
		Scan(&state.displayName, &state.displayInfo, &state.softCurrency, &state.premiumCurrency, &state.freeXP, &state.currentXP, &state.currentRank, &state.rankXP); err != nil {
		return mmogPlayerState{}, fmt.Errorf("load player state: %w", err)
	}
	state.displayInfo = normalizedCaptainDisplayInfo(state.displayInfo)

	loadouts, err := loadPersistedShipLoadouts(database, pid)
	if err != nil {
		return mmogPlayerState{}, err
	}
	fleets, err := loadPersistedFleets(database, pid, loadouts)
	if err != nil {
		return mmogPlayerState{}, err
	}
	if len(fleets) > 0 {
		state.fleets = fleets
	}
	return state, nil
}

func seedMmogPlayerState(database *sql.DB, playerPID string) error {
	pid := normalizedPlayerStatePID(playerPID)
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("begin seed player state: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// A brand-new player starts at rank 1 with 100 XP (verified against the
	// real game) — was previously seeded at 0/0, and inconsistent with
	// defaultMmogPlayerState's fallback values besides.
	if _, err := tx.Exec(`INSERT OR IGNORE INTO player_state(user_id,soft_currency,premium_currency,free_xp,current_xp,current_rank,rank_xp) VALUES(?,?,?,?,?,?,?)`,
		pid, 10000, 0, 0, 100, 1, 100); err != nil {
		return fmt.Errorf("seed player_state: %w", err)
	}

	for factionID := range knownFactionNames {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO player_faction_reputation(user_id,faction_id,reputation) VALUES(?,?,0)`, pid, factionID); err != nil {
			return fmt.Errorf("seed player_faction_reputation: %w", err)
		}
	}

	for _, fleet := range mmogFleetSeeds() {
		result, err := tx.Exec(`INSERT OR IGNORE INTO player_fleets(user_id,fleet_id,token,display_name,fleet_type,active,flagship_ship_id,flagship_loadout_id,flagship_loadout_index) VALUES(?,?,?,?,?,?,?,?,?)`,
			pid, fleet.fleetID, fleet.token, fleet.displayName, fleet.fleetType, boolToInt(fleet.active), fleet.flagshipShipID, fleet.flagshipLoadoutID, fleet.flagshipIndex())
		if err != nil {
			return fmt.Errorf("seed player_fleets: %w", err)
		}
		// Fleet MEMBERSHIP is seeded only when this fleet row did not already
		// exist. Seeding runs on every login, and re-inserting the starter
		// ships each time silently undid the player's own edits: a session that
		// removed all four starter ships kept only the one removed after the
		// last seed, and the other three reappeared. The loadouts themselves are
		// still seeded every time -- owning a ship is not the same as having it
		// in a fleet, and a removed ship must stay owned so it can be re-added.
		createdFleet := false
		if affected, err := result.RowsAffected(); err == nil && affected > 0 {
			createdFleet = true
		}
		for position, loadout := range fleet.shipLoadouts {
			if err := seedPersistedLoadout(tx, pid, loadout); err != nil {
				return err
			}
			if !createdFleet {
				continue
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO player_fleet_loadouts(user_id,fleet_id,position,loadout_id) VALUES(?,?,?,?)`,
				pid, fleet.fleetID, position, loadout.loadoutID()); err != nil {
				return fmt.Errorf("seed player_fleet_loadouts: %w", err)
			}
		}
	}
	if err := normalizePersistedStarterNativeLoadoutIDs(tx, pid); err != nil {
		return err
	}
	if err := normalizePersistedStarterShipIDs(tx, pid); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit seed player state: %w", err)
	}
	return nil
}

func normalizePersistedStarterNativeLoadoutIDs(exec sqlExecer, playerPID string) error {
	// Rewrites rows persisted with the old DEVELOPMENT class names
	// (Default__VH_AssaultMedium_T1_Loadout_BP_C and friends) to the shipping
	// precast ones, so existing players pick the correction up on next login
	// rather than keeping a development blueprint forever.
	for _, precastID := range dreadconfig.StarterInventoryLoadoutIDs() {
		nativeID, ok := nativeStarterLoadoutClassName(precastID)
		if !ok {
			continue
		}
		if _, err := exec.Exec(`UPDATE player_ship_loadouts SET native_loadout_id=?, updated_at=datetime('now') WHERE user_id=? AND precast_loadout_id=? AND native_loadout_id<>?`,
			nativeID, playerPID, precastID, nativeID); err != nil {
			return fmt.Errorf("normalize starter native loadout id %d: %w", precastID, err)
		}
	}
	return nil
}

func normalizePersistedStarterShipIDs(exec sqlExecer, playerPID string) error {
	for _, loadout := range starterShipLoadouts() {
		if _, err := exec.Exec(`UPDATE player_ship_loadouts SET ship_id=?, updated_at=datetime('now') WHERE user_id=? AND precast_loadout_id=? AND ship_id<>?`,
			loadout.ship.id, playerPID, loadout.precastLoadoutID, loadout.ship.id); err != nil {
			return fmt.Errorf("normalize starter ship id %d: %w", loadout.precastLoadoutID, err)
		}
		if _, err := exec.Exec(`UPDATE player_fleets SET flagship_ship_id=?, updated_at=datetime('now') WHERE user_id=? AND flagship_loadout_id=? AND flagship_ship_id<>?`,
			loadout.effectiveFleetShipID(), playerPID, loadout.precastLoadoutID, loadout.effectiveFleetShipID()); err != nil {
			return fmt.Errorf("normalize starter flagship ship id %d: %w", loadout.precastLoadoutID, err)
		}
	}
	return nil
}

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func seedPersistedLoadout(exec sqlExecer, playerPID string, loadout mmogShipLoadoutSeed) error {
	if _, err := exec.Exec(`INSERT OR IGNORE INTO player_ship_loadouts(
		user_id,loadout_id,native_loadout_id,precast_loadout_id,ship_id,loadout_index,loadout_name,position,active,
		weapon_primary_id,weapon_secondary_id,ability_primary_id,ability_secondary_id,ability_perimeter_id,ability_internal_id,
		perk_com_id,perk_weapon_id,perk_navigation_id,perk_engineer_id
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		playerPID, loadout.loadoutID(), loadout.entryID(), loadout.precastLoadoutID, loadout.ship.id, loadout.loadoutIndex, loadout.loadoutName, loadout.position, boolToInt(loadout.active),
		loadout.weaponPrimaryItemID(), loadout.weaponSecondaryItemID(), loadout.abilityItemID(0), loadout.abilityItemID(1), loadout.abilityItemID(2), loadout.abilityItemID(3),
		loadout.perkItemID(0), loadout.perkItemID(1), loadout.perkItemID(2), loadout.perkItemID(3)); err != nil {
		return fmt.Errorf("seed player_ship_loadouts: %w", err)
	}
	return nil
}

func loadPersistedShipLoadouts(database *sql.DB, playerPID string) (map[int32]mmogShipLoadoutSeed, error) {
	rows, err := database.Query(`SELECT loadout_id,native_loadout_id,precast_loadout_id,ship_id,loadout_index,loadout_name,position,active,
		weapon_primary_id,weapon_secondary_id,ability_primary_id,ability_secondary_id,ability_perimeter_id,ability_internal_id,
		perk_com_id,perk_weapon_id,perk_navigation_id,perk_engineer_id,display_info
		FROM player_ship_loadouts WHERE user_id=? ORDER BY position, loadout_id`, playerPID)
	if err != nil {
		return nil, fmt.Errorf("load player_ship_loadouts: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	loadouts := map[int32]mmogShipLoadoutSeed{}
	for rows.Next() {
		var loadout mmogShipLoadoutSeed
		var loadoutID int32
		var shipID int32
		var active int
		if err := rows.Scan(
			&loadoutID, &loadout.nativeLoadoutID, &loadout.precastLoadoutID, &shipID, &loadout.loadoutIndex, &loadout.loadoutName, &loadout.position, &active,
			&loadout.weaponPrimaryID, &loadout.weaponSecondaryID, &loadout.abilityIDs[0], &loadout.abilityIDs[1], &loadout.abilityIDs[2], &loadout.abilityIDs[3],
			&loadout.perkIDs[0], &loadout.perkIDs[1], &loadout.perkIDs[2], &loadout.perkIDs[3], &loadout.savedDisplayInfo,
		); err != nil {
			return nil, fmt.Errorf("scan player_ship_loadouts: %w", err)
		}
		loadout.playerLoadoutID = loadoutID
		loadout.ship = persistedShipByID(shipID)
		if starter, ok := starterLoadoutByPrecastID(loadout.precastLoadoutID); ok {
			loadout.fleetShipID = starter.fleetShipID
		} else if loadout.precastLoadoutID != 0 {
			// Same rule the starter fleet uses -- fleetStarterShipIDForPrecast is
			// the identity function, so a fleet's ship id IS its precast loadout
			// id. Without this branch only the four starter loadouts got it, and
			// any other owned ship (a Veteran/Legendary fleet, anything unlocked
			// later) fell back to the YPawn id instead, giving the client a
			// different id shape for those fleets than for the one that is known
			// to render correctly.
			loadout.fleetShipID = fleetStarterShipIDForPrecast(loadout.precastLoadoutID)
		}
		loadout.active = active != 0
		loadouts[loadout.loadoutID()] = loadout
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate player_ship_loadouts: %w", err)
	}
	return loadouts, nil
}

func loadPersistedFleets(database *sql.DB, playerPID string, loadouts map[int32]mmogShipLoadoutSeed) ([]mmogFleetSeed, error) {
	rows, err := database.Query(`SELECT fleet_id,token,display_name,fleet_type,active,flagship_ship_id,flagship_loadout_id,flagship_loadout_index FROM player_fleets WHERE user_id=? ORDER BY fleet_type`, playerPID)
	if err != nil {
		return nil, fmt.Errorf("load player_fleets: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var fleets []mmogFleetSeed
	for rows.Next() {
		var fleet mmogFleetSeed
		var active int
		if err := rows.Scan(&fleet.fleetID, &fleet.token, &fleet.displayName, &fleet.fleetType, &active, &fleet.flagshipShipID, &fleet.flagshipLoadoutID, &fleet.flagshipLoadoutIndex); err != nil {
			return nil, fmt.Errorf("scan player_fleets: %w", err)
		}
		fleet.active = active != 0
		if eligibility, ok := dreadFleetEligibilityByType(fleet.fleetType); ok {
			fleet.tiers = append([]int32(nil), eligibility.AllowedTiers...)
		}
		fleets = append(fleets, fleet)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate player_fleets: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close player_fleets: %w", err)
	}
	for idx := range fleets {
		fleets[idx].shipLoadouts, err = loadPersistedFleetLoadouts(database, playerPID, fleets[idx].fleetID, loadouts)
		if err != nil {
			return nil, err
		}
	}
	return fleets, nil
}

func loadPersistedFleetLoadouts(database *sql.DB, playerPID string, fleetID int32, loadouts map[int32]mmogShipLoadoutSeed) ([]mmogShipLoadoutSeed, error) {
	rows, err := database.Query(`SELECT loadout_id FROM player_fleet_loadouts WHERE user_id=? AND fleet_id=? ORDER BY position`, playerPID, fleetID)
	if err != nil {
		return nil, fmt.Errorf("load player_fleet_loadouts: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var fleetLoadouts []mmogShipLoadoutSeed
	for rows.Next() {
		var loadoutID int32
		if err := rows.Scan(&loadoutID); err != nil {
			return nil, fmt.Errorf("scan player_fleet_loadouts: %w", err)
		}
		if loadout, ok := loadouts[loadoutID]; ok {
			loadout.position = int32(len(fleetLoadouts))
			fleetLoadouts = append(fleetLoadouts, loadout)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate player_fleet_loadouts: %w", err)
	}
	return fleetLoadouts, nil
}

func dreadFleetEligibilityByType(fleetType int32) (struct {
	AllowedTiers []int32
}, bool) {
	for _, eligibility := range configBackedFleetEligibilities() {
		if eligibility.FleetType == fleetType {
			return struct {
				AllowedTiers []int32
			}{AllowedTiers: eligibility.AllowedTiers}, true
		}
	}
	return struct {
		AllowedTiers []int32
	}{}, false
}

func persistedShipByID(shipID int32) mmogShipSeed {
	for _, ship := range allT1Ships() {
		if ship.id == shipID {
			return ship
		}
	}
	if ship, ok := runtimeStarterShipForInstallerShipID(shipID); ok {
		return ship
	}
	return mmogShipSeed{id: shipID, name: fmt.Sprintf("Ship %d", shipID), owned: true, nodeID: shipID}
}

func (state mmogPlayerState) activeFleet() mmogFleetSeed {
	for _, fleet := range state.fleets {
		if fleet.active {
			return fleet
		}
	}
	if len(state.fleets) > 0 {
		return state.fleets[0]
	}
	return starterFleetState()
}

func (state mmogPlayerState) activeFleets() []mmogFleetSeed {
	active := make([]mmogFleetSeed, 0, len(state.fleets))
	for _, fleet := range state.fleets {
		if fleet.active {
			active = append(active, fleet)
		}
	}
	if len(active) == 0 && len(state.fleets) > 0 {
		active = append(active, state.fleets[0])
	}
	return active
}

func (state mmogPlayerState) shipLoadouts() []mmogShipLoadoutSeed {
	seen := map[int32]struct{}{}
	var loadouts []mmogShipLoadoutSeed
	for _, fleet := range state.fleets {
		for _, loadout := range fleet.shipLoadouts {
			if _, ok := seen[loadout.loadoutID()]; ok {
				continue
			}
			seen[loadout.loadoutID()] = struct{}{}
			loadouts = append(loadouts, loadout)
		}
	}
	if len(loadouts) == 0 {
		return starterShipLoadouts()
	}
	return loadouts
}

func persistedMmogPlayerPurchaseItemIDs(playerPID string) []int32 {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return nil
	}
	pid := normalizedPlayerStatePID(playerPID)
	if err := seedMmogPlayerState(database, pid); err != nil {
		return nil
	}

	rows, err := database.Query(`SELECT item_id FROM player_purchases WHERE user_id=? ORDER BY purchased_at,item_id`, pid)
	if err != nil {
		return nil
	}
	defer func() {
		_ = rows.Close()
	}()

	var ids []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil
	}
	return ids
}

func persistedMmogPlayerPurchasedItemIDSet(playerPID string) map[int32]struct{} {
	ids := persistedMmogPlayerPurchaseItemIDs(playerPID)
	if len(ids) == 0 {
		return nil
	}
	owned := make(map[int32]struct{}, len(ids))
	for _, id := range ids {
		owned[id] = struct{}{}
	}
	return owned
}

func playerOwnedTechTreeShips(playerPID string) []mmogShipSeed {
	ships := techTreeShips()
	purchased := persistedMmogPlayerPurchasedItemIDSet(playerPID)
	if len(purchased) > 0 {
		for idx := range ships {
			if _, ok := purchased[ships[idx].id]; ok {
				ships[idx].owned = true
			}
		}
	}
	ships = appendPersistedLoadoutShipRows(playerPID, ships)
	return appendPurchasedLoadoutShipRows(purchased, ships)
}

// appendPurchasedLoadoutShipRows gives an unlocked ship a row of its own.
//
// A tech tree unlock names the PRECAST LOADOUT (category 1/3), and the ship
// list is keyed by those same ids -- but techTreeShips() only contains T1/T2
// pawns plus starter aliases, so an unlocked T2/T3 ship had no row for the
// purchased id to mark owned. Live evidence: three unlocks were charged and
// recorded (33489267, 33489277, 33489281) while the ships stayed invisible,
// which is indistinguishable from "the unlock did nothing".
//
// The pawn behind the loadout comes from ShipIDForPrecastLoadout, so the row
// carries the real ship's identity rather than an invented one.
func appendPurchasedLoadoutShipRows(purchased map[int32]struct{}, ships []mmogShipSeed) []mmogShipSeed {
	if len(purchased) == 0 {
		return ships
	}
	seen := make(map[int32]struct{}, len(ships))
	for _, ship := range ships {
		seen[ship.id] = struct{}{}
	}
	for itemID := range purchased {
		if _, ok := seen[itemID]; ok {
			continue
		}
		switch category := (itemID >> 24) & 0xff; category {
		case mmogItemCategoryShipLoadoutPrecast, mmogItemCategoryShipLoadoutHero:
		default:
			continue
		}
		shipID, ok := dreadconfig.ShipIDForPrecastLoadout(itemID)
		if !ok {
			continue
		}
		row, ok := shipSeedForPawn(ships, shipID)
		if !ok {
			continue
		}
		seen[itemID] = struct{}{}
		row.id = itemID
		row.nodeID = itemID
		row.owned = true
		ships = append(ships, row)
	}
	return ships
}

// shipSeedForPawn builds a ship row for any pawn, preferring an existing row
// and otherwise deriving one from the pawn's asset path.
//
// Copying an existing row is not enough on its own: the built-in list only
// holds T1/T2 pawns, so a T3+ unlock found no row to copy and was dropped.
// Observed live -- of four unlocks, only the T2 one (33489267, Dover) appeared;
// the two T3 ones (33489277, 33489281) were charged and stayed invisible.
func shipSeedForPawn(ships []mmogShipSeed, shipID int32) (mmogShipSeed, bool) {
	for _, ship := range ships {
		if ship.id == shipID {
			return ship, true
		}
	}
	return deriveShipSeedFromAssetPath(shipID)
}

// shipClassIDsByClassName maps the class segment of a pawn's asset path to the
// classID/shipClass pair the client expects.
//
// Read off the built-in T1/T2 rows, where the pair depends on the CLASS only
// and never on the size: Assault 14/4 (Agosta, Trafalgar), Dreadnought 6/0
// (Simargl, Nav), Sniper 10/2 (Rurik, Tugarin, and Furia which is Light),
// Support 12/3 (Cerberus, Orcus), Scout 2/1 (Dover, Light). TestDerivedShipSeed
// MatchesTheBuiltInRows re-checks this against those rows so a wrong pair
// cannot be introduced silently.
var shipClassIDsByClassName = map[string]struct{ classID, shipClass int32 }{
	"Assault":     {14, 4},
	"Dreadnought": {6, 0},
	"Scout":       {2, 1},
	"Sniper":      {10, 2},
	"Support":     {12, 3},
}

// shipWeightBySizeName is the size index the same rows carry: Light 0
// (Furia, Dover), Medium 1 (everything else in the built-in list). Heavy
// follows the sequence but has no built-in row to confirm it, so it is marked.
var shipWeightBySizeName = map[string]int32{
	"Light":  0,
	"Medium": 1,
	"Heavy":  2, // GUESS: no built-in T1/T2 Heavy row to check against.
}

var shipPawnAssetPathPattern = regexp.MustCompile(`^/Game/Generic/Ships/([A-Za-z]+)/([A-Za-z]+)/T\d/`)

func deriveShipSeedFromAssetPath(shipID int32) (mmogShipSeed, bool) {
	item, ok := dreadconfig.ItemByID(shipID)
	if !ok || item.AssetPath == "" {
		return mmogShipSeed{}, false
	}
	match := shipPawnAssetPathPattern.FindStringSubmatch(item.AssetPath)
	if match == nil {
		return mmogShipSeed{}, false
	}
	class, ok := shipClassIDsByClassName[match[1]]
	if !ok {
		return mmogShipSeed{}, false
	}
	weight, ok := shipWeightBySizeName[match[2]]
	if !ok {
		return mmogShipSeed{}, false
	}
	// The ship's real name comes from the authoritative table, not from
	// ItemMetadata.DisplayName -- the latter is synthesised from the asset path
	// and yields things like "Scout Heavy T3 Vh Scouth Pawn T3" where the game
	// calls the ship by a proper name.
	name := item.DisplayName
	if authoritative, ok := dreadconfig.AuthoritativeShipName(shipID); ok && authoritative != "" {
		name = authoritative
	}
	return mmogShipSeed{
		id:           shipID,
		name:         name,
		classID:      class.classID,
		shipClass:    class.shipClass,
		weight:       weight,
		nodeID:       shipID,
		manufacturer: shipManufacturerForClassID(class.classID, ""),
	}, true
}

// appendPersistedLoadoutShipRows gives every loadout the player actually owns a
// tech-tree row, marked owned.
//
// techTreeShips() synthesises these "fleet alias" rows only for the four
// STARTER loadouts, because until now no account had anything else. The client
// resolves a fleet entry by looking its loadout id up IN the tech tree
// (YUIHangarFleetData::Load), and the owned-ship overview is built from the
// same rows -- so a loadout with no row is a ship the client cannot place and
// will not list. Observed directly: with T3 and T5 ships in the Veteran and
// Legendary fleets, the owned-ship overview came up empty and the only ships
// offered for adding were ones just removed from a fleet (those still had
// starter rows).
//
// Ownership here means "this player has the loadout persisted", which is
// exactly what player_ship_loadouts records; purchases stay an additional
// source rather than the only one.
func appendPersistedLoadoutShipRows(playerPID string, ships []mmogShipSeed) []mmogShipSeed {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return ships
	}
	loadouts, err := loadPersistedShipLoadouts(database, normalizedPlayerStatePID(playerPID))
	if err != nil || len(loadouts) == 0 {
		return ships
	}
	seen := make(map[int32]struct{}, len(ships))
	for _, ship := range ships {
		seen[ship.id] = struct{}{}
	}
	for _, loadout := range loadouts {
		for _, id := range []int32{loadout.effectiveFleetShipID(), loadout.loadoutID()} {
			if id == 0 {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ships = append(ships, mmogShipSeed{
				id: id, name: syntheticRowName(id, loadout),
				classID: loadout.ship.classID, shipClass: loadout.ship.shipClass,
				weight: loadout.ship.weight, owned: true, nodeID: id, nodeType: 0,
				manufacturer: shipManufacturerForClassID(loadout.ship.classID, loadout.ship.manufacturer),
			})
		}
	}
	return ships
}

func persistMmogPlayerMutation(playerPID string, requestName string, payload []byte) error {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return nil
	}
	pid := normalizedPlayerStatePID(playerPID)
	if err := seedMmogPlayerState(database, pid); err != nil {
		return err
	}
	switch requestName {
	case "YA_SavePlayerDisplayInformation":
		return persistSavePlayerDisplayInformation(database, pid, payload)
	case "YA_IncrementPlayerStatsCounter":
		return persistIncrementPlayerStatsCounter(database, pid, payload)
	case "YA_UpdateShipLoadout":
		return persistUpdateShipLoadout(database, pid, payload)
	case "YA_RenameShipLoadout":
		return persistRenameShipLoadout(database, pid, payload)
	case "YA_SetFleetFlagship":
		return persistSetFleetFlagship(database, pid, payload)
	case "YA_AddShipDefaultLoadouts":
		return persistAddShipDefaultLoadouts(database, pid, payload)
	case "YA_UnlockItem":
		return persistUnlockItem(database, pid, payload)
	case "YA_AddToFleet":
		return persistAddToFleet(database, pid, payload)
	case "YA_RemoveFromFleet":
		return persistRemoveFromFleet(database, pid, payload)
	case "YA_ChargeFleet", "YA_RepairFleet":
		return nil
	case "YA_SaveGame":
		return persistPlayerSaveBlob(database, pid, playerSaveSlotOnboarding, payload)
	case "YA_SaveCtAData":
		return persistPlayerSaveBlob(database, pid, playerSaveSlotCtA, payload)
	default:
		return nil
	}
}

// The client's own save blobs. It uploads them with YA_SaveGame /
// YA_SaveCtAData and reads them back from these YA_PlayerGet fields, so the
// slot names are deliberately the wire field names they are echoed into.
const (
	playerSaveSlotOnboarding = "SGD"
	playerSaveSlotCtA        = "SCtA"
)

// persistPlayerSaveBlob stores the opaque blob from a client save request.
//
// Both requests carry it in a byte-array field called "data". The contents are
// an int32 uncompressed size followed by zlib data, wrapping a magic tag and
// UE4 tagged-property serialisation of a UObject ("ONBS" +
// UYOnboardingSavedData for YA_SaveGame, "DAtC" + UYCtASaveData for
// YA_SaveCtAData). The server has no reason to understand any of that: it is
// client-owned state that only has to survive a round trip, so it is stored
// verbatim.
//
// This is what makes onboarding progress stick. UYOnboardingManager::LoadStates
// restores each rule's last-fired timestamp from the SGD blob, and the login
// gate's tutorial check is exactly "the Ob_TutorialFinished rule has a non-zero
// timestamp". Without persistence the client re-runs onboarding from scratch on
// every login and can never satisfy that check.
func persistPlayerSaveBlob(database *sql.DB, playerPID string, slot string, payload []byte) error {
	blob, ok := protocol.ExtractBytesField(payload, "data")
	if !ok {
		// Nothing to store. Deliberately not an error: dropping the previous
		// blob because one request arrived malformed would lose real progress.
		return nil
	}
	if _, err := database.Exec(
		`INSERT INTO player_save_blobs(user_id, slot, data, updated_at)
		 VALUES(?, ?, ?, datetime('now'))
		 ON CONFLICT(user_id, slot) DO UPDATE SET data=excluded.data, updated_at=excluded.updated_at`,
		playerPID, slot, blob,
	); err != nil {
		return fmt.Errorf("persist %s save blob: %w", slot, err)
	}
	return nil
}

// loadPlayerSaveBlob returns the stored blob for a slot, or nil when the player
// has never saved one (a brand-new account, which must still go through
// onboarding).
func loadPlayerSaveBlob(playerPID string, slot string) []byte {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return nil
	}
	var blob []byte
	err := database.QueryRow(
		`SELECT data FROM player_save_blobs WHERE user_id=? AND slot=?`,
		normalizedPlayerStatePID(playerPID), slot,
	).Scan(&blob)
	if err != nil {
		return nil
	}
	return blob
}

func persistSavePlayerDisplayInformation(database *sql.DB, playerPID string, payload []byte) error {
	// The client names this field "disp", not "DisplayInfo" -- confirmed from a
	// captured request:
	//
	//   RT "YA_SavePlayerDisplayInformation"  PID <guid>
	//   disp "GENDER_FEMALE;#iiS=872349703#iiH=872349769#...#bIam=0"
	//
	// Reading the wrong name meant every captain the player customised was
	// silently discarded and the account reverted to the default appearance.
	// "DisplayInfo" is kept as a fallback: it is the name this server uses when
	// it hands the same string back in other payloads.
	displayInfo := firstMmogStringField(payload, "disp", "DisplayInfo")
	displayName := firstMmogStringField(payload, "DisplayName", "displayName")

	if displayInfo == "" && strings.TrimSpace(displayName) == "" {
		return nil
	}

	if displayName == "" {
		if err := database.QueryRow(`SELECT display_name FROM player_state WHERE user_id=?`, playerPID).Scan(&displayName); err != nil {
			return fmt.Errorf("load existing display name: %w", err)
		}
	}
	displayInfo = normalizedCaptainDisplayInfo(displayInfo)

	if _, err := database.Exec(`UPDATE player_state SET display_name=?, display_info=?, updated_at=datetime('now') WHERE user_id=?`,
		displayName, displayInfo, playerPID); err != nil {
		return fmt.Errorf("save player display information: %w", err)
	}
	return nil
}

// persistUpdateShipLoadout stores a loadout the player edited in the hangar.
//
// Every field name here comes from a captured request, because none of the ones
// this function used to look for exist. The client sends (tags in brackets):
//
//	RT [str] "YA_UpdateShipLoadout"   ID [guid]   PID [guid]
//	ShipID [i32] 33489262             Name [str] "Agosta"
//	DisplayInfo [str] "335872027#335872028#335872026#335872025;352649226;..."
//	WeaponPrimary [i32]  WeaponSecondary [i32]
//	AbilityPrimary [i32] AbilitySecondary [i32] AbilityPerimeter [i32] AbilityInternal [i32]
//	PerkCom [i32] PerkWeapon [i32] PerkNavigation [i32] PerkEngineer [i32]
//	LoadoutSlotNum [i32]
//
// Two things were wrong, and together they made this function a complete no-op:
//
//   - The row was looked up by "LoadoutID"/"loadoutID"/"precastLoadout"/
//     "precastLoadoutID". The client calls it **ShipID**, and its value is the
//     precast loadout id (33489262 for the Agosta), not the pawn id. So the
//     lookup returned 0 and the function returned at the first statement, every
//     time.
//   - The item fields were spelled "weaponPrimary", "abilityPerimeter" and so
//     on. The client capitalises them, and protocol.ExtractInt32Field compares
//     names with ==, not case-insensitively. Even reached, none would have
//     matched.
//
// So no weapon, module, perk or appearance change a player made was ever
// stored. The old names are kept as fallbacks: they cost nothing and this
// server has answered to itself with them in tests.
func persistUpdateShipLoadout(database *sql.DB, playerPID string, payload []byte) error {
	loadoutID := firstMmogInt32Field(payload, "ShipID", "LoadoutID", "loadoutID", "precastLoadout", "precastLoadoutID")
	if loadoutID == 0 {
		return nil
	}
	assignments := []struct {
		column string
		fields []string
	}{
		{column: "weapon_primary_id", fields: []string{"WeaponPrimary", "weaponPrimary", "weaponPrimaryID"}},
		{column: "weapon_secondary_id", fields: []string{"WeaponSecondary", "weaponSecondary", "weaponSecondaryID"}},
		{column: "ability_primary_id", fields: []string{"AbilityPrimary", "abilityPrimary"}},
		{column: "ability_secondary_id", fields: []string{"AbilitySecondary", "abilitySecondary"}},
		{column: "ability_perimeter_id", fields: []string{"AbilityPerimeter", "abilityPerimeter"}},
		{column: "ability_internal_id", fields: []string{"AbilityInternal", "abilityInternal"}},
		{column: "perk_com_id", fields: []string{"PerkCom", "perkCom"}},
		{column: "perk_weapon_id", fields: []string{"PerkWeapon", "perkWeapon"}},
		{column: "perk_navigation_id", fields: []string{"PerkNavigation", "perkNavigation"}},
		{column: "perk_engineer_id", fields: []string{"PerkEngineer", "perkEngineer"}},
	}
	for _, assignment := range assignments {
		value := firstMmogInt32Field(payload, assignment.fields...)
		if value == 0 {
			// Zero is "not sent" here, not "cleared": the client sends 0 for the
			// perk slots of a tier-1 hull, which legitimately has none.
			continue
		}
		if _, err := database.Exec("UPDATE player_ship_loadouts SET "+assignment.column+"=?, updated_at=datetime('now') WHERE user_id=? AND loadout_id=?", value, playerPID, loadoutID); err != nil {
			return fmt.Errorf("update loadout %s: %w", assignment.column, err)
		}
	}

	// The ship's appearance. Stored verbatim -- the server has no reason to
	// understand it, it only has to survive the round trip so the ship the
	// player built is the ship they get back. Format and consumers are in
	// shared/dreadgameconfig/ship_vanity.go.
	if displayInfo := strings.TrimSpace(firstMmogStringField(payload, "DisplayInfo", "displayInfo", "m_displayInfo")); displayInfo != "" {
		if _, err := database.Exec(`UPDATE player_ship_loadouts SET display_info=?, updated_at=datetime('now') WHERE user_id=? AND loadout_id=?`,
			displayInfo, playerPID, loadoutID); err != nil {
			return fmt.Errorf("update loadout display_info: %w", err)
		}
	}

	// The client also renames through this request, not only through
	// YA_RenameShipLoadout.
	if name := strings.TrimSpace(firstMmogStringField(payload, "Name", "name")); name != "" {
		if _, err := database.Exec(`UPDATE player_ship_loadouts SET loadout_name=?, updated_at=datetime('now') WHERE user_id=? AND loadout_id=?`,
			name, playerPID, loadoutID); err != nil {
			return fmt.Errorf("update loadout name: %w", err)
		}
	}
	return nil
}

func persistRenameShipLoadout(database *sql.DB, playerPID string, payload []byte) error {
	loadoutID := firstMmogInt32Field(payload, "LoadoutID", "loadoutID", "precastLoadout")
	name := firstMmogStringField(payload, "Name", "name", "LoadoutName", "loadoutName")
	if loadoutID == 0 || strings.TrimSpace(name) == "" {
		return nil
	}
	if _, err := database.Exec(`UPDATE player_ship_loadouts SET loadout_name=?, updated_at=datetime('now') WHERE user_id=? AND loadout_id=?`, name, playerPID, loadoutID); err != nil {
		return fmt.Errorf("rename loadout: %w", err)
	}
	return nil
}

func persistSetFleetFlagship(database *sql.DB, playerPID string, payload []byte) error {
	fleetID := firstMmogInt32Field(payload, "fleet id", "FleetType", "m_fleetId")
	loadoutID := firstMmogInt32Field(payload, "FlagShipLoadoutID", "flagshipLoadoutID", "LoadoutID", "loadoutID")
	shipID := firstMmogInt32Field(payload, "FlagShipID", "shipID", "shipId", "ShipID")
	if fleetID == 0 {
		fleetID = starterFleetState().fleetID
	}
	if loadoutID == 0 && shipID != 0 {
		if err := database.QueryRow(`SELECT loadout_id FROM player_ship_loadouts WHERE user_id=? AND ship_id=? ORDER BY position LIMIT 1`, playerPID, shipID).Scan(&loadoutID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lookup loadout for set fleet flagship: %w", err)
		}
	}
	if loadoutID == 0 {
		return nil
	}
	var position int32
	if err := database.QueryRow(`SELECT COALESCE(MIN(position),0) FROM player_fleet_loadouts WHERE user_id=? AND fleet_id=? AND loadout_id=?`, playerPID, fleetID, loadoutID).Scan(&position); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("lookup flagship index: %w", err)
	}
	if shipID == 0 {
		if err := database.QueryRow(`SELECT ship_id FROM player_ship_loadouts WHERE user_id=? AND loadout_id=?`, playerPID, loadoutID).Scan(&shipID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lookup ship for set fleet flagship: %w", err)
		}
	}
	if _, err := database.Exec(`UPDATE player_fleets SET active=0, updated_at=datetime('now') WHERE user_id=?`, playerPID); err != nil {
		return fmt.Errorf("clear active fleets: %w", err)
	}
	if _, err := database.Exec(`UPDATE player_fleets SET active=1, flagship_ship_id=?, flagship_loadout_id=?, flagship_loadout_index=?, updated_at=datetime('now') WHERE user_id=? AND fleet_id=?`,
		shipID, loadoutID, position, playerPID, fleetID); err != nil {
		return fmt.Errorf("set fleet flagship: %w", err)
	}
	return nil
}

func persistAddShipDefaultLoadouts(database *sql.DB, playerPID string, payload []byte) error {
	shipID := firstMmogInt32Field(payload, "ShipID", "shipID", "shipId")
	if shipID == 0 {
		return nil
	}
	for _, loadout := range starterShipLoadouts() {
		if loadout.ship.id != shipID {
			continue
		}
		return seedPersistedLoadout(database, playerPID, loadout)
	}
	return nil
}

// Item categories, from the top byte of an id ((id >> 24) & 0xff) -- the same
// tag ItemIDTable assigns and the client's own gates compare (see
// tech_tree_precast.go for the decompile evidence).
const (
	mmogItemCategoryShipLoadoutPrecast = 1
	mmogItemCategoryShipLoadoutHero    = 3
	mmogItemCategoryShipPawn           = 10
)

// fleetEditTargetFleetID picks the fleet a YA_AddToFleet/YA_RemoveFromFleet
// applies to.
//
// The client does NOT send our numeric fleet id back. Captured live, the
// request carries `fleet` as a 16-byte GUID (wire tag 0x02) holding the
// player's own PID -- it identifies the owner, not which of their three fleets
// -- plus `shipId`. So the only sane target is the fleet the player currently
// has active, falling back to the starter fleet if none is marked.
func fleetEditTargetFleetID(database *sql.DB, playerPID string, payload []byte) int32 {
	if fleetID := firstMmogInt32Field(payload, "fleet id", "FleetType", "m_fleetId"); fleetID != 0 {
		return fleetID
	}
	var fleetID int32
	if err := database.QueryRow(`SELECT fleet_id FROM player_fleets WHERE user_id=? AND active=1 LIMIT 1`,
		playerPID).Scan(&fleetID); err == nil && fleetID != 0 {
		return fleetID
	}
	return starterFleetState().fleetID
}

// fleetEditLoadoutID resolves which of the player's loadouts a fleet edit is
// about.
//
// The `shipId` the client sends is NOT a ship id. Captured live, a removal of
// Agosta carried shipId=33489262 -- the PRECAST LOADOUT id. By the category law
// its top byte is 1 (YShipLoadoutPrecast); a real ship id has top byte 10
// (YPawn). Looking it up in ship_id therefore matched nothing and both handlers
// silently returned nil, which is why fleet edits never took: twenty
// YA_RemoveFromFleet requests in one session, every one a no-op.
//
// Resolve by category rather than by guessing a column: a precast/hero id is
// matched against loadout_id and precast_loadout_id, a YPawn id against ship_id.
func fleetEditLoadoutID(database *sql.DB, playerPID string, payload []byte) (int32, error) {
	if loadoutID := firstMmogInt32Field(payload, "LoadoutID", "loadoutID", "FlagShipLoadoutID"); loadoutID != 0 {
		return loadoutID, nil
	}
	candidate := firstMmogInt32Field(payload, "ShipID", "shipID", "shipId")
	if candidate == 0 {
		return 0, nil
	}
	var loadoutID int32
	var err error
	switch category := (candidate >> 24) & 0xff; category {
	case mmogItemCategoryShipLoadoutPrecast, mmogItemCategoryShipLoadoutHero:
		err = database.QueryRow(`SELECT loadout_id FROM player_ship_loadouts
			WHERE user_id=? AND (loadout_id=? OR precast_loadout_id=?) ORDER BY position LIMIT 1`,
			playerPID, candidate, candidate).Scan(&loadoutID)
	default:
		err = database.QueryRow(`SELECT loadout_id FROM player_ship_loadouts
			WHERE user_id=? AND ship_id=? ORDER BY position LIMIT 1`, playerPID, candidate).Scan(&loadoutID)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("resolve fleet edit loadout %d: %w", candidate, err)
	}
	return loadoutID, nil
}

// persistUnlockItem records a tech-tree unlock and charges what the client says
// it is spending.
//
// YA_UnlockItem was previously answered with a bare success and nothing else,
// so a tech tree unlock changed no state at all -- the ship never became owned
// and the player could unlock it again forever. Captured request (2026-08-02):
//
//	RT     STRING "YA_UnlockItem"
//	ItemID tag 0x76 (8-byte int) = 33489267   (the T2 Scout Light precast)
//	ShipXp tag 0x66 (4-byte int) = 0
//	FreeXp tag 0x66 (4-byte int) = 5000
//
// The client sends the price it already showed the player, so the costs are
// taken from the request rather than recomputed from a tech tree the server
// would have to model independently. They are still clamped at zero so a
// malformed request cannot credit anybody.
func persistUnlockItem(database *sql.DB, playerPID string, payload []byte) error {
	itemID := firstMmogInt32Field(payload, "ItemID", "itemID", "itemId")
	if itemID == 0 {
		return nil
	}
	// ShipXp is in the request too, but it is not charged here: the request
	// names the ITEM, not which ship's pool the XP comes from, and guessing
	// would take currency from the wrong ship. Free XP is unambiguous.
	freeXP := firstMmogInt32Field(payload, "FreeXp", "freeXp", "FreeXP")
	if freeXP < 0 {
		freeXP = 0
	}

	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("unlock item %d: %w", itemID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Already owned? Charge nothing. The client re-sends YA_UnlockItem for an
	// item it does not believe it owns, and it does not yet believe it owns
	// these -- one session sent six unlock requests while the purchase count
	// stayed at four, each repeat silently taking another 5,000 free XP for an
	// item the player already had. INSERT OR IGNORE swallowed the duplicate row
	// but the charge above it had already happened.
	var alreadyOwned int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM player_purchases WHERE user_id=? AND item_id=?`,
		playerPID, itemID).Scan(&alreadyOwned); err != nil {
		return fmt.Errorf("check ownership of %d: %w", itemID, err)
	}
	if alreadyOwned > 0 {
		return nil
	}

	if freeXP > 0 {
		result, err := tx.Exec(`UPDATE player_state SET free_xp=free_xp-?, updated_at=datetime('now')
			WHERE user_id=? AND free_xp>=?`, freeXP, playerPID, freeXP)
		if err != nil {
			return fmt.Errorf("charge free xp for %d: %w", itemID, err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			// Not enough free XP. Record nothing: leaving the unlock unrecorded
			// is what keeps the client's view and ours in step.
			return nil
		}
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO player_purchases(user_id,item_id,item_type,price_paid,currency)
		VALUES(?,?,?,?,?)`, playerPID, itemID, purchasedItemType(itemID), freeXP, "freexp"); err != nil {
		return fmt.Errorf("record unlock %d: %w", itemID, err)
	}
	if err := grantUnlockedShipLoadout(tx, playerPID, itemID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit unlock %d: %w", itemID, err)
	}
	committed = true
	return nil
}

func persistAddToFleet(database *sql.DB, playerPID string, payload []byte) error {
	fleetID := fleetEditTargetFleetID(database, playerPID, payload)
	loadoutID, err := fleetEditLoadoutID(database, playerPID, payload)
	if err != nil {
		return err
	}
	if loadoutID == 0 {
		return nil
	}
	var position int32
	if err := database.QueryRow(`SELECT COALESCE(MAX(position)+1,0) FROM player_fleet_loadouts WHERE user_id=? AND fleet_id=?`, playerPID, fleetID).Scan(&position); err != nil {
		return fmt.Errorf("next fleet position: %w", err)
	}
	if _, err := database.Exec(`INSERT OR IGNORE INTO player_fleet_loadouts(user_id,fleet_id,position,loadout_id) VALUES(?,?,?,?)`, playerPID, fleetID, position, loadoutID); err != nil {
		return fmt.Errorf("add to fleet: %w", err)
	}
	return nil
}

func persistRemoveFromFleet(database *sql.DB, playerPID string, payload []byte) error {
	fleetID := fleetEditTargetFleetID(database, playerPID, payload)
	loadoutID, err := fleetEditLoadoutID(database, playerPID, payload)
	if err != nil {
		return err
	}
	if loadoutID == 0 {
		return nil
	}
	if _, err := database.Exec(`DELETE FROM player_fleet_loadouts WHERE user_id=? AND fleet_id=? AND loadout_id=?`, playerPID, fleetID, loadoutID); err != nil {
		return fmt.Errorf("remove from fleet: %w", err)
	}
	return nil
}

// firstMmogInt32Field reads a request field as int32, tolerating both the
// int32 wire tag (0x56) and a numeric-string tag (0x09) for the same field
// name. Response-side payloads in this codebase were confirmed (decompile
// evidence) to need numeric-string encoding for several array-entry fields
// the client's restrictive scalar parser otherwise silently zeros; it's
// plausible outgoing client requests re-serialize the same reflected
// UStruct fields as strings too, which the int32-only ExtractInt32Field
// cannot see. Trying both is strictly more permissive than int32-only and
// costs nothing when the client does send int32.
func firstMmogInt32Field(payload []byte, names ...string) int32 {
	for _, name := range names {
		if value, ok := protocol.ExtractInt32Field(payload, name); ok {
			return value
		}
	}
	for _, name := range names {
		if raw := protocol.ExtractStringField(payload, name); raw != "" {
			if value, err := strconv.Atoi(raw); err == nil {
				return int32(value)
			}
		}
	}
	return 0
}

func firstMmogStringField(payload []byte, names ...string) string {
	for _, name := range names {
		if value := protocol.ExtractStringField(payload, name); value != "" {
			return value
		}
	}
	return ""
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// persistedPlayerShipXP returns the XP the player has accumulated on one ship,
// or 0 when there is no row (or no database). YA_UnlockItem's response echoes
// it back alongside the free-XP balance.
func persistedPlayerShipXP(playerPID string, shipID int32) int32 {
	if shipID == 0 {
		return 0
	}
	database := currentMmogPlayerStateDB()
	if database == nil {
		return 0
	}
	var xp int32
	if err := database.QueryRow(`SELECT xp FROM player_ship_xp WHERE user_id=? AND ship_id=?`,
		normalizedPlayerStatePID(playerPID), shipID).Scan(&xp); err != nil {
		return 0
	}
	return xp
}

// grantUnlockedShipLoadout gives the player a loadout row for a ship they just
// unlocked.
//
// A purchase record alone is not ownership as far as the client is concerned.
// Every ship the client treats as owned is one the player has a LOADOUT for --
// that is what populates UYLoadoutManager, which is what the hangar and the
// fleet screens read. The four starter ships are owned precisely because they
// have rows here.
//
// Evidence it is not the purchases list: with the ids in PurchasesData and
// m_isOwned set on the progression rows, the client still re-sent YA_UnlockItem
// for 33489267 -- an id already in player_purchases -- so it had not learned
// ownership from either.
//
// The row carries identity only: loadout id, the precast id, the pawn behind
// it, the blueprint class name and the ship's name. Item slots are left at zero
// because the client instantiates the precast blueprint named in
// native_loadout_id and that blueprint carries its own weapons, abilities and
// perks -- the same reason nativeStarterLoadoutClassName has to name the
// SHIPPING asset rather than a development one.
func grantUnlockedShipLoadout(tx *sql.Tx, playerPID string, precastLoadoutID int32) error {
	switch category := (precastLoadoutID >> 24) & 0xff; category {
	case mmogItemCategoryShipLoadoutPrecast, mmogItemCategoryShipLoadoutHero:
	default:
		return nil // modules and the like own nothing on their own
	}
	shipID, ok := dreadconfig.ShipIDForPrecastLoadout(precastLoadoutID)
	if !ok {
		return nil
	}
	nativeID, ok := nativeStarterLoadoutClassName(precastLoadoutID)
	if !ok {
		return nil
	}
	name := ""
	if authoritative, ok := dreadconfig.AuthoritativeShipName(shipID); ok {
		name = authoritative
	}
	var nextPosition int32
	if err := tx.QueryRow(`SELECT COALESCE(MAX(position)+1,0) FROM player_ship_loadouts WHERE user_id=?`,
		playerPID).Scan(&nextPosition); err != nil {
		return fmt.Errorf("next loadout position: %w", err)
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO player_ship_loadouts(
		user_id,loadout_id,native_loadout_id,precast_loadout_id,ship_id,loadout_index,loadout_name,position,active
	) VALUES(?,?,?,?,?,0,?,?,1)`,
		playerPID, precastLoadoutID, nativeID, precastLoadoutID, shipID, name, nextPosition); err != nil {
		return fmt.Errorf("grant loadout for unlocked %d: %w", precastLoadoutID, err)
	}
	return nil
}
