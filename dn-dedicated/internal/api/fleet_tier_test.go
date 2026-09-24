package api

import "testing"

// fleet_tier reaches the battle server as ?FleetTier=<n>, and nothing else a
// caller sends can: only the two values the GameState decodes (0x3A5831) pass.
func TestFleetTierURLOptions(t *testing.T) {
	for tier, want := range map[int]string{4: "FleetTier=4", 5: "FleetTier=5"} {
		got, err := fleetTierURLOptions(tier)
		if err != nil || len(got) != 1 || got[0] != want {
			t.Errorf("fleet_tier %d -> %v, %v; want [%s]", tier, got, err, want)
		}
	}
	if got, err := fleetTierURLOptions(0); err != nil || len(got) != 0 {
		t.Errorf("fleet_tier 0 (Recruit) -> %v, %v; want no option", got, err)
	}
	for _, bad := range []int{1, 2, 3, 6, -1} {
		if _, err := fleetTierURLOptions(bad); err == nil {
			t.Errorf("fleet_tier %d was accepted", bad)
		}
	}
}
