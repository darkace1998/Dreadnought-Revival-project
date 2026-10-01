package main

import (
	"strconv"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// The rule: the four 101 briefings come with every tier III-V ship (they fit
// them), and each of the other 16 is unlocked by one tier III-IV ship and
// shown in that ship's tech tree.
func TestEachBriefingBelongsToItsUnlockingShip(t *testing.T) {
	if n := len(officerBriefings()); n != 20 {
		t.Fatalf("%d officer briefings, want the 20 the store sold", n)
	}
	hulls := map[int32]baseShipLoadout{}
	for _, h := range baseShipLoadouts {
		hulls[h.loadoutID] = h
	}
	fittedEverywhere := map[int32]int{}
	slotShips := 0
	for _, h := range baseShipLoadouts {
		if hullHasOfficerSlots(h) {
			slotShips++
			for _, p := range h.perks {
				fittedEverywhere[p]++
			}
		}
	}
	for _, id := range officerBriefings() {
		ship, unlocked := officerBriefingUnlockShip[id]
		if !unlocked {
			if fittedEverywhere[id] != slotShips {
				t.Errorf("briefing %d is neither unlocked by a ship nor fitted on every ship with officer slots", id)
			}
			continue
		}
		hull, ok := hulls[ship]
		if !ok {
			t.Errorf("briefing %d: unlocking ship %d is not in the roster", id, ship)
			continue
		}
		if hull.tier < 3 || hull.tier > 4 {
			t.Errorf("briefing %d: %s is tier %d, the wiki says tier III-IV", id, hull.name, hull.tier)
		}
	}
	if len(officerBriefingUnlockShip) != 16 {
		t.Errorf("%d unlocking ships, want 16", len(officerBriefingUnlockShip))
	}
	for _, h := range baseShipLoadouts {
		for _, item := range techTreeModuleItems(h, 0) {
			if !isOfficerBriefing(item.id) {
				continue
			}
			if officerBriefingUnlockShip[item.id] != h.loadoutID {
				t.Errorf("%s lists briefing %d, which another ship unlocks", h.name, item.id)
			}
			if item.xpCost != 0 {
				t.Errorf("%s: briefing %d costs %d XP; it comes with the ship", h.name, item.id, item.xpCost)
			}
		}
	}
}

// Owning the unlocking ship owns its briefing, account-wide, in what the
// client is told (PurchasesData and the inventory); research is refused and
// costs nothing; briefings are not sold.
func TestOwningTheShipOwnsItsBriefing(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	const briefing, ship int32 = 117374988, 33489274 // Slow and Steady <- Chernobog
	owns := func() (bool, bool) {
		inOwned := false
		for _, id := range clientOwnedItemIDs(pid) {
			inOwned = inOwned || id == briefing
		}
		inv := countWireStringField(string(buildMmogPlayerGetPayload(pid)), "ItemID", strconv.Itoa(int(briefing))) > 0
		return inOwned, inv
	}
	if a, b := owns(); a || b {
		t.Fatal("the briefing is owned before its ship")
	}

	if _, err := database.Exec(`UPDATE player_state SET free_xp=100000 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	req := protocol.AppendStringField(nil, "RT", "YA_UnlockItem")
	req = append(req, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(int(briefing)))...)
	req = append(req, protocol.AppendStringField(nil, "FreeXp", "3000")...)
	if err := persistUnlockItem(database, pid, protocol.AppendRootEnd(req)); err != nil {
		t.Fatal(err)
	}
	var freeXP int32
	_ = database.QueryRow(`SELECT free_xp FROM player_state WHERE user_id=?`, pid).Scan(&freeXP)
	if freeXP != 100000 {
		t.Errorf("researching a briefing charged %d free XP", 100000-freeXP)
	}

	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := grantUnlockedShipLoadout(tx, pid, ship); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// The ship alone is not enough: its briefing needs two of its modules
	// bought (officerBriefingModulesRequired).
	if a, _ := owns(); a {
		t.Error("the briefing is owned with no Chernobog module bought")
	}
	var chernobog baseShipLoadout
	for _, h := range baseShipLoadouts {
		if h.loadoutID == ship {
			chernobog = h
		}
	}
	bought := 0
	for _, item := range techTreeModuleItems(chernobog, 0) {
		if isOfficerBriefing(item.id) || bought == int(officerBriefingModulesRequired) {
			continue
		}
		if a, _ := owns(); a {
			t.Fatalf("the briefing is owned after %d modules", bought)
		}
		if _, err := database.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency) VALUES(?,?,'module',1000,'CR')`, pid, item.id); err != nil {
			t.Fatal(err)
		}
		bought++
	}
	if a, b := owns(); !a || !b {
		t.Errorf("owning Chernobog with %d modules bought: briefing owned=%v, in inventory=%v", bought, a, b)
	}
	// ...and the 101s it fits.
	const weapons101 int32 = 117374996
	found := false
	for _, id := range officerBriefingsOwnedThroughShips(pid) {
		found = found || id == weapons101
	}
	if !found {
		t.Error("a tier III ship does not bring Weapons 101")
	}

	for _, s := range gatewayItemCatalogSeeds(pid) {
		if isOfficerBriefing(s.itemID) {
			t.Errorf("briefing %d is sold in the store", s.itemID)
		}
	}
}

// The next ship needs the previous ship's modules bought, by its tier: 5, 7,
// 12, 17 (operator, from the original game: Jutland, tier IV, needs 17).
func TestNextShipNeedsModulesByTier(t *testing.T) {
	tierOf := map[int32]int32{}
	for _, h := range baseShipLoadouts {
		tierOf[h.loadoutID] = h.tier
	}
	want := map[int32]int32{1: 5, 2: 7, 3: 12, 4: 17}
	seen := map[int32]bool{}
	for _, item := range techTreeBaseItems() {
		if item.module || len(item.prereq) == 0 || item.techItemsRequired == 0 {
			continue
		}
		parentTier := tierOf[item.prereq[0]]
		if w, ok := want[parentTier]; ok && item.techItemsRequired != w {
			t.Errorf("hull %d (parent tier %d) needs %d modules, want %d", item.id, parentTier, item.techItemsRequired, w)
		}
		seen[parentTier] = true
	}
	for tier := int32(1); tier <= 4; tier++ {
		if !seen[tier] {
			t.Errorf("no ship unlocks from a tier %d parent", tier)
		}
	}
}
