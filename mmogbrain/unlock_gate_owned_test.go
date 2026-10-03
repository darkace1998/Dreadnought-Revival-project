package main

import "testing"

// The parent-modules gate counts what the client counts: every owned item of
// the parent's tree, including items owned because another owned ship of the
// same class fits them. A tier-III ship fits its class's tier-II rows, which
// are exactly a tier-II ship's research items.
func TestUnlockGateCountsModulesOwnedThroughOtherShips(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	var parent, child, other baseShipLoadout
	for _, h := range baseShipLoadouts {
		if h.hullLine == "AssaultMedium" && h.tier == 2 {
			parent = h
		}
		if h.hullLine == "AssaultMedium" && h.tier == 3 {
			other = h
		}
	}
	for _, item := range techTreeBaseItems() {
		if !item.module && len(item.prereq) > 0 && item.prereq[0] == parent.loadoutID {
			for _, h := range baseShipLoadouts {
				if h.loadoutID == item.id {
					child = h
				}
			}
		}
	}
	if parent.loadoutID == 0 || other.loadoutID == 0 || child.loadoutID == 0 {
		t.Fatal("test ships not found")
	}
	before, need, _, _ := hullUnlockShortfall(pid, child.loadoutID)

	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := grantUnlockedShipLoadout(tx, pid, other.loadoutID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after, _, _, _ := hullUnlockShortfall(pid, child.loadoutID)

	// The count must equal the owned list's share of the parent's tree.
	owned := map[int32]bool{}
	for _, id := range clientOwnedItemIDs(pid) {
		owned[id] = true
	}
	want := int32(0)
	for _, item := range techTreeModuleItems(parent, 0) {
		if owned[item.id] && !isOfficerBriefing(item.id) {
			want++
		}
	}
	if after != want {
		t.Errorf("gate counts %d of %s's modules, the client's owned list has %d", after, parent.name, want)
	}
	if after <= before {
		t.Errorf("owning %s did not count toward %s's gate (%d -> %d, need %d)", other.name, parent.name, before, after, need)
	}
}
