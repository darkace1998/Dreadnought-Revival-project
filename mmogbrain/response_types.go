package main

import (
	"log"
	"strconv"
	"strings"

	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
)

type mmogShipSeed struct {
	id           int32
	name         string
	classID      int32
	shipClass    int32
	weight       int32
	manufacturer string
	owned        bool
	nodeID       int32
	parentID     int32
	nodeType     int32
	unlockCost   int32
	prereqID1    int32
	prereqID2    int32
	bIsNew       bool
	// isLoadoutAlias marks a synthetic tech-tree entry keyed by a loadout id
	// rather than a real ship id (see techTreeShips). It should appear in the
	// tech tree lookup array but be excluded from ship-count/progression
	// listings, which must reflect real ships only.
	isLoadoutAlias bool
}

type mmogShipLoadoutSeed struct {
	ship              mmogShipSeed
	fleetShipID       int32
	playerLoadoutID   int32
	precastLoadoutID  int32
	nativeLoadoutID   string
	loadoutIndex      int32
	loadoutName       string
	position          int32
	active            bool
	weaponPrimaryID   int32
	weaponSecondaryID int32
	abilityIDs        [4]int32
	perkIDs           [4]int32
}

func (loadout mmogShipLoadoutSeed) loadoutID() int32 {
	if loadout.playerLoadoutID != 0 {
		return loadout.playerLoadoutID
	}
	return loadout.precastLoadoutID
}

func (loadout mmogShipLoadoutSeed) effectiveFleetShipID() int32 {
	if loadout.fleetShipID != 0 {
		return loadout.fleetShipID
	}
	return loadout.ship.id
}

func (loadout mmogShipLoadoutSeed) entryID() string {
	if loadout.nativeLoadoutID != "" {
		return loadout.nativeLoadoutID
	}
	return "Default__" + strings.ReplaceAll(loadout.loadoutName, " ", "") + "_" + strconv.FormatInt(int64(loadout.loadoutID()), 10) + "_C"
}

// noShipVanityDisplayInfo is a ship's cosmetic-customisation string with nothing
// applied. It is NOT the functional loadout — weapons, abilities and perks
// travel in their own fields (weaponPrimary, m_abilityIDs, m_perkIDs, ...).
//
// The format comes from UYShipCustomisationComponent::ExportToDisplayInfo,
// which builds exactly "%s;%s;%s;%s;%s" — five semicolon-separated groups, the
// first of which is itself "%s#%s#%s#%s" (four mesh ids). Its importer enforces
// that: ImportFromDisplayInfo rejects anything that does not split into exactly
// five parts with "Invalid import string %s", and rejects a first group that
// does not split into four with "Invalid mesh import string %s". A group
// shorter than two characters is skipped silently, so five empty groups is
// precisely what the client itself exports for a ship with no vanity applied.
//
// Both extremes were wrong. An empty string made
// UYShipLoadout::ImportLoadoutParameterAsync log "No item IDs retrieved from
// display info!" and load zero assets. A ten-slot item list (an earlier attempt
// here) cleared that but tripped the customisation importer's five-part check.
//
// "nothing applied" is spelled as an explicit -1 per slot, not as empty groups.
// GetItemIDsFromDisplayInfoString treats a group shorter than two characters as
// missing: it logs "Using -1 for empty entriy in string %s" and substitutes -1
// anyway. So ";;;;" and this string produce identical results, except that
// ";;;;" logged an error for every group of every call -- and the client calls
// it once per vanity category, eight categories per loadout, on every refresh,
// which buried the log in thousands of identical Error lines.
//
// The separators are ';' between groups and '#' inside the mesh group,
// confirmed from the parser's own split constants (0x142f90f34 and
// 0x142f913e4). Five groups whose first splits into exactly four satisfies both
// importer checks, and yields 4+1+1+1+1 = 8 ids -- exactly the eight categories
// ObjectToIDCachedVanity asks for.
const noShipVanityDisplayInfo = "-1#-1#-1#-1;-1;-1;-1;-1"

// displayInfo is the ship's cosmetic-customisation string. Starter ships ship
// with no vanity applied, and this server does not yet persist per-ship vanity,
// so every loadout reports the empty-but-well-formed form.
func (loadout mmogShipLoadoutSeed) displayInfo() string {
	return noShipVanityDisplayInfo
}

// nativeStarterLoadoutClassName is the blueprint class the client instantiates
// for a starter loadout, derived from the loadout id's registered asset path.
//
// This was a hardcoded table naming the DEVELOPMENT blueprints --
// Default__VH_AssaultMedium_T1_Loadout_BP_C and friends, from
// /Game/Generic/Loadouts/Precast/Development/. That is where the whole
// 33489198/33489239/33489199/33489200 id space came from: the client
// instantiates the blueprint named here and then reports the blueprint's OWN
// m_precastLoadoutID (UYShipLoadout+0xC0), which for a development BP is the
// development item id, not the id we asked for. Confirmed from
// "ObjectToIDCachedVanity | Loadout Simargl | 33489239", whose %d argument is
// exactly that offset while we were sending precastLoadoutID 33489423.
//
// Deriving it from the register instead yields the shipping assets, and matches
// what ItemIDConversionTable independently records for the three of these four
// that appear in it (e.g. 33489262 ->
// .../T1/VH_AssaultMedium_T1_PrecastLoadout_BP.Default__VH_AssaultMedium_T1_PrecastLoadout_BP_C).
func nativeStarterLoadoutClassName(precastLoadoutID int32) (string, bool) {
	item, ok := dreadconfig.ItemByID(precastLoadoutID)
	if !ok || item.AssetPath == "" {
		return "", false
	}
	asset := item.AssetPath
	if idx := strings.LastIndex(asset, "/"); idx >= 0 {
		asset = asset[idx+1:]
	}
	if asset == "" {
		return "", false
	}
	return "Default__" + asset + "_C", true
}

