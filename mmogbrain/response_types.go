package main

import (
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

func (loadout mmogShipLoadoutSeed) displayInfo() string {
	return ""
}

var nativeStarterLoadoutIDsByPrecastID = map[int32]string{
	33489262: "Default__VH_AssaultMedium_T1_Loadout_BP_C",
	33489423: "Default__VH_DreadnoughtMedium_Loadout_BP_C",
	33489263: "Default__VH_SniperMedium_T1_Loadout_BP_C",
	33489264: "Default__VH_SupportMedium_T1_Loadout_BP_C",
}

var fleetStarterShipIDsByPrecastID = map[int32]int32{
	33489262: 33489198,
	33489423: 33489239,
	33489263: 33489199,
	33489264: 33489200,
}

func nativeStarterLoadoutID(precastLoadoutID int32) (string, bool) {
	id, ok := nativeStarterLoadoutIDsByPrecastID[precastLoadoutID]
	return id, ok
}

func fleetStarterShipIDForPrecast(precastLoadoutID int32) int32 {
	if id, ok := fleetStarterShipIDsByPrecastID[precastLoadoutID]; ok {
		return id
	}
	return 0
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
	itemID   int32
	index    int32
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
				itemID:   slot.itemID,
				index:    int32(len(seeds)),
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

var starterShipArchetypes = map[string]starterShipArchetype{
	"assault":     {classKey: "assault", classID: 14, shipClass: 0, manufacturer: "JupiterArms"},
	"dreadnought": {classKey: "dreadnought", classID: 6, shipClass: 4, manufacturer: "AkulaVektor"},
	"sniper":      {classKey: "sniper", classID: 10, shipClass: 2, manufacturer: "AkulaVektor"},
	"support":     {classKey: "support", classID: 12, shipClass: 3, manufacturer: "Oberon"},
}

var starterShips = []mmogShipSeed{
	{id: extractedShipIDAthos, name: "Athos", classID: 14, shipClass: 0, weight: 1, manufacturer: "JupiterArms", owned: true, nodeID: extractedShipIDAthos, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: 0, prereqID2: 0, bIsNew: false},    // Jupiter Arms Destroyer
	{id: extractedShipIDZmey, name: "Zmey", classID: 6, shipClass: 4, weight: 1, manufacturer: "AkulaVektor", owned: true, nodeID: extractedShipIDZmey, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: 0, prereqID2: 0, bIsNew: false},        // Akula Vektor Dreadnought
	{id: extractedShipIDSvarog, name: "Svarog", classID: 10, shipClass: 2, weight: 1, manufacturer: "AkulaVektor", owned: true, nodeID: extractedShipIDSvarog, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: 0, prereqID2: 0, bIsNew: false}, // Akula Vektor Artillery
	{id: extractedShipIDAion, name: "Aion", classID: 12, shipClass: 3, weight: 1, manufacturer: "Oberon", owned: true, nodeID: extractedShipIDAion, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: 0, prereqID2: 0, bIsNew: false},            // Oberon Tactical
}

var lockedT1Ships = []mmogShipSeed{
	{id: extractedShipIDValcour, name: "Valcour", classID: 2, shipClass: 1, weight: 0, manufacturer: "JupiterArms", owned: false, nodeID: extractedShipIDValcour, parentID: 0, nodeType: 0, unlockCost: 0, prereqID1: extractedShipIDAthos, prereqID2: 0, bIsNew: false},                        // Jupiter Arms Corvette
	{id: extractedShipIDLeipzig, name: "Leipzig", classID: 14, shipClass: 0, weight: 1, manufacturer: "JupiterArms", owned: false, nodeID: extractedShipIDLeipzig, parentID: extractedShipIDAthos, nodeType: 0, unlockCost: 5000, prereqID1: extractedShipIDAthos, prereqID2: 0, bIsNew: false}, // Jupiter Arms Destroyer T2
	{id: extractedShipIDTrieste, name: "Trieste", classID: 6, shipClass: 4, weight: 1, manufacturer: "AkulaVektor", owned: false, nodeID: extractedShipIDTrieste, parentID: extractedShipIDZmey, nodeType: 0, unlockCost: 5000, prereqID1: extractedShipIDZmey, prereqID2: 0, bIsNew: false},    // Akula Vektor Dreadnought T2
	{id: extractedShipIDCeres, name: "Ceres", classID: 12, shipClass: 3, weight: 1, manufacturer: "Oberon", owned: false, nodeID: extractedShipIDCeres, parentID: extractedShipIDAion, nodeType: 0, unlockCost: 5000, prereqID1: extractedShipIDAion, prereqID2: 0, bIsNew: false},              // Oberon Tactical follow-up
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

func techTreeShips() []mmogShipSeed {
	ships := allT1Ships()
	seen := make(map[int32]struct{}, len(ships)+len(starterShipLoadouts()))
	for _, ship := range ships {
		seen[ship.id] = struct{}{}
	}
	for _, loadout := range starterShipLoadouts() {
		fleetShipID := loadout.effectiveFleetShipID()
		if _, ok := seen[fleetShipID]; ok {
			continue
		}
		seen[fleetShipID] = struct{}{}
		ships = append(ships, mmogShipSeed{
			id:        fleetShipID,
			name:      loadout.ship.name + " fleet entry",
			classID:   loadout.ship.classID,
			shipClass: loadout.ship.shipClass,
			weight:    loadout.ship.weight,
			owned:     true,
			nodeID:    fleetShipID,
			nodeType:  0,
		})
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
		if loadout.ship.id == shipID || loadout.effectiveFleetShipID() == shipID {
			return loadout, true
		}
	}
	return mmogShipLoadoutSeed{}, false
}
