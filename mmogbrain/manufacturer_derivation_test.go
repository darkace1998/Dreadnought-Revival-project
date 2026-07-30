package main

import (
	"testing"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// Manufacturer is server-authored -- nothing in the client carries it per ship
// -- so the only defence against it drifting is that it stays a function of the
// hull line. It had already drifted once: Sniper Light T2 (Furia) was
// AkulaVektor while every other SniperLight hull is Oberon.
func TestBaseShipManufacturersFollowTheHullLine(t *testing.T) {
	checked := 0
	for _, ship := range append(append([]mmogShipSeed{}, t1t2TechTreeShips...), lockedT1Ships...) {
		derived, ok := derivedShipManufacturer(ship.id)
		if !ok {
			continue // hero loadouts and untiered hulls carry no class/size path
		}
		checked++
		if got := shipManufacturer(ship); got != derived {
			t.Errorf("ship %d (%s) reports manufacturer %q, hull line says %q", ship.id, ship.name, got, derived)
		}
		if shipManufacturerID(derived) < 0 {
			item, _ := dreadconfig.ItemByID(ship.id)
			t.Errorf("ship %d (%s) derives manufacturer %q, which has no id; %s", ship.id, ship.name, derived, item.AssetPath)
		}
	}
	if checked == 0 {
		t.Fatal("no ships exercised the derivation")
	}
	t.Logf("checked %d hulls", checked)
}

// Every hull line must resolve, or a ship silently drops out of the tech tree:
// buildMmogTechTreeDocument skips any row whose manufacturer has no id.
func TestEveryHullLineHasAManufacturerID(t *testing.T) {
	for key, manufacturer := range baseShipManufacturerByClassSize {
		if shipManufacturerID(manufacturer) < 0 {
			t.Errorf("hull line %s maps to %q, which has no manufacturer id", key, manufacturer)
		}
	}
}