func nativeStarterLoadoutID(precastLoadoutID int32) (string, bool) {
	return nativeStarterLoadoutClassName(precastLoadoutID)
}

// fleetStarterShipIDForPrecast is the id the fleet reports for a slot.
//
// It used to map each loadout to the DEVELOPMENT blueprint's item id, which
// only made sense while nativeStarterLoadoutID pointed at those blueprints.
// Now that the client instantiates the shipping precast asset, it reports that
// asset's own id, so the two have to agree -- hence the identity. Getting this
// out of step with the class name above is what breaks the hangar's flagship
// lookup, which resolves FlagShipID against the loadouts the client actually
// built ("AYGameMode_Outpost::InitializeOutpostShipInternal | No flagship
// found!").
func fleetStarterShipIDForPrecast(precastLoadoutID int32) int32 {
	return precastLoadoutID
}

func countOwnedShips(ships []mmogShipSeed) int {
	count := 0
	for _, ship := range ships {
		if ship.owned {
			count++
		}
	}
	return count
}

func (loadout mmogShipLoadoutSeed) weaponIDs() []int32 {
	return collectNonZeroItemIDs(loadout.weaponPrimaryItemID(), loadout.weaponSecondaryItemID())
}

func (loadout mmogShipLoadoutSeed) abilityItemIDs() []int32 {
	return collectNonZeroItemIDs(
		loadout.abilityItemID(0),
		loadout.abilityItemID(1),
		loadout.abilityItemID(2),
		loadout.abilityItemID(3),
	)
}

func (loadout mmogShipLoadoutSeed) perkItemIDs() []int32 {
	return collectNonZeroItemIDs(loadout.perkIDs[:]...)
}

func (loadout mmogShipLoadoutSeed) perkNames() []string {
	slotNames := []string{"Command Briefing", "Weapon Briefing", "Navigation Briefing", "Engineering Briefing"}
	names := make([]string, 0, len(slotNames))
	for idx, fallback := range slotNames {
		itemID := loadout.perkItemID(idx)
		if itemID == 0 {
			continue
		}
		names = append(names, extractedMarketItemDisplayName(itemID, fallback))
	}
	return names
}

func (loadout mmogShipLoadoutSeed) complete() bool {
	if len(loadout.loadoutSlots()) == 0 {
		return false
	}
	for _, slot := range loadout.loadoutSlots() {
		if slot.itemID == 0 {
			return false
		}
	}
	return true
}

type mmogLoadoutItemSeed struct {
	slotName    string
	headline    string
	description string
	itemType    string
	position    int32
	itemID      int32
	itemTier    int32
}

type mmogModuleUIDataSeed struct {
	itemID int32
	index  int32
	// shipID is the ship this module is fitted to. The client stores module UI
	// data keyed by ship (UTechTreeInterpreter::ComposeModuleUiDataForShip
	// looks it up by ship id and logged "Modules not found for ship id
	// 33489198"), so an entry with no ship cannot be found for any of them. It
	// is the fleet ship id, which is what the client asks with.
	shipID   int32
	owned    bool
	equipped bool
}

func collectNonZeroItemIDs(ids ...int32) []int32 {
	items := make([]int32, 0, len(ids))
	for _, itemID := range ids {
		if itemID != 0 {
			items = append(items, itemID)
		}
	}
	return items
}

func starterModuleUIDataSeeds() []mmogModuleUIDataSeed {
	loadouts := starterShipLoadouts()
	seen := make(map[int32]int)
	seeds := make([]mmogModuleUIDataSeed, 0, len(loadouts)*6)

	// F6: Wire perks into tech tree - add perk items to module UI data
	// Load all perks and add them to the seeds
	for _, perk := range dreadconfig.AllPerks() {
		if perk.PerkID == 0 {
			continue
		}
		if _, exists := seen[perk.PerkID]; exists {
			continue
		}
		seen[perk.PerkID] = len(seeds)
		seeds = append(seeds, mmogModuleUIDataSeed{
			itemID:   perk.PerkID,
			index:    int32(len(seeds)),
			owned:    true,
			equipped: false, // Perks are not equipped by default
		})
	}

	for _, loadout := range loadouts {
		for _, slot := range loadout.loadoutSlots() {
			if slot.itemID == 0 {
				continue
			}
			if _, exists := seen[slot.itemID]; exists {
				continue
			}
			if _, ok := extractedMarketItemMetadataForID(slot.itemID); !ok {
				continue
			}
			seen[slot.itemID] = len(seeds)
			seeds = append(seeds, mmogModuleUIDataSeed{
				itemID: slot.itemID,
				index:  int32(len(seeds)),
				// The pawn id: "ship_id" on an owned item names the ship the
				// item is fitted to, and the gateway's owned_items reports pawn
				// ids, so the two have to agree.
				shipID:   loadout.ship.id,
				owned:    true,
				equipped: true,
			})
		}
	}
	return seeds
}

func (loadout mmogShipLoadoutSeed) weaponPrimaryItemID() int32 {
	return loadout.weaponPrimaryID
}

func (loadout mmogShipLoadoutSeed) weaponSecondaryItemID() int32 {
	return loadout.weaponSecondaryID
}

func (loadout mmogShipLoadoutSeed) abilityItemID(index int) int32 {
	return loadout.abilityIDs[index]
}

