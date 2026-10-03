package main

import (
	"testing"
)

// A base ship's own look -- named by its precast blueprint -- is owned with
// the ship. Onager (33489292) is built from the VAN_H_SniperH_Kore_* parts;
// these per-ship ids are exactly what a live player's Onager carried when the
// ownership check flagged it (2026-10-03).
func TestOwnedShipOwnsItsBlueprintLook(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	const onager int32 = 33489292
	kore := []int32{336265338, 336265339, 336265337, 336265336}
	onagerLook := "336265338#336265339#336265337#336265336;-1;-1;-1;-1"

	if got := unownedAppearanceItems(ownedItemSet(pid), onagerLook); len(got) != 4 {
		t.Fatalf("without Onager, unowned = %v; want all four parts", got)
	}

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
	if got := unownedAppearanceItems(owned, onagerLook); len(got) != 0 {
		t.Errorf("owning Onager, its own parts are flagged: %v", got)
	}
	for _, id := range kore {
		if !owned[sharedGearID(id)] {
			t.Errorf("part %d (shared %d) not owned with Onager", id, sharedGearID(id))
		}
	}
	// Another ship's look is still not owned.
	if got := unownedAppearanceItems(owned, "335937612#335937613#335937611#335937609;-1;-1;-1;-1"); len(got) != 4 {
		t.Errorf("Chernobog's parts (not owned) flagged %v, want all four", got)
	}
}
