package matchmaker

import (
	"encoding/json"
	"os"
	"strings"
)

// matchPin is a test hook: the next match formed is played in this mode on
// this map, whatever the player queued for. It exists so the headless client
// (scripts/cycle.py), which can only press the Proving Grounds button, can
// sweep every mode x map. Off unless DN_MATCH_PIN_FILE names a file; the file
// is consumed (deleted) by the match it pins, so one pin = one match.
//
//	{"mode": "TE", "map": "Gorge"}
//	{"mode": "Horde", "map": "PVE_S1E1", "path": "/Game/Maps/PVE/Season1/Episode1/PVE_S1E1_P"}
//
// "path" is only needed for a map none of the lists below know. "launch" sets
// the host's game= option when it differs from the recorded mode.
type matchPin struct {
	Mode   string `json:"mode"`
	Map    string `json:"map"`
	Path   string `json:"path"`
	Launch string `json:"launch"`
}

func takeMatchPin() (matchPin, bool) {
	var p matchPin
	f := strings.TrimSpace(os.Getenv("DN_MATCH_PIN_FILE"))
	if f == "" {
		return p, false
	}
	data, err := os.ReadFile(f) //nolint:gosec // operator-set test path
	if err != nil {
		return p, false
	}
	_ = os.Remove(f)
	if json.Unmarshal(data, &p) != nil || p.Mode == "" {
		return p, false
	}
	if p.Map != "" && p.Path == "" {
		p.Path = knownMapPath(p.Map)
	}
	return p, true
}

func knownMapPath(name string) string {
	lists := [][]GameMap{availableMaps, pveMaps, conquestMaps, onslaughtMaps}
	for _, l := range mapsByGameMode {
		lists = append(lists, l)
	}
	for _, l := range lists {
		for _, gm := range l {
			if strings.EqualFold(gm.Name, name) {
				return gm.Path
			}
		}
	}
	return ""
}