func (loadout mmogShipLoadoutSeed) perkItemID(index int) int32 {
	return loadout.perkIDs[index]
}

func (loadout mmogShipLoadoutSeed) weaponSlots() []mmogLoadoutItemSeed {
	slots := []mmogLoadoutItemSeed{}
	if itemID := loadout.weaponPrimaryItemID(); itemID != 0 {
		slots = append(slots, mmogLoadoutItemSeed{slotName: "weaponPrimary", headline: extractedMarketItemDisplayName(itemID, "Primary Weapon"), description: loadout.loadoutName + " primary weapon slot", itemType: "weapon", position: 0, itemID: itemID, itemTier: 1})
	}
	if itemID := loadout.weaponSecondaryItemID(); itemID != 0 {
		slots = append(slots, mmogLoadoutItemSeed{slotName: "weaponSecondary", headline: extractedMarketItemDisplayName(itemID, "Secondary Weapon"), description: loadout.loadoutName + " secondary weapon slot", itemType: "weapon", position: 1, itemID: itemID, itemTier: 1})
	}
	return slots
}

func (loadout mmogShipLoadoutSeed) abilitySlots() []mmogLoadoutItemSeed {
	slotNames := []struct {
		name     string
		headline string
	}{
		{name: "abilityPrimary", headline: "Primary Ability"},
		{name: "abilitySecondary", headline: "Secondary Ability"},
		{name: "abilityPerimeter", headline: "Perimeter Ability"},
		{name: "abilityInternal", headline: "Internal Ability"},
	}
	slots := make([]mmogLoadoutItemSeed, 0, len(slotNames))
	for idx, slot := range slotNames {
		itemID := loadout.abilityItemID(idx)
		if itemID == 0 {
			continue
		}
		slots = append(slots, mmogLoadoutItemSeed{
			slotName:    slot.name,
			headline:    extractedMarketItemDisplayName(itemID, slot.headline),
			description: loadout.loadoutName + " " + strings.ToLower(slot.headline) + " slot",
			itemType:    "ability",
			position:    int32(idx),
			itemID:      itemID,
			itemTier:    1,
		})
	}
	return slots
}

func (loadout mmogShipLoadoutSeed) perkSlots() []mmogLoadoutItemSeed {
	slotNames := []struct {
		name     string
		headline string
	}{
		{name: "perkCom", headline: "Command Briefing"},
		{name: "perkWeapon", headline: "Weapon Briefing"},
		{name: "perkNavigation", headline: "Navigation Briefing"},
		{name: "perkEngineer", headline: "Engineering Briefing"},
	}
	slots := make([]mmogLoadoutItemSeed, 0, len(slotNames))
	for idx, slot := range slotNames {
		itemID := loadout.perkItemID(idx)
		if itemID == 0 {
			continue
		}
		slots = append(slots, mmogLoadoutItemSeed{
			slotName:    slot.name,
			headline:    extractedMarketItemDisplayName(itemID, slot.headline),
			description: loadout.loadoutName + " " + strings.ToLower(slot.headline) + " slot",
			itemType:    "perk",
			position:    int32(idx),
			itemID:      itemID,
			itemTier:    1,
		})
	}
	return slots
}

func (loadout mmogShipLoadoutSeed) loadoutSlots() []mmogLoadoutItemSeed {
	slots := make([]mmogLoadoutItemSeed, 0, len(loadout.weaponSlots())+len(loadout.abilitySlots())+len(loadout.perkSlots()))
	slots = append(slots, loadout.weaponSlots()...)
	slots = append(slots, loadout.abilitySlots()...)
	slots = append(slots, loadout.perkSlots()...)
	return slots
}

type mmogFleetSeed struct {
	fleetID              int32
	token                string
	displayName          string
	fleetType            int32
	tiers                []int32
	active               bool
	shipLoadouts         []mmogShipLoadoutSeed
	flagshipShipID       int32
	flagshipLoadoutID    int32
	flagshipLoadoutIndex int32
}

func (fleet mmogFleetSeed) flagshipIndex() int32 {
	if fleet.flagshipLoadoutIndex > 0 {
		return fleet.flagshipLoadoutIndex
	}
	for idx, loadout := range fleet.shipLoadouts {
		if loadout.effectiveFleetShipID() == fleet.flagshipShipID && loadout.loadoutID() == fleet.flagshipLoadoutID {
			return int32(idx)
		}
	}
	return 0
}

func (fleet mmogFleetSeed) flagshipOnly() mmogFleetSeed {
	var flagship []mmogShipLoadoutSeed
	for _, loadout := range fleet.shipLoadouts {
		if loadout.effectiveFleetShipID() == fleet.flagshipShipID && loadout.loadoutID() == fleet.flagshipLoadoutID {
			flagship = []mmogShipLoadoutSeed{loadout}
			break
		}
	}
	if len(flagship) == 0 && len(fleet.shipLoadouts) > 0 {
		flagship = []mmogShipLoadoutSeed{fleet.shipLoadouts[0]}
	}
	if len(flagship) > 0 {
		flagship[0].position = 0
		flagship[0].loadoutIndex = 0
	}
	fleet.shipLoadouts = flagship
	fleet.flagshipLoadoutIndex = 0
	if len(fleet.shipLoadouts) > 0 {
		fleet.flagshipShipID = fleet.shipLoadouts[0].effectiveFleetShipID()
		fleet.flagshipLoadoutID = fleet.shipLoadouts[0].loadoutID()
	}
	return fleet
}

