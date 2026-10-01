package dreadgameconfig

import (
	"strings"
	"testing"
)

// Every hero has its own look, resolved to real cosmetic ids.
func TestHeroShipAppearanceResolves(t *testing.T) {
	full := 0
	for id := range heroLoadoutIDsForTest(t) {
		a, ok := HeroShipAppearance(id)
		if !ok {
			t.Errorf("hero %d: no appearance", id)
			continue
		}
		if len(a.MeshParts) == 4 && a.Paint != 0 && a.Decal != 0 {
			full++
		}
		for _, item := range a.Items() {
			if c := (item >> 24) & 0xff; c < 20 || c > 24 {
				t.Errorf("hero %d: %d is not a ship cosmetic (category %d)", id, item, c)
			}
		}
	}
	if full < 40 {
		t.Errorf("only %d heroes resolve four parts, a paint and a decal", full)
	}
	info, ok := HeroShipDisplayInfo(67043392, "ScoutLight", "JupiterArms") // Hermes
	if !ok || len(strings.Split(info, ";")) != 5 || len(strings.Split(strings.Split(info, ";")[0], "#")) != 4 {
		t.Fatalf("Hermes display info %q is not m#m#m#m;e;p;p;d", info)
	}
	if strings.Contains(info, DefaultShipDisplayInfo("ScoutLight", "JupiterArms")) {
		t.Errorf("Hermes still has the base ship's appearance: %s", info)
	}
}

func heroLoadoutIDsForTest(t *testing.T) map[int32]bool {
	heroAppearanceOnce.Do(loadHeroAppearances)
	ids := map[int32]bool{}
	for id := range heroAppearances {
		ids[id] = true
	}
	if len(ids) < 40 {
		t.Fatalf("only %d hero appearances loaded", len(ids))
	}
	return ids
}
