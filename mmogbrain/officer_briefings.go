package main

import (
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// Officer briefings in the tech tree.
//
// The game's own text says how they work: "Ship Experience can be used in a
// specific ship's tech tree to research its modules, weapons, officer
// briefings, and higher-tier ships", "After you have researched a piece of
// tech, you can then buy it with Credits", and "Credits can be used to
// purchase modules, weapons, and officer briefings". The tech tree never
// listed any (no category-6 item was ever researched or bought on any account,
// 2026-10-01), so the officer slots could not be upgraded at all.
//
// What the client data fixes:
//   - which briefings exist: the regular PRK_COM/WPN/NAV/ENG perks the store
//     sold (CatalogIDTable carries 20 of them as "999"+id SKUs; the _Hero
//     variants are fitted to hero ships only and were never sold);
//   - which ships have officer slots: every tier III-V hull's blueprint fits
//     four briefings, no tier I-II hull fits any;
//   - the slot a briefing goes in: the client classifies the id itself (cached
//     item type, slot tags 7-10), as it does for modules.
//
// Briefing ids are SHARED (0xFF middle byte in every blueprint, the store and
// the conversion table) -- unlike weapons and modules they are not per ship.
// Research and ownership are therefore account-wide: the client keys
// research by item id, so a briefing researched on one ship is researched on
// all ("research ... officer briefings on any ship you own").

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

// officerBriefingXPCost is what researching a briefing costs.
// GUESS: no source gives briefing costs (like module costs, issue #66). One
// price for every ship, because research is account-wide and the server
// checks the offered XP against the tree's cost.
var officerBriefingXPCost = techTreeModuleXPCost(3)

// officerBriefingPrice is what buying a researched briefing costs, in
// credits. GUESS: the 5,000 the store already showed for every briefing (no
// source gives briefing prices, issue #66); one price, as ownership is
// account-wide.
const officerBriefingPrice int32 = 5000

// officerBriefingOfferSeeds are the briefings' store offers: HIDDEN, like the
// modules', so a briefing is bought through its tech tree entry after
// research (the BUY action looks up the item's offer, 0x41F4C0) rather than
// straight off the shelf -- 12 of them used to be sold openly with no research.
func officerBriefingOfferSeeds(owned map[int32]struct{}) []gatewayCatalogEntitySeed {
	var seeds []gatewayCatalogEntitySeed
	for _, id := range officerBriefings() {
		name, _ := dreadconfig.AuthoritativeItemName(id)
		key, found := dreadconfig.ItemHeadlineKey(id)
		if !found {
			key = marketItemLocalizationKeys[id]
		}
		seed := gatewayCatalogEntitySeed{
			itemID:          id,
			externalID:      extractedMarketItemExternalID(id, name),
			displayName:     name,
			localizationKey: key,
			entityType:      "item",
			itemType:        itemTypeFromCategoryLaw(id),
			priceCurrencyID: "CR",
			priceAmount:     officerBriefingPrice,
			quantity:        1,
			hidden:          true,
		}
		if _, bought := owned[id]; bought {
			seed.owned = true
		}
		seeds = append(seeds, seed)
	}
	return seeds
}

// officerBriefingTechTreeItems are a hull's briefing entries: every briefing
// it does not already fit, filed under it like a module.
func officerBriefingTechTreeItems(hull baseShipLoadout, manufacturerID int32, fitted map[int32]bool, position int32) []techTreeItem {
	if !hullHasOfficerSlots(hull) {
		return nil
	}
	var items []techTreeItem
	for _, id := range officerBriefings() {
		if fitted[id] {
			continue
		}
		items = append(items, techTreeItem{
			id:           id,
			classID:      hull.loadoutID,
			prereq:       []int32{hull.loadoutID},
			manufacturer: manufacturerID,
			tier:         techTreeWireTier(hull.tier),
			xpCost:       officerBriefingXPCost,
			position:     position,
			module:       true,
		})
		position++
	}
	return items
}

// officerResearchShip picks the ship whose XP pays for a briefing.
//
// The request names only the item (YA_UnlockItem: ItemID, ShipXp, FreeXp --
// sender 0x142a4c340), and a briefing id is the same on every ship, so the
// ship the client charged cannot be read from it. GUESS: the owned ship with
// officer slots that has the most ship XP, if that covers what was spent --
// right whenever only one ship could have paid. Must not run inside a
// database transaction (single connection).
func officerResearchShip(playerPID string, shipXP int32) (clientKey, pawn int32, ok bool) {
	if shipXP <= 0 {
		return 0, 0, false
	}
	xpByPawn := map[int32]int32{}
	for _, e := range persistedPlayerShipXPs(playerPID) {
		xpByPawn[e.shipID] = e.xp
	}
	hulls := map[int32]baseShipLoadout{}
	for _, h := range baseShipLoadouts {
		hulls[h.loadoutID] = h
	}
	best := int32(-1)
	for _, l := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(playerPID), playerPID) {
		hull, isBase := hulls[l.precastLoadoutID]
		if !isBase || !hullHasOfficerSlots(hull) {
			continue
		}
		p, found := dreadconfig.ShipIDForPrecastLoadout(hull.loadoutID)
		if !found {
			continue
		}
		if xp := xpByPawn[p]; xp >= shipXP && xp > best {
			best, clientKey, pawn, ok = xp, hull.loadoutID, p, true
		}
	}
	return clientKey, pawn, ok
}