// shipIDs is what the wire field "shipIds" carries: LOADOUT ids, not ship-pawn
// ids. Do not "fix" this to pawn ids -- that was tried in b5b6df1 and reverted
// in 24986d4 because it broke the hangar flagship spawn
// ("AYGameMode_Outpost::InitializeOutpostShipInternal | No flagship found!").
//
// That commit's justification was also simply wrong, and the reasoning is worth
// recording because it inverts what the client actually does. The gate on the
// tech-tree item array at manager+0x38 is
//
//	byte5(FUN_140547970(Id)) == 1 || FUN_1405483e0(Id)
//
// and both compare (Id >> 24) & 0xff -- the top byte of the id, a category tag,
// via FUN_1402cf640 -- against a resolved class: FUN_140547970 against
// YShipLoadoutHero, FUN_1405483e0 against YShipLoadoutPrecast. ItemIDTable gives
// those categories IDs 3 and 1. The top byte of 33489262/33489423/33489263/
// 33489264 is 1; YPawn is category 10 and the top byte of 184483982/184484170
// is 10.
//
// So that gate ADMITS precast and hero loadout ids and REJECTS pawn ids. Loadout
// ids belong here. It also means the ten pawn-id rows buildMmogTechTreeDocument
// currently emits are discarded by the loader, and a correct document would key
// its ship rows on precast loadout ids -- which is the next thing to change, not
// this field.
func (fleet mmogFleetSeed) shipIDs() []int32 {
	ids := make([]int32, 0, len(fleet.shipLoadouts))
	for _, loadout := range fleet.shipLoadouts {
		ids = append(ids, loadout.effectiveFleetShipID())
	}
	return ids
}

func (fleet mmogFleetSeed) loadoutIDs() []int32 {
	ids := make([]int32, 0, len(fleet.shipLoadouts))
	for _, loadout := range fleet.shipLoadouts {
		ids = append(ids, loadout.loadoutID())
	}
	return ids
}

func (fleet mmogFleetSeed) shipTechTreeComplete() []bool {
	complete := make([]bool, len(fleet.shipLoadouts))
	for idx := range complete {
		complete[idx] = true
	}
	return complete
}

type starterShipArchetype struct {
	classKey     string
	classID      int32
	shipClass    int32
	manufacturer string
}

// shipClass ordinals verified against decompiled enum-to-string function FUN_140303fb0
// (0=Dreadnought, 1=Corvette, 2=ArtilleryCruiser, 3=TacticalCruiser, 4=Destroyer/Assault)
// and cross-checked against asset paths in dynamic_item_catalog.go (Assault ships live
// under /Ships/Assault/, Dreadnought ships under /Ships/Dreadnought/).
var starterShipArchetypes = map[string]starterShipArchetype{
	"assault":     {classKey: "assault", classID: 14, shipClass: 4, manufacturer: "JupiterArms"},
	"dreadnought": {classKey: "dreadnought", classID: 6, shipClass: 0, manufacturer: "AkulaVektor"},
	"sniper":      {classKey: "sniper", classID: 10, shipClass: 2, manufacturer: "AkulaVektor"},
	"support":     {classKey: "support", classID: 12, shipClass: 3, manufacturer: "Oberon"},
}

var starterShips = []mmogShipSeed{
	{id: extractedShipIDAthos, name: "Athos", classID: 14, shipClass: 4, weight: 1, manufacturer: "JupiterArms", owned: true, nodeID: extractedShipIDAthos, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: 0, prereqID2: 0, bIsNew: false},    // Jupiter Arms Destroyer
	{id: extractedShipIDZmey, name: "Zmey", classID: 6, shipClass: 0, weight: 1, manufacturer: "AkulaVektor", owned: true, nodeID: extractedShipIDZmey, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: 0, prereqID2: 0, bIsNew: false},        // Akula Vektor Dreadnought
	{id: extractedShipIDSvarog, name: "Svarog", classID: 10, shipClass: 2, weight: 1, manufacturer: "AkulaVektor", owned: true, nodeID: extractedShipIDSvarog, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: 0, prereqID2: 0, bIsNew: false}, // Akula Vektor Artillery
	{id: extractedShipIDAion, name: "Aion", classID: 12, shipClass: 3, weight: 1, manufacturer: "Oberon", owned: true, nodeID: extractedShipIDAion, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: 0, prereqID2: 0, bIsNew: false},            // Oberon Tactical
}

var lockedT1Ships = []mmogShipSeed{
	{id: extractedShipIDValcour, name: "Valcour", classID: 2, shipClass: 1, weight: 0, manufacturer: "JupiterArms", owned: false, nodeID: extractedShipIDValcour, parentID: 0, nodeType: 0, unlockCost: 5000, prereqID1: extractedShipIDAthos, prereqID2: 0, bIsNew: false},                           // Jupiter Arms Corvette
	{id: extractedShipIDTrafalgar, name: "Trafalgar", classID: 14, shipClass: 4, weight: 1, manufacturer: "JupiterArms", owned: false, nodeID: extractedShipIDTrafalgar, parentID: extractedShipIDAthos, nodeType: 0, unlockCost: 5000, prereqID1: extractedShipIDAthos, prereqID2: 0, bIsNew: false}, // Jupiter Arms Destroyer T2
	{id: extractedShipIDNav, name: "Nav", classID: 6, shipClass: 0, weight: 1, manufacturer: "AkulaVektor", owned: false, nodeID: extractedShipIDNav, parentID: extractedShipIDZmey, nodeType: 0, unlockCost: 5000, prereqID1: extractedShipIDZmey, prereqID2: 0, bIsNew: false},                      // Akula Vektor Dreadnought T2
	{id: extractedShipIDCeres, name: "Ceres", classID: 12, shipClass: 3, weight: 1, manufacturer: "Oberon", owned: false, nodeID: extractedShipIDCeres, parentID: extractedShipIDAion, nodeType: 0, unlockCost: 5000, prereqID1: extractedShipIDAion, prereqID2: 0, bIsNew: false},                    // Oberon Tactical follow-up
}

