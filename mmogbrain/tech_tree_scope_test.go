package main

import (
	"strings"
	"testing"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

func hullNamed(t *testing.T, name string) baseShipLoadout {
	t.Helper()
	for _, hull := range baseShipLoadouts {
		if hull.name == name {
			return hull
		}
	}
	t.Fatalf("no hull named %q", name)
	return baseShipLoadout{}
}

// Reported as "every ship gets every possible tech" from a tier-I and a tier-II
// ship both showing 25 (AGENT-CHAT C12.5). They do not: two hulls of the same
// CLASS share most of a pool because the client's own assets organise sniper
// secondaries and abilities by class with no size in their path, and two hulls
// of different classes share nothing at all.
func TestTechTreeIsSharedWithinAClassAndNotAcrossClasses(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	ids := func(name string) map[int32]bool {
		out := map[int32]bool{}
		for _, item := range techTreeModuleItems(hullNamed(t, name), 0) {
			out[item.id] = true
		}
		return out
	}
	rurik, furia, agosta := ids("Rurik"), ids("Furia"), ids("Agosta")

	shared := 0
	for id := range rurik {
		if furia[id] {
			shared++
		}
	}
	if shared == 0 {
		t.Errorf("Rurik and Furia are both Artillery Cruisers and share %d items; the class pool is not reaching them", shared)
	}
	for id := range rurik {
		if agosta[id] {
			item, _ := dreadconfig.ItemByID(id)
			t.Errorf("a Sniper hull and an Assault hull both offer %d (%s)", id, item.AssetPath)
		}
	}
}

// The slots that ARE size-specific -- weapons carry the hull size in their asset
// path, abilities do not -- must never offer a hull the wrong size's weapon.
func TestTechTreeNeverOffersTheWrongHullSize(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	for _, hull := range baseShipLoadouts {
		var wrong string
		switch {
		case strings.Contains(hull.hullLine, "Medium"):
			wrong = "/Light/"
		case strings.Contains(hull.hullLine, "Light"):
			wrong = "/Medium/"
		case strings.Contains(hull.hullLine, "Heavy"):
			wrong = "/Light/"
		default:
			continue
		}
		for _, item := range techTreeModuleItems(hull, 0) {
			asset, _ := dreadconfig.ItemByID(item.id)
			if !strings.Contains(asset.AssetPath, "/Weapons/") {
				continue // abilities and perks are class-level, not per size
			}
			if strings.Contains(asset.AssetPath, wrong) {
				t.Errorf("%s (%s) is offered %s", hull.name, hull.hullLine, asset.AssetPath)
			}
		}
	}
}

// The counter's numerator is zero by construction: this document carries only
// what the player does not have, because sending the equipped items too was
// verified to draw the loadout twice. Pinning it so that a future change which
// starts emitting owned items is a deliberate decision rather than a surprise.
func TestTechTreeExcludesWhatTheShipAlreadyFields(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	hull := hullNamed(t, "Rurik")
	equipped := map[int32]bool{hull.primary: true, hull.secondary: true}
	for _, id := range hull.abilities {
		equipped[id] = true
	}
	for _, item := range techTreeModuleItems(hull, 0) {
		if equipped[item.id] {
			t.Errorf("item %d is fitted to the ship AND offered in its tech tree; "+
				"the client draws fitted items from its own slot list, so this is the duplicate", item.id)
		}
	}
}
