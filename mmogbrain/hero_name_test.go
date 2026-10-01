package main

import (
	"testing"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// A hero is named after its own blueprint, not after the base hull whose pawn
// it flies: 38 heroes went out as "Furia", "Nav", "Tugarin"... and showed as
// those base ships (operator, 2026-10-01).
func TestHeroLoadoutsKeepTheirOwnName(t *testing.T) {
	checked := 0
	for _, h := range heroShipLoadouts {
		shipID, ok := dreadconfig.ShipIDForPrecastLoadout(h.loadoutID)
		if !ok {
			continue
		}
		own, _ := dreadconfig.CookedHeroName(h.loadoutID)
		base, hasBase := dreadconfig.AuthoritativeShipName(shipID)
		if hasBase && base != own {
			if got := heroLoadoutName(h.loadoutID, shipID, base); got != own {
				t.Errorf("%s stored as %q goes out as %q, want %q", h.name, base, got, own)
			}
			checked++
		}
		if got := heroLoadoutName(h.loadoutID, shipID, "My Ship"); got != "My Ship" {
			t.Errorf("%s: a player's own name was replaced with %q", h.name, got)
		}
	}
	if checked < 10 {
		t.Errorf("only %d heroes share a pawn with a differently named base hull", checked)
	}
	if got := heroLoadoutName(33489262, 184483982, "Agosta"); got != "Agosta" {
		t.Errorf("a base ship was renamed to %q", got)
	}
}
