package main

import (
	"testing"

	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
)

// Tier used to be inferred from unlockCost, so every researchable row claimed
// tier 2 regardless of what the ship actually is. The asset path states the
// tier, so any row that disagrees with its own asset is a defect.
func TestTechTreeTiersMatchTheAssetPaths(t *testing.T) {
	checked := 0
	for _, ship := range append(append([]mmogShipSeed{}, t1t2TechTreeShips...), lockedT1Ships...) {
		item, ok := dreadconfig.ItemByID(ship.id)
		if !ok {
			continue
		}
		match := shipAssetPathPattern.FindStringSubmatch(item.AssetPath)
		if match == nil {
			continue // hero loadouts and untiered hulls carry no tier in the path
		}
		checked++
		if got, want := techTreeRowTier(ship), match[3]; string(rune('0'+got)) != want {
			t.Errorf("ship %d (%s) reports tier %d, asset path says T%s", ship.id, item.AssetPath, got, want)
		}
	}
	if checked == 0 {
		t.Fatal("no tiered ship rows were checked; the derivation is not being exercised")
	}
	t.Logf("checked %d tiered rows", checked)
}
