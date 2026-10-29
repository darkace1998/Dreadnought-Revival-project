package main

import "testing"

// A hero's XP must go out under the hero's own loadout id. Marauder (hero
// 67043393, pawn 184484238) shares its pawn with no base hull, so before
// 2026-10-29 its XP went out only under the pawn id and the client showed 0.
func TestHeroShipXPKeyedByHeroLoadout(t *testing.T) {
	const marauder, marauderPawn = int32(67043393), int32(184484238)
	got := map[int32]int32{}
	for _, e := range clientShipXPs([]shipXPEntry{{shipID: marauderPawn, xp: 8180}}) {
		got[e.shipID] = e.xp
	}
	if got[marauder] != 8180 {
		t.Errorf("Marauder's XP under %d = %d, want 8180 (entries %v)", marauder, got[marauder], got)
	}
	if got[marauderPawn] != 8180 {
		t.Errorf("the pawn-keyed entry is gone: %v", got)
	}
}

// Base hulls keep their hull loadout key: Dover (ScoutLight T2, 33489267) on
// pawn 184483964.
func TestBaseShipXPKeyedByHullLoadout(t *testing.T) {
	found := false
	for _, e := range clientShipXPs([]shipXPEntry{{shipID: 184483964, xp: 5961}}) {
		found = found || (e.shipID == 33489267 && e.xp == 5961)
	}
	if !found {
		t.Error("Dover's XP is not keyed by its hull loadout id 33489267")
	}
}
