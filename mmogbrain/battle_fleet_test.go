package main

import "testing"

// A Veteran match pays the Veteran fleet's ships even though that fleet is
// not the one selected (active) in the hangar.
func TestBattleFleetIsTheMatchFleetTypeEvenWhenInactive(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	buildMmogPlayerGetPayload(pid) // creates the player and its fleets
	state := mmogPlayerStateForPID(pid)
	var veteran *mmogFleetSeed
	for i := range state.fleets {
		if state.fleets[i].fleetType == 2 {
			veteran = &state.fleets[i]
		}
	}
	if veteran == nil {
		t.Skip("the seeded player has no Veteran fleet")
	}
	if veteran.active {
		t.Skip("the Veteran fleet is the active one; this test needs it inactive")
	}
	got := battleFleetLoadouts(pid, 2)
	if len(got) != len(veteran.shipLoadouts) {
		t.Fatalf("Veteran match fleet has %d ships, the Veteran fleet %d", len(got), len(veteran.shipLoadouts))
	}
	for i := range got {
		if got[i].loadoutID() != veteran.shipLoadouts[i].loadoutID() {
			t.Errorf("ship %d is %d, the Veteran fleet has %d", i, got[i].loadoutID(), veteran.shipLoadouts[i].loadoutID())
		}
	}
}
