package main

import (
	"database/sql"
	"errors"
	"fmt"
	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
	"strconv"
	"strings"
	"sync"

	"github.com/dreadnought-ps/mmogbrain/protocol"
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
		if _, err := tx.Exec(`INSERT OR IGNORE INTO player_fleets(user_id,fleet_id,token,display_name,fleet_type,active,flagship_ship_id,flagship_loadout_id,flagship_loadout_index) VALUES(?,?,?,?,?,?,?,?,?)`,
			pid, fleet.fleetID, fleet.token, fleet.displayName, fleet.fleetType, boolToInt(fleet.active), fleet.flagshipShipID, fleet.flagshipLoadoutID, fleet.flagshipIndex()); err != nil {
			return fmt.Errorf("seed player_fleets: %w", err)
		}
		for position, loadout := range fleet.shipLoadouts {
			if err := seedPersistedLoadout(tx, pid, loadout); err != nil {
				return err
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
		perk_com_id,perk_weapon_id,perk_navigation_id,perk_engineer_id
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
			&loadout.perkIDs[0], &loadout.perkIDs[1], &loadout.perkIDs[2], &loadout.perkIDs[3],
		); err != nil {
			return nil, fmt.Errorf("scan player_ship_loadouts: %w", err)
		}
		loadout.playerLoadoutID = loadoutID
		loadout.ship = persistedShipByID(shipID)
		if starter, ok := starterLoadoutByPrecastID(loadout.precastLoadoutID); ok {
			loadout.fleetShipID = starter.fleetShipID
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
	if len(purchased) == 0 {
		return ships
	}
	for idx := range ships {
		if _, ok := purchased[ships[idx].id]; ok {
			ships[idx].owned = true
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

func persistUpdateShipLoadout(database *sql.DB, playerPID string, payload []byte) error {
	loadoutID := firstMmogInt32Field(payload, "LoadoutID", "loadoutID", "precastLoadout", "precastLoadoutID")
	if loadoutID == 0 {
		return nil
	}
	assignments := []struct {
		column string
		fields []string
	}{
		{column: "weapon_primary_id", fields: []string{"weaponPrimary", "weaponPrimaryID"}},
		{column: "weapon_secondary_id", fields: []string{"weaponSecondary", "weaponSecondaryID"}},
		{column: "ability_primary_id", fields: []string{"abilityPrimary"}},
		{column: "ability_secondary_id", fields: []string{"abilitySecondary"}},
		{column: "ability_perimeter_id", fields: []string{"abilityPerimeter"}},
		{column: "ability_internal_id", fields: []string{"abilityInternal"}},
		{column: "perk_com_id", fields: []string{"perkCom"}},
		{column: "perk_weapon_id", fields: []string{"perkWeapon"}},
		{column: "perk_navigation_id", fields: []string{"perkNavigation"}},
		{column: "perk_engineer_id", fields: []string{"perkEngineer"}},
	}
	for _, assignment := range assignments {
		value := firstMmogInt32Field(payload, assignment.fields...)
		if value == 0 {
			continue
		}
		if _, err := database.Exec("UPDATE player_ship_loadouts SET "+assignment.column+"=?, updated_at=datetime('now') WHERE user_id=? AND loadout_id=?", value, playerPID, loadoutID); err != nil {
			return fmt.Errorf("update loadout %s: %w", assignment.column, err)
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

func persistAddToFleet(database *sql.DB, playerPID string, payload []byte) error {
	fleetID := firstMmogInt32Field(payload, "fleet id", "FleetType", "m_fleetId")
	loadoutID := firstMmogInt32Field(payload, "LoadoutID", "loadoutID", "FlagShipLoadoutID")
	shipID := firstMmogInt32Field(payload, "ShipID", "shipID", "shipId")
	if fleetID == 0 {
		fleetID = starterFleetState().fleetID
	}
	if loadoutID == 0 && shipID != 0 {
		if err := database.QueryRow(`SELECT loadout_id FROM player_ship_loadouts WHERE user_id=? AND ship_id=? ORDER BY position LIMIT 1`, playerPID, shipID).Scan(&loadoutID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lookup loadout for add to fleet: %w", err)
		}
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
	fleetID := firstMmogInt32Field(payload, "fleet id", "FleetType", "m_fleetId")
	loadoutID := firstMmogInt32Field(payload, "LoadoutID", "loadoutID", "FlagShipLoadoutID")
	if fleetID == 0 {
		fleetID = starterFleetState().fleetID
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
