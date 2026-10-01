package main

import (
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// Officer briefings.
//
// How the game unlocked them (Dreadnought wiki, "Officer Briefings": "As of
// the shipyard update, all officer briefings must be unlocked through
// purchasing specific ships within their tech trees. The tier of ships varies
// from Tier 3 to Tier 4"), and the briefing is then usable on every ship:
//
//   - the four "101" briefings (Communications/Engineering/Navigation/Weapons
//     101) are fitted on every tier III-V hull's blueprint, so any such ship
//     brings them;
//   - each of the other 16 is unlocked by ONE ship (officerBriefingUnlockShip).
//     The mapping is not in the client's data -- no progression blueprint fits
//     those 16 (only the PAX demo loadouts do) -- it comes from the wiki's
//     table, read through search-result quotes of it (the page itself refuses
//     this server). It is consistent with the client: every named ship is in
//     the roster, and every one is tier III or IV, as the wiki says.
//
// The tree carried no briefing at all, and no account ever owned one beyond
// what its ships fit (2026-10-01). A first attempt here made all 20 briefings
// researchable on every ship; that matched the generic "research ... officer
// briefings" text but not this rule, and it was replaced the same day.
//
// Briefing ids are SHARED (0xFF middle byte in every source), so ownership is
// account-wide, as the rule requires.

// officerBriefingUnlockShip maps each non-101 briefing to the ship (precast
// loadout id) whose purchase unlocks it. Source: see above.
var officerBriefingUnlockShip = map[int32]int32{
	117374977: 33489287, // Module Recycler     <- Lorica (Dreadnought, T4)
	117374978: 33489286, // Retaliator          <- Jutland (Dreadnought, T4)
	117374980: 33489281, // Feedback Loop       <- Palos (Tactical Cruiser, T3)
	117374981: 33489294, // Desperate Measures  <- Murometz (Artillery Cruiser, T4)
	117374983: 33489276, // It's a Trap!        <- Machias (Corvette, T3)
	117374984: 33489295, // Adrenaline Shot     <- Koschei (Tactical Cruiser, T4)
	117374986: 33489297, // Reinforced          <- Aion (Tactical Cruiser, T4)
	117374987: 33489288, // Tip the Scales      <- Voronezh (Dreadnought, T4)
	117374988: 33489274, // Slow and Steady     <- Chernobog (Dreadnought, T3)
	117374989: 33489291, // Navigation Expert   <- Medusa (Corvette, T4)
	117374990: 33489285, // Nerves of Steel     <- Vigo (Destroyer, T4)
	117374992: 33489284, // Engine Rigger       <- Vindicta (Destroyer, T4)
	117374993: 33489283, // Module Amper        <- Blud (Destroyer, T4)
	117374994: 33489293, // Glass Cannon        <- Nox (Artillery Cruiser, T4)
	117374995: 33489289, // Destruction Cascade <- Stribog (Corvette, T4)
	117374997: 33489292, // Survival Instinct   <- Onager (Artillery Cruiser, T4)
}

var (
	officerBriefingsOnce sync.Once
	officerBriefingList  []int32
	officerBriefingSet   map[int32]bool
)

func loadOfficerBriefings() {
	officerBriefingSet = map[int32]bool{}
	_ = dreadconfig.LoadCatalogIDTable() // the store's SKUs say which are player-facing
	for _, category := range dreadconfig.GetAllCategories() {
		if category.CategoryName != "YPerk" {
			continue
		}
		for _, id := range category.ItemIDs {
			item, ok := dreadconfig.ItemByID(id)
			if !ok {
				continue
			}
			name := path.Base(item.AssetPath)
			if !strings.HasPrefix(name, "PRK_") || strings.Contains(name, "_Hero") {
				continue
			}
			// The SKUs are stored as strings ("999"+id).
			if !dreadconfig.IsStringItemIDInCatalog("999" + strconv.Itoa(int(id))) {
				continue // never sold: not a player-facing briefing
			}
			officerBriefingSet[id] = true
			officerBriefingList = append(officerBriefingList, id)
		}
	}
	sort.Slice(officerBriefingList, func(i, j int) bool { return officerBriefingList[i] < officerBriefingList[j] })
}

// officerBriefings is every player-facing officer briefing, sorted.
func officerBriefings() []int32 {
	officerBriefingsOnce.Do(loadOfficerBriefings)
	return officerBriefingList
}

func isOfficerBriefing(id int32) bool {
	officerBriefingsOnce.Do(loadOfficerBriefings)
	return officerBriefingSet[id]
}

// hullHasOfficerSlots reports whether a hull's blueprint fits briefings.
func hullHasOfficerSlots(hull baseShipLoadout) bool {
	for _, perk := range hull.perks {
		if perk > 0 {
			return true
		}
	}
	return false
}

// officerBriefingModulesRequired is how many of the unlocking ship's modules
// must be bought before its briefing unlocks. Source: the operator, from
// gameplay footage (Jutland: 2 modules for its briefing, 2026-10-01). GUESS:
// that the briefing is then owned outright, with no XP or credits.
const officerBriefingModulesRequired = 2

// officerBriefingTechTreeItems is the briefing a ship unlocks, shown in its
// tech tree under it. It costs no XP: it comes with buying the ship.
func officerBriefingTechTreeItems(hull baseShipLoadout, manufacturerID int32, fitted map[int32]bool, position int32) []techTreeItem {
	var items []techTreeItem
	for _, id := range officerBriefings() {
		if officerBriefingUnlockShip[id] != hull.loadoutID || fitted[id] {
			continue
		}
		items = append(items, techTreeItem{
			id:           id,
			classID:      hull.loadoutID,
			prereq:       []int32{hull.loadoutID},
			manufacturer: manufacturerID,
			tier:         techTreeWireTier(hull.tier),
			position:     position,
			// The client's gate, shown as "requirements not met" until
			// the modules are bought (NumTechTreeItemsRequired).
			techItemsRequired: officerBriefingModulesRequired,
			module:            true,
		})
		position++
	}
	return items
}

// officerBriefingsOwnedThroughShips is every briefing the player owns by
// owning ships: the ones its ships fit (the 101s) and the ones its ships
// unlock. Shared ids, so they count on every ship.
func officerBriefingsOwnedThroughShips(playerPID string) []int32 {
	hulls := map[int32]baseShipLoadout{}
	for _, h := range baseShipLoadouts {
		hulls[h.loadoutID] = h
	}
	unlockedBy := map[int32][]int32{}
	for briefing, ship := range officerBriefingUnlockShip {
		unlockedBy[ship] = append(unlockedBy[ship], briefing)
	}
	seen := map[int32]bool{}
	var out []int32
	add := func(id int32) {
		if id > 0 && isOfficerBriefing(id) && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	bought := map[int32]bool{}
	for _, id := range ownedPurchaseItemIDs(playerPID) {
		bought[id] = true
	}
	for _, l := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(playerPID), playerPID) {
		hull, ok := hulls[l.precastLoadoutID]
		if !ok {
			continue
		}
		for _, p := range hull.perks {
			add(p)
		}
		if len(unlockedBy[hull.loadoutID]) == 0 {
			continue
		}
		// The ship's own briefing needs officerBriefingModulesRequired of its
		// modules bought first.
		have := int32(0)
		for _, item := range techTreeModuleItems(hull, 0) {
			if !isOfficerBriefing(item.id) && bought[item.id] {
				have++
			}
		}
		if have >= officerBriefingModulesRequired {
			for _, b := range unlockedBy[hull.loadoutID] {
				add(b)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
