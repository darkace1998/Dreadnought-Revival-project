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
