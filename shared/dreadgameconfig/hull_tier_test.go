package dreadgameconfig

import (
	"strings"
	"testing"
)

// ItemIDRegister maps fifteen precast ids -- one per Class x Size -- to the
// PREVIOUS build's tier-less asset, so the tier cannot be read off the path.
// Every one of them is a top-tier hull, and every one of them was reported as
// Tier 1.
//
// The expected tiers come from the blueprints (data/assets/HullNames.json,
// generated from the client's own precast assets) and agree with the community
// loadout reference in docs/reference/ on all five spot-checked here:
//
//	T5 Akula Vector Destroyer, Gora        /Precast/T5/VH_AssaultHeavy_T5_PrecastLoadout_BP
//	T5 Jupiter Arms Destroyer, Athos       /Precast/T5/VH_AssaultMedium_T5_PrecastLoadout_BP
//	T5 Akula Vector Dreadnought, Zmey      /Precast/T5/VH_DreadnoughtMedium_T5_PrecastLoadout_BP
//	T4 Oberon Tactical Cruiser, Aion       /Precast/T4/VH_SupportMedium_T4_PrecastLoadout_BP
//	T3 Oberon Corvette, Fulgora            /Precast/T3/VH_ScoutMedium_T3_PrecastLoadout_BP
func TestTierlessPrecastIDsResolveToTheirRealTier(t *testing.T) {
	for _, tc := range []struct {
		id   int32
		name string
		want int
	}{
		{33489315, "Athos", 5},   // in the market catalog
		{33489318, "Zmey", 5},    // in the market catalog
		{33489331, "Aion", 4},    // in the market catalog
		{33489313, "Gora", 5},    // cross-checked against the community reference
		{33489325, "Fulgora", 3}, // the only one below T4, so not a constant in disguise
	} {
		got, ok := HullTierForItemID(tc.id)
		if !ok {
			t.Errorf("%s (%d): no tier at all", tc.name, tc.id)
			continue
		}
		if got == 1 && tc.want != 1 {
			t.Errorf("%s (%d) = tier 1, the fallback; want %d. The name join is not being applied.",
				tc.name, tc.id, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("%s (%d) = tier %d, want %d", tc.name, tc.id, got, tc.want)
		}
	}
}

// Where the path states a tier it wins, and the answer must not move.
func TestTieredPrecastPathsAreUnchanged(t *testing.T) {
	for _, tc := range []struct {
		id   int32
		name string
		want int
	}{
		{33489262, "Agosta", 1},
		{33489263, "Rurik", 1},
		{33489264, "Cerberus", 1},
		{33489276, "Machias", 3},
		{33489305, "Nevis", 5},
	} {
		if got, ok := HullTierForItemID(tc.id); !ok || got != tc.want {
			t.Errorf("%s (%d) = %d (ok=%v), want %d", tc.name, tc.id, got, ok, tc.want)
		}
	}
}

// The join is by NAME, so it is only sound while hull names are unique. They
// are (52 of 52); if an asset change breaks that, the index drops the ambiguous
// name and this test says why rather than letting a hull silently take another
// hull's tier.
func TestHullNamesAreUniqueEnoughToJoinOnTier(t *testing.T) {
	seen := map[string][]string{}
	for _, hull := range GetAllHullNames() {
		seen[hull.Name] = append(seen[hull.Name], hull.Asset)
	}
	for name, assets := range seen {
		if len(assets) > 1 {
			t.Errorf("hull name %q is used by %d assets (%v); HullTierForItemID cannot join on it",
				name, len(assets), assets)
		}
	}
}

// The name join must only rescue the tier-less player hulls. The category also
// holds Special/PAX, Havoc and Development variants, and a name collision there
// would hand one of them a real hull's tier.
func TestOnlyTierlessPlayerHullsAreJoinedByName(t *testing.T) {
	joined := 0
	for _, category := range GetAllCategories() {
		if category.CategoryName != "YShipLoadoutPrecast" {
			continue
		}
		for _, id := range category.ItemIDs {
			item, ok := ItemByID(id)
			if !ok {
				continue
			}
			if strings.Contains(item.AssetPath, "/Loadouts/Precast/T") {
				continue // the path states the tier; no join involved
			}
			if _, ok := HullTierForItemID(id); !ok {
				continue
			}
			joined++
			const want = "/Game/Generic/Loadouts/Precast/VH_"
			if !strings.HasPrefix(item.AssetPath, want) ||
				!strings.HasSuffix(item.AssetPath, "_PrecastLoadout_BP") {
				name, _ := AuthoritativeItemName(id)
				t.Errorf("%d (%q) was given a hull tier but is not a tier-less player hull: %s",
					id, name, item.AssetPath)
			}
		}
	}
	// Five classes x three sizes. A different number means the population
	// changed and the reasoning above should be re-checked.
	if joined != 15 {
		t.Errorf("%d ids resolved through the name join, want 15 (one per class x size)", joined)
	}
}
