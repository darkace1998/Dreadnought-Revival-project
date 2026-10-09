package matchmaker

import (
	"strings"
	"testing"
)

// Conquest needs a <Map>_Territory sublevel (its capture points): Glacier and
// Space01 have none, and every Conquest match there had no capture points
// (host logs, 2026-10-07). Ceres Awakens needs Space02's PodTDM sublevel.
func TestModesOnlyGetMapsWithTheirSublevel(t *testing.T) {
	for _, mode := range []string{"TER", "Territory"} {
		maps := mapsByGameMode[mode]
		if len(maps) == 0 {
			t.Fatalf("%s has no map pool", mode)
		}
		for _, m := range maps {
			if m.Name == "Glacier" || m.Name == "Space" || strings.Contains(m.Path, "Space01") {
				t.Errorf("%s may run on %s, which has no Territory sublevel", mode, m.Name)
			}
		}
	}
	if p := mapsByGameMode["PodTDM"]; len(p) != 1 || !strings.Contains(p[0].Path, "Space02") {
		t.Errorf("PodTDM maps %v, want only Space02", p)
	}
}

// Onslaught uses the regular rotation plus Amirani, not Derelict ("Site 23",
// recognised by a tester as the Turbo TDM map, 2026-10-08).
func TestOnslaughtUsesTheRegularMaps(t *testing.T) {
	maps := mapsByGameMode["Onslaught"]
	if len(maps) != len(availableMaps)+1 {
		t.Fatalf("Onslaught maps %v, want the %d rotation maps plus Amirani", maps, len(availableMaps))
	}
	for _, m := range maps {
		if m.Name == "Derelict" {
			t.Errorf("Onslaught may run on Derelict")
		}
	}
}