func allT1Ships() []mmogShipSeed {
	installerStarterShips := starterBootstrapShips()
	ships := make([]mmogShipSeed, 0, len(installerStarterShips)+len(starterShips)+len(lockedT1Ships))
	seen := make(map[int32]struct{}, cap(ships))
	for _, group := range [][]mmogShipSeed{installerStarterShips, starterShips, lockedT1Ships} {
		for _, ship := range group {
			if _, ok := seen[ship.id]; ok {
				continue
			}
			seen[ship.id] = struct{}{}
			ships = append(ships, ship)
		}
	}
	return ships
}

// realShipsOnly drops synthetic loadout-alias entries (see techTreeShips),
// for consumers like ship-progression listings that must count/display real
// ships only, not the extra tech-tree lookup nodes.
func realShipsOnly(ships []mmogShipSeed) []mmogShipSeed {
	out := make([]mmogShipSeed, 0, len(ships))
	for _, ship := range ships {
		if !ship.isLoadoutAlias {
			out = append(out, ship)
		}
	}
	return out
}

// t1t2TechTreeShips is the authoritative T1+T2 tech-tree ship roster, validated
// directly against the client Content (DreadGame/Content/Generic):
//   - ship IDs are the real ship-pawn item IDs from data/assets/ItemIDRegister.json
//     (/Game/Generic/Ships/<Class>/<Weight>/T<n>/VH_..._Pawn_T<n>_BP). The four
//     T1 IDs exactly match the client's own starter_ship_ids
//     [184483982,184484170,184483950,184484202].
//   - each has a real precast loadout under /Game/Generic/Loadouts/Precast/T<n>/;
//     Support/Light T2 (pawn 184484238) is intentionally excluded — it has a
//     pawn but no precast loadout, so it is not a real playable node.
//   - classID/shipClass per archetype: Assault 14/4, Dreadnought 6/0, Sniper
//     10/2, Support 12/3, Scout 2/1; weight Light=0 Medium=1 Heavy=2.
//
// The client already ships every static ship/loadout/weapon definition in its
// own Content assets, so YA_GetTechTree only needs to convey per-node identity
// + the player's unlock/ownership state (see buildMmogTechTreePayload). T1 is
// owned by default; T2 is researchable (owned iff the player has purchased it).
// Manufacturer is REQUIRED on every row. The client groups the tech tree by
// maker, so rows without one cannot be placed on any manufacturer page and
// every page renders empty. Assignment follows starterShipArchetypes:
// assault -> Jupiter Arms, dreadnought/sniper -> Akula Vektor,
// support -> Oberon, scout -> Jupiter Arms.
var t1t2TechTreeShips = []mmogShipSeed{
	// T1 (owned by default)
	{id: 184483982, name: "Agosta", classID: 14, shipClass: 4, weight: 1, owned: true, nodeID: 184483982, nodeType: 0, unlockCost: 0, manufacturer: "JupiterArms"},
	{id: 184484170, name: "Simargl", classID: 6, shipClass: 0, weight: 1, owned: true, nodeID: 184484170, nodeType: 0, unlockCost: 0, manufacturer: "AkulaVektor"},
	{id: 184483950, name: "Rurik", classID: 10, shipClass: 2, weight: 1, owned: true, nodeID: 184483950, nodeType: 0, unlockCost: 0, manufacturer: "AkulaVektor"},
	{id: 184484202, name: "Cerberus", classID: 12, shipClass: 3, weight: 1, owned: true, nodeID: 184484202, nodeType: 0, unlockCost: 0, manufacturer: "Oberon"},
	// T2 (researchable; parentID links to the corresponding T1 node)
	{id: 184483981, name: "Trafalgar", classID: 14, shipClass: 4, weight: 1, owned: false, nodeID: 184483981, parentID: 184483982, prereqID1: 184483982, nodeType: 0, unlockCost: 5000, manufacturer: "JupiterArms"},
	{id: 184483972, name: "Nav", classID: 6, shipClass: 0, weight: 1, owned: false, nodeID: 184483972, parentID: 184484170, prereqID1: 184484170, nodeType: 0, unlockCost: 5000, manufacturer: "AkulaVektor"},
	{id: 184483949, name: "Tugarin", classID: 10, shipClass: 2, weight: 1, owned: false, nodeID: 184483949, parentID: 184483950, prereqID1: 184483950, nodeType: 0, unlockCost: 5000, manufacturer: "AkulaVektor"},
	{id: 184483954, name: "Furia", classID: 10, shipClass: 2, weight: 0, owned: false, nodeID: 184483954, parentID: 184483950, prereqID1: 184483950, nodeType: 0, unlockCost: 5000, manufacturer: "AkulaVektor"},
	{id: 184483964, name: "Dover", classID: 2, shipClass: 1, weight: 0, owned: false, nodeID: 184483964, parentID: 0, nodeType: 0, unlockCost: 5000, manufacturer: "JupiterArms"},
	{id: 184484205, name: "Orcus", classID: 12, shipClass: 3, weight: 1, owned: false, nodeID: 184484205, parentID: 184484202, prereqID1: 184484202, nodeType: 0, unlockCost: 5000, manufacturer: "Oberon"},
}

