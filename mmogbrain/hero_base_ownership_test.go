package main

import "testing"

// Buying a hero before its base counterpart must not list the hero's
// per-class weapons/modules as owned: they are research items of that base
// hull, and GetTechTreeItemState answers "owned" for anything listed, which
// left them owned-but-unusable on a ship the player does not have
// (2026-10-14). Once the base hull is owned they count again.
func TestHeroFitOwnedOnClassOnlyWithItsBaseHull(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	database := currentMmogPlayerStateDB()
	pid := "0123456789abcdef0123456789abcd01"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	hero, ok := heroByID(67043373) // Leviathan, DreadnoughtLight T4
	if !ok {
		t.Fatal("Leviathan missing from the hero roster")
	}
	var base baseShipLoadout
	for _, b := range baseShipLoadouts {
		if b.hullLine == hero.hullLine && b.tier == hero.tier {
			base = b
		}
	}
	if base.loadoutID == 0 {
		t.Fatal("no base counterpart")
	}
	grant := func(id int32) {
		tx, err := database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := grantUnlockedShipLoadout(tx, pid, id); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	class := eyShipClassByKey[hero.hullLine]
	research := map[int32]bool{}
	for _, item := range techTreeModuleItems(base, 0) {
		research[item.id] = true
	}
	spilled := func() (n int) {
		owned := map[int32]bool{}
		for _, id := range clientOwnedItemIDs(pid) {
			owned[id] = true
		}
		for _, id := range append([]int32{hero.primary, hero.secondary}, hero.abilities[:]...) {
			if inf := inflatedItemID(id, class); id > 0 && research[inf] && owned[inf] {
				n++
			}
		}
		return n
	}

	grant(hero.loadoutID)
	if n := spilled(); n != 0 {
		t.Fatalf("hero alone lists %d of its base hull's research items as owned", n)
	}
	set := ownedItemSet(pid)
	if !set[hero.primary] || !set[sharedGearID(hero.abilities[0])] {
		t.Fatal("the hero's own fit is no longer fittable on the hero")
	}

	grant(base.loadoutID)
	if n := spilled(); n == 0 {
		t.Fatal("with the base hull owned, the hero's fit should count on its class again")
	}
}
