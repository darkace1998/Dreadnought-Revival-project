package main

import "testing"

// Every hero resolves its own tier (no tier-1 fallback), so owned heroes
// count toward the fleet unlocks and progression shows their real tier.
func TestHeroShipsResolveTheirTier(t *testing.T) {
	for _, h := range heroShipLoadouts {
		got, ok := shipTierForIDChecked(h.loadoutID)
		if !ok || got != h.tier {
			t.Errorf("hero %s (%d): tier %d (derived %v), want %d", h.name, h.loadoutID, got, ok, h.tier)
		}
	}
}