func techTreeShips() []mmogShipSeed {
	// Emptied to only the validated T1+T2 ships. The client holds all static
	// ship/loadout/weapon data in its own Content; the server only conveys
	// unlock/ownership state. Hero ships live in heroShipLoadouts and reach the
	// client through the tech tree DOCUMENT, not through these rows -- see
	// techTreeHeroItems. The old bootstrap/fleet-alias nodes are no longer
	// injected here either. If a fleet
	// references a node the client can't find locally we can add just that
	// node back, but default fleets use these T1 ships.
	ships := make([]mmogShipSeed, len(t1t2TechTreeShips))
	copy(ships, t1t2TechTreeShips)

	// Fleet-alias nodes are REQUIRED even in the minimal tech tree: the
	// client's hangar fleet loader (YUIHangarFleetData::Load) resolves each
	// fleet entry's ships by looking them up IN the tech tree by their
	// fleet-ship id / loadout id (33489xxx range), NOT by the T1/T2 pawn ids
	// (184xxxxx) above. Without these nodes the lookup fails, the fleet's ship
	// array comes back empty, and the client logs "Invalid fleet data, fleet
	// array is empty" -> HandleMmogbrainError(8) -> fleet-manager bit 2 never
	// completes (fleet stuck at [12], never [15]). These are cheap identity-only
	// rows. (Regression from the T1+T2 minimization, which dropped this loop.)
	seen := make(map[int32]struct{}, len(ships)+2*len(starterShipLoadouts()))
	for _, s := range ships {
		seen[s.id] = struct{}{}
	}
	for _, loadout := range starterShipLoadouts() {
		fleetShipID := loadout.effectiveFleetShipID()
		if _, ok := seen[fleetShipID]; !ok {
			seen[fleetShipID] = struct{}{}
			ships = append(ships, mmogShipSeed{
				id: fleetShipID, name: syntheticRowName(fleetShipID, loadout),
				classID: loadout.ship.classID, shipClass: loadout.ship.shipClass,
				weight: loadout.ship.weight, owned: true, nodeID: fleetShipID, nodeType: 0,
				// Without the manufacturer these synthetic entries cannot be
				// grouped under a maker, so the client's per-manufacturer tech
				// tree pages come up empty and
				// ComposeShipManufacturerDataForLoadout finds no maker for the
				// ship id the fleet reports.
				manufacturer: shipManufacturerForClassID(loadout.ship.classID, loadout.ship.manufacturer),
			})
		}
		loadoutID := loadout.loadoutID()
		if _, ok := seen[loadoutID]; !ok {
			seen[loadoutID] = struct{}{}
			ships = append(ships, mmogShipSeed{
				id: loadoutID, name: syntheticRowName(loadoutID, loadout),
				classID: loadout.ship.classID, shipClass: loadout.ship.shipClass,
				weight: loadout.ship.weight, owned: true, nodeID: loadoutID, nodeType: 0,
				manufacturer:   loadout.ship.manufacturer,
				isLoadoutAlias: true,
			})
		}
	}
	return ships
}

func runtimeStarterShipForInstallerClass(classKey string) (mmogShipSeed, bool) {
	switch strings.ToLower(strings.TrimSpace(classKey)) {
	case "assault":
		return starterShips[0], true
	case "dreadnought":
		return starterShips[1], true
	case "sniper":
		return starterShips[2], true
	case "support":
		return starterShips[3], true
	default:
		return mmogShipSeed{}, false
	}
}

func runtimeStarterShipForInstallerShipID(shipID int32) (mmogShipSeed, bool) {
	for _, pkg := range dreadconfig.InstallerStarterPackages() {
		if pkg.ShipID != shipID {
			continue
		}
		return runtimeStarterShipForInstallerClass(pkg.ClassKey)
	}
	return mmogShipSeed{}, false
}

func starterBootstrapShipByID(shipID int32) (mmogShipSeed, bool) {
	for _, loadout := range dreadconfig.StarterInventoryLoadouts() {
		if loadout.ShipID != shipID {
			continue
		}
		item, ok := dreadconfig.ItemByID(loadout.ShipID)
		if !ok {
			return mmogShipSeed{}, false
		}
		for _, pkg := range dreadconfig.InstallerStarterPackages() {
			if pkg.ShipID != loadout.ShipID {
				continue
			}
			archetype, ok := starterShipArchetypes[pkg.ClassKey]
			if !ok {
				return mmogShipSeed{}, false
			}
			return mmogShipSeed{
				id:           loadout.ShipID,
				name:         item.DisplayName,
				classID:      archetype.classID,
				shipClass:    archetype.shipClass,
				weight:       1,
				manufacturer: archetype.manufacturer,
				owned:        true,
				nodeID:       loadout.ShipID,
				parentID:     0,
				nodeType:     0,
				unlockCost:   0,
				prereqID1:    0,
				prereqID2:    0,
				bIsNew:       false,
			}, true
		}
	}
	return mmogShipSeed{}, false
}

func starterBootstrapShips() []mmogShipSeed {
	sharedLoadouts := dreadconfig.StarterInventoryLoadouts()
	ships := make([]mmogShipSeed, 0, len(sharedLoadouts))
	for _, loadout := range sharedLoadouts {
		ship, ok := starterBootstrapShipByID(loadout.ShipID)
		if !ok {
			panic("missing starter bootstrap ship metadata")
		}
		ships = append(ships, ship)
	}
	return ships
}

