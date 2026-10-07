package main

import (
	"testing"
)

// A base ship's own look -- named by its precast blueprint -- is accepted on
// that ship. Onager (33489292) is built from the VAN_H_SniperH_Kore_* parts;
// these per-ship ids are exactly what a live player's Onager carried when the
// ownership check flagged it (2026-10-03).
//
// CHANGED 2026-10-07: they are no longer OWNED items. The client can only own
// a hull part per hull line, so owning Onager's hull made it selectable on
// every SniperHeavy -- another ship's hull (operator report: Jutland showed
// other makers' hulls). The look is accepted on Onager itself instead.
func TestOwnedShipOwnsItsBlueprintLook(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	const onager int32 = 33489292
	kore := []int32{336265338, 336265339, 336265337, 336265336}
	onagerLook := "336265338#336265339#336265337#336265336;-1;-1;-1;-1"

	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := grantUnlockedShipLoadout(tx, pid, onager); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	owned := ownedItemSet(pid)
	if !allowAppearance("enforce", owned, pid, onager, onagerLook) {
		t.Error("Onager's own look is refused on Onager")
	}
	for _, id := range kore {
		if owned[sharedGearID(id)] {
			t.Errorf("part %d (shared %d) is an owned item: it would be selectable on every SniperHeavy", id, sharedGearID(id))
		}
	}
	// Onager's hull on another SniperHeavy is not accepted.
	for _, b := range baseShipLoadouts {
		if b.hullLine == "SniperHeavy" && b.loadoutID != onager {
			if allowAppearance("enforce", owned, pid, b.loadoutID, onagerLook) {
				t.Errorf("Onager's hull accepted on %s", b.name)
			}
			break
		}
	}
	// Another ship's look is still not owned.
	if got := unownedAppearanceItems(owned, "335937612#335937613#335937611#335937609;-1;-1;-1;-1"); len(got) != 4 {
		t.Errorf("Chernobog's parts (not owned) flagged %v, want all four", got)
	}
}
