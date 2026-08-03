package dreadgameconfig

import (
	"strings"
	"testing"
)

// Each hull has two live precast ids: the current tiered asset and the previous
// build's tier-less one. Only the tiered id is a SKU in the shipped
// CatalogIDTable (49 category-1 SKUs, 33489265..33489312, none of the fifteen
// tier-less ids), so the tiered id is the one the game sells.
func TestLegacyPrecastIDsResolveToTheSKUID(t *testing.T) {
	for _, tc := range []struct {
		legacy, canonical int32
		name              string
	}{
		{33489315, 33489300, "Athos"},
		{33489318, 33489303, "Zmey"},
		{33489331, 33489297, "Aion"},
		{33489313, 33489298, "Gora"},
	} {
		got := CanonicalPrecastLoadoutID(tc.legacy)
		if got == tc.legacy {
			t.Errorf("%s: %d was not substituted; the store still sells an id the game never did",
				tc.name, tc.legacy)
			continue
		}
		if got != tc.canonical {
			t.Errorf("%s: %d -> %d, want %d", tc.name, tc.legacy, got, tc.canonical)
		}
	}
}

// The substitution must be idempotent and must leave everything else alone --
// it runs over the whole catalog, not just the three hulls that needed it.
func TestCanonicalPrecastIDLeavesGoodIDsAlone(t *testing.T) {
	for _, id := range []int32{
		33489262,  // Agosta, already the tiered asset
		33489300,  // Athos, the canonical id itself
		33489198,  // a Development loadout: same category, not a player hull
		100597772, // a weapon: not category 1 at all
		184483950, // a ship pawn
	} {
		if got := CanonicalPrecastLoadoutID(id); got != id {
			t.Errorf("%d was rewritten to %d; only tier-less player hulls may be substituted", id, got)
		}
	}
}

// The substitution is by name, so it must map onto exactly the fifteen
// tier-less player hulls and nothing else in the category (Special/PAX, Havoc
// and Development variants share it).
func TestOnlyTierlessPlayerHullsAreSubstituted(t *testing.T) {
	substituted := 0
	for _, category := range GetAllCategories() {
		if category.CategoryName != "YShipLoadoutPrecast" {
			continue
		}
		for _, id := range category.ItemIDs {
			canonical := CanonicalPrecastLoadoutID(id)
			if canonical == id {
				continue
			}
			substituted++
			item, ok := ItemByID(id)
			if !ok {
				t.Errorf("%d was substituted but is not registered", id)
				continue
			}
			const prefix = "/Game/Generic/Loadouts/Precast/VH_"
			if !strings.HasPrefix(item.AssetPath, prefix) {
				name, _ := AuthoritativeItemName(id)
				t.Errorf("%d (%q) was substituted but is not a tier-less player hull: %s",
					id, name, item.AssetPath)
			}
			target, ok := ItemByID(canonical)
			if !ok || !strings.Contains(target.AssetPath, "/Loadouts/Precast/T") {
				t.Errorf("%d resolved to %d, which is not a tiered hull asset", id, canonical)
			}
		}
	}
	if substituted != 15 {
		t.Errorf("%d ids were substituted, want 15 (one per class x size)", substituted)
	}
}