func starterShipLoadouts() []mmogShipLoadoutSeed {
	sharedLoadouts := dreadconfig.StarterInventoryLoadouts()
	loadouts := make([]mmogShipLoadoutSeed, 0, len(sharedLoadouts))
	for idx, sharedLoadout := range sharedLoadouts {
		identity, ok := extractedStarterLoadoutIdentityForShip(sharedLoadout.ShipName)
		if !ok {
			panic("missing shared starter loadout identity")
		}
		ship, ok := starterBootstrapShipByID(sharedLoadout.ShipID)
		if !ok {
			panic("missing starter bootstrap ship")
		}
		loadoutMeta, ok := dreadconfig.ItemByID(sharedLoadout.LoadoutID)
		if !ok {
			panic("missing starter loadout metadata")
		}
		nativeID, ok := nativeStarterLoadoutID(sharedLoadout.LoadoutID)
		if !ok {
			panic("missing starter native loadout ID")
		}
		loadouts = append(loadouts, mmogShipLoadoutSeed{
			ship:              ship,
			fleetShipID:       fleetStarterShipIDForPrecast(sharedLoadout.LoadoutID),
			precastLoadoutID:  sharedLoadout.LoadoutID,
			nativeLoadoutID:   nativeID,
			loadoutIndex:      0,
			loadoutName:       loadoutMeta.DisplayName,
			position:          int32(idx),
			active:            true,
			weaponPrimaryID:   identity.weapons[0],
			weaponSecondaryID: identity.weapons[1],
			abilityIDs:        identity.abilities,
			perkIDs:           identity.perks,
		})
	}

	// I3: Add development loadouts as precast loadout references
	devLoadouts := developmentShipLoadouts()
	loadouts = append(loadouts, devLoadouts...)

	return loadouts
}

func starterFleetState() mmogFleetSeed {
	loadouts := starterShipLoadouts()
	return buildConfigBackedStarterFleet(loadouts)
}

func mmogFleetSeeds() []mmogFleetSeed {
	return buildConfigBackedFleetSeeds(starterShipLoadouts())
}

func starterShipIDs() []int32 {
	return starterFleetState().shipIDs()
}

func starterLoadoutIDs() []int32 {
	loadouts := starterShipLoadouts()
	ids := make([]int32, 0, len(loadouts))
	for _, loadout := range loadouts {
		ids = append(ids, loadout.loadoutID())
	}
	return ids
}

// developmentShipLoadouts returns development loadouts as mmogShipLoadoutSeed objects
// I3: Wire into YA_PlayerFleets and YA_RequestStaticFleetData as precast loadout references
func developmentShipLoadouts() []mmogShipLoadoutSeed {
	// Load development loadouts
	if err := dreadconfig.LoadDevLoadouts(); err != nil {
		log.Printf("Warning: Failed to load development loadouts: %v", err)
		return nil
	}

	devLoadouts := dreadconfig.DevLoadouts()
	loadouts := make([]mmogShipLoadoutSeed, 0, len(devLoadouts))

	for idx, devLoadout := range devLoadouts {
		// Try to get ship data using starterBootstrapShipByID
		ship, ok := starterBootstrapShipByID(devLoadout.ShipID)
		if !ok {
			// Skip development loadouts that don't have fleet ship data
			// I3: For now, only include development loadouts with known fleet ship IDs
			continue
		}

		// Create a native loadout ID from the DevLoadout ID
		// The DevLoadout ID is a string like "Default__VH_SupportHeavy_Indrik_DevLoadout_BP_C"
		// We'll use this as the nativeLoadoutID
		nativeID := devLoadout.ID

		// Create mmogShipLoadoutSeed
		loadoutSeed := mmogShipLoadoutSeed{
			ship:              ship,
			fleetShipID:       devLoadout.ShipID, // Use ShipID as fleetShipID
			precastLoadoutID:  0,                 // Will be set later if needed
			nativeLoadoutID:   nativeID,
			loadoutIndex:      0,
			loadoutName:       devLoadout.Name,
			position:          int32(idx),
			active:            true,
			weaponPrimaryID:   devLoadout.WeaponPrimary,
			weaponSecondaryID: devLoadout.WeaponSecondary,
			abilityIDs: [4]int32{
				devLoadout.AbilityPrimary,
				devLoadout.AbilitySecondary,
				devLoadout.AbilityPerimeter,
				devLoadout.AbilityInternal,
			},
			perkIDs: [4]int32{
				devLoadout.PerkCom,
				devLoadout.PerkWeapon,
				devLoadout.PerkNavigation,
				devLoadout.PerkEngineer,
			},
		}

		// Filter out invalid ItemIDs (0 or -1) from ability and perk arrays
		// For abilities
		validAbilities := [4]int32{}
		abilityIndex := 0
		for _, abilityID := range loadoutSeed.abilityIDs {
			if abilityID != 0 && abilityID != -1 && abilityIndex < 4 {
				validAbilities[abilityIndex] = abilityID
				abilityIndex++
			}
		}
		loadoutSeed.abilityIDs = validAbilities

		// For perks
		validPerks := [4]int32{}
		perkIndex := 0
		for _, perkID := range loadoutSeed.perkIDs {
			if perkID != 0 && perkID != -1 && perkIndex < 4 {
				validPerks[perkIndex] = perkID
				perkIndex++
			}
		}
		loadoutSeed.perkIDs = validPerks

		// Handle weapon slots
		if loadoutSeed.weaponPrimaryID == 0 || loadoutSeed.weaponPrimaryID == -1 {
			loadoutSeed.weaponPrimaryID = 0
		}
		if loadoutSeed.weaponSecondaryID == 0 || loadoutSeed.weaponSecondaryID == -1 {
			loadoutSeed.weaponSecondaryID = 0
		}

		loadouts = append(loadouts, loadoutSeed)
	}

	return loadouts
}

