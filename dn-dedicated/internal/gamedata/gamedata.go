// Package gamedata holds the map and game-mode tables the engine will accept.
//
// PROVENANCE MATTERS HERE. Every value in this file is copied from the Revival
// project's mmogbrain/matchmaker/matchmaker.go, which in turn took them from the
// client's own GlobalUI.uasset (UYUIData::m_multiplayerMaps), pairing each
// m_mapName with the m_mapPath the engine actually loads.
//
// Nothing in this file may be invented. The Revival project's CONTRIBUTING.md
// records that an earlier map list -- Charon, Medusa, Procyon, DS-75, Onyx,
// Vesta, Kylo, Spree -- was made up, and that every match was consequently
// formed against a map that does not exist, which made matchmaking look broken
// for reasons unrelated to matchmaking. If you add a map here, cite where in the
// client's data you found it.
package gamedata

import (
	"fmt"
	"sort"
	"strings"
)

// Map is a playable map: the name the server reports, and the package path the
// engine loads. The path is what actually matters -- it becomes the positional
// URL argument, and the engine ignores a bare name.
type Map struct {
	Name string
	Path string
	PvE  bool
}

// multiplayerMaps are the five entries flagged m_avaliableTM in the client
// table. The rest of that table is night variants, Havoc maps and the tutorial,
// which are deliberately not offered here.
var multiplayerMaps = []Map{
	{Name: "Highlands", Path: "/Game/Maps/MP/Highlands/MP_Highlands_P"},
	{Name: "Glacier", Path: "/Game/Maps/MP/Glacier/MP_Glacier_P"},
	{Name: "Gorge", Path: "/Game/Maps/MP/Gorge/MP_Gorge_P"},
	{Name: "Space", Path: "/Game/Maps/MP/Space01/MP_Space01_P"},
	{Name: "Skybridge", Path: "/Game/Maps/MP/Skybridge/MP_Skybridge_P"},
}

// pveMaps are the real PvE entries from the same client table. "Iapetus" and
// "Kalyke" used to sit beside them in the Revival project and are not maps this
// game has.
var pveMaps = []Map{
	{Name: "Amirani", Path: "/Game/Maps/MP/Amirani/MP_Amirani_P", PvE: true},
	{Name: "Derelict", Path: "/Game/Maps/MP/Derelict/MP_Derelict_P", PvE: true},
}

// DefaultMap is what an unspecified map resolves to.
//
// game-manager/main.go defaults to "Charon", which is one of the invented names
// above and has no package path, so an instance launched through that default
// loads nothing. Defaulting to a real map is the single most useful difference
// between this tool and that code path.
const DefaultMap = "Highlands"

// DefaultGameMode matches matchmaker.DefaultGameMode.
const DefaultGameMode = "TDM"

// Maps returns every known map, multiplayer first, then PvE.
func Maps() []Map {
	out := make([]Map, 0, len(multiplayerMaps)+len(pveMaps))
	out = append(out, multiplayerMaps...)
	out = append(out, pveMaps...)
	return out
}

// LookupMap resolves a map by name, case-insensitively. It also accepts a raw
// package path (anything starting with "/Game/"), which lets an operator run a
// map this table does not list -- night variants and Havoc maps do exist in the
// paks -- without having to invent a name for it here.
func LookupMap(name string) (Map, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return LookupMap(DefaultMap)
	}
	if strings.HasPrefix(trimmed, "/Game/") {
		// Derive a display name from the leaf, so "/Game/Maps/MP/Gorge/MP_Gorge_P"
		// reports as "MP_Gorge_P" rather than the whole path.
		leaf := trimmed
		if i := strings.LastIndex(leaf, "/"); i >= 0 {
			leaf = leaf[i+1:]
		}
		return Map{Name: leaf, Path: trimmed}, nil
	}
	for _, m := range Maps() {
		if strings.EqualFold(m.Name, trimmed) {
			return m, nil
		}
	}
	return Map{}, fmt.Errorf("unknown map %q (known: %s; or pass a full /Game/... package path)",
		name, strings.Join(MapNames(), ", "))
}

// MapNames lists every known map name in table order.
func MapNames() []string {
	names := make([]string, 0, len(multiplayerMaps)+len(pveMaps))
	for _, m := range Maps() {
		names = append(names, m.Name)
	}
	return names
}

// gameModes are the client-facing mode names, from matchmaker's
// clientGameModeConfigs. TeamSize is carried for display only -- the engine is
// told the mode by name.
var gameModes = []struct {
	Name     string
	TeamSize int
}{
	{"TDM", 5},
	{"PodTDM", 5},
	{"TE", 5},
	{"TM", 5},
	{"TER", 5},
	{"Territory", 5},
	{"Onslaught", 5},
	{"BC", 1},
	{"Bootcamp", 1},
	{"TurboTDM", 5},
}

// gameModeAliases maps the longer legacy server names onto the client config
// names, matching matchmaker.NormalizeGameMode so both accept the same input.
var gameModeAliases = map[string]string{
	"TeamDeathMatch":   "TDM",
	"TeamDeathmatch":   "TDM",
	"TeamElimination":  "TE",
	"TerritoryControl": "TER",
	"BootCamp":         "BC",
	"HAVOC":            "Onslaught",
	"PvE_Standard":     "Onslaught",
	"PvE_Havoc":        "Onslaught",
	"PvE_Onslaught":    "Onslaught",
	"PvE_Coop":         "Onslaught",
	"Training":         "BC",
}

// NormalizeGameMode resolves an alias to its client config name and validates
// the result. Matching is case-insensitive on both the alias table and the mode
// list, which is slightly more permissive than matchmaker.NormalizeGameMode
// (exact-match only) -- a superset, so anything mmogbrain sends still resolves
// to the same value.
func NormalizeGameMode(mode string) (string, error) {
	trimmed := strings.TrimSpace(mode)
	if trimmed == "" {
		return DefaultGameMode, nil
	}
	for alias, canonical := range gameModeAliases {
		if strings.EqualFold(alias, trimmed) {
			return canonical, nil
		}
	}
	for _, m := range gameModes {
		if strings.EqualFold(m.Name, trimmed) {
			return m.Name, nil
		}
	}
	return "", fmt.Errorf("unknown game mode %q (known: %s)", mode, strings.Join(GameModeNames(), ", "))
}

// GameModeNames lists the client config mode names in table order.
func GameModeNames() []string {
	names := make([]string, 0, len(gameModes))
	for _, m := range gameModes {
		names = append(names, m.Name)
	}
	return names
}

// GameModeAliases returns the alias table as sorted "alias -> canonical" pairs,
// for the `modes` command's output.
func GameModeAliases() [][2]string {
	out := make([][2]string, 0, len(gameModeAliases))
	for alias, canonical := range gameModeAliases {
		out = append(out, [2]string{alias, canonical})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// TeamSize returns the configured team size for a canonical mode name.
func TeamSize(mode string) int {
	for _, m := range gameModes {
		if strings.EqualFold(m.Name, mode) {
			return m.TeamSize
		}
	}
	return 0
}