type mmogInventoryItemSeed struct {
	id           string
	name         string
	itemType     string
	externalID   string
	description  string
	itemID       int32
	shipID       int32
	loadoutID    int32
	manufacturer string
	slotName     string
	quantity     int32
}

func starterSeedSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")
	return value
}

func starterOwnedInventorySeeds() []mmogInventoryItemSeed {
	sharedItems := dreadconfig.StarterInventoryItems()
	items := make([]mmogInventoryItemSeed, 0, len(sharedItems))
	for _, item := range sharedItems {
		ship, _ := starterBootstrapShipByID(item.ShipID)
		shipSlug := starterSeedSlug(item.ShipName)
		fallbackExternalID := item.Item.ItemType + "_" + shipSlug
		seed := mmogInventoryItemSeed{
			name:         item.Item.DisplayName,
			itemType:     item.Item.ItemType,
			externalID:   extractedMarketItemExternalID(item.Item.ItemID, fallbackExternalID),
			description:  item.Item.DisplayName + " starter " + item.Item.ItemType + " entitlement",
			itemID:       item.Item.ItemID,
			shipID:       item.ShipID,
			loadoutID:    item.LoadoutID,
			manufacturer: ship.manufacturer,
			slotName:     item.SlotName,
			quantity:     1,
		}
		switch item.Item.ItemType {
		case "ship":
			seed.id = "ship_" + starterSeedSlug(item.Item.DisplayName)
		case "loadout":
			seed.id = "loadout_" + starterSeedSlug(item.Item.DisplayName)
			seed.externalID = extractedMarketItemExternalID(item.Item.ItemID, seed.id)
			seed.description = item.Item.DisplayName + " starter loadout entitlement"
		default:
			seed.id = "item_" + shipSlug + "_" + item.SlotName
			seed.externalID = extractedMarketItemExternalID(item.Item.ItemID, seed.id)
		}
		items = append(items, seed)
	}
	return items
}

func customMmogShipLoadoutsForPayload(loadouts []mmogShipLoadoutSeed) []mmogShipLoadoutSeed {
	custom := make([]mmogShipLoadoutSeed, 0, len(loadouts))
	for _, loadout := range loadouts {
		if isDefaultStarterShipLoadout(loadout) {
			continue
		}
		custom = append(custom, loadout)
	}
	return custom
}

func isDefaultStarterShipLoadout(loadout mmogShipLoadoutSeed) bool {
	starter, ok := starterLoadoutByPrecastID(loadout.precastLoadoutID)
	if !ok {
		return false
	}
	if loadout.loadoutID() != starter.loadoutID() ||
		loadout.entryID() != starter.entryID() ||
		loadout.ship.id != starter.ship.id ||
		loadout.loadoutName != starter.loadoutName ||
		loadout.weaponPrimaryItemID() != starter.weaponPrimaryItemID() ||
		loadout.weaponSecondaryItemID() != starter.weaponSecondaryItemID() {
		return false
	}
	for idx := range loadout.abilityIDs {
		if loadout.abilityItemID(idx) != starter.abilityItemID(idx) {
			return false
		}
	}
	for idx := range loadout.perkIDs {
		if loadout.perkItemID(idx) != starter.perkItemID(idx) {
			return false
		}
	}
	return true
}

func starterLoadoutByPrecastID(precastLoadoutID int32) (mmogShipLoadoutSeed, bool) {
	for _, loadout := range starterShipLoadouts() {
		if loadout.precastLoadoutID == precastLoadoutID {
			return loadout, true
		}
	}
	return mmogShipLoadoutSeed{}, false
}

func starterLoadoutByShipID(shipID int32) (mmogShipLoadoutSeed, bool) {
	for _, loadout := range starterShipLoadouts() {
		// Also match on the loadout's own id: techTreeShips() emits a tech
		// tree node keyed by loadout id (see the comment there), and that
		// node's ship.id is set to the loadout id too, so this lookup must
		// resolve it back to the same loadout to attach m_shipLoadoutInfo.
		if loadout.ship.id == shipID || loadout.effectiveFleetShipID() == shipID || loadout.loadoutID() == shipID {
			return loadout, true
		}
	}
	return mmogShipLoadoutSeed{}, false
}

// shipManufacturerForClassID resolves the maker for a synthetic tech-tree row.
// The fleet-alias and loadout-alias rows are built from a loadout whose ship
// record may carry no manufacturer, and a row without one cannot appear on any
// manufacturer page.
func shipManufacturerForClassID(classID int32, existing string) string {
	if existing != "" {
		return existing
	}
	switch classID {
	case 14, 2: // assault, scout
		return "JupiterArms"
	case 6, 10: // dreadnought, sniper
		return "AkulaVektor"
	case 12: // support
		return "Oberon"
	}
	return ""
}

// syntheticRowName names a synthetic tech tree row after the item it actually
// represents.
//
// These rows used to be labelled by composing strings -- "<ship> fleet entry" and
// "<ship> <loadout>" -- which put text the game never uses into a field that
// reaches the client as the market card's ParentCategoryName. Both of these ids
// are precast loadout ids, so the game has a real name for them.
func syntheticRowName(id int32, loadout mmogShipLoadoutSeed) string {
	if name, ok := authoritativeItemName(id); ok {
		return name
	}
	if loadout.loadoutName != "" {
		return loadout.loadoutName
	}
	return loadout.ship.name
}
