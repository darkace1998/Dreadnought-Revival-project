package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Hull display names read out of the client's own precast loadout blueprints.
//
// ItemIDConversionTable is the naming authority everywhere else in this package
// and mostly earns it, but it is an OldItemID -> NewItemID translation and its
// Name column carries the name from the OLDER build. For a hull that was
// renamed between builds you therefore get the current id paired with the
// legacy name -- and because the table is internally consistent, an audit
// against the table cannot surface it. Four hulls are affected:
//
//	ScoutLight  T3   table: Lerwick   client: Machias
//	ScoutLight  T5   table: Bakar     client: Nevis
//	AssaultHeavy T3  table: Kama      client: Dola
//	ScoutHeavy  T4   table: Perun     client: Stribog
//
// Those four were cross-checked against a live client by the client-side half of
// this project (AGENT-CHAT.md, C5) before this file existed, and the mechanism
// they predicted is exactly what the assets show.
//
// The blueprint is what the client actually loads, and it carries its own
// display name, so it wins. scripts/gen-hull-names.py extracts them; it refuses
// to write the file unless every hull's subclass matches the class in its own
// filename, so a change in the asset layout fails loudly instead of quietly
// producing plausible nonsense.
//
// This deliberately covers PLAYER HULLS ONLY -- the 52 assets under
// /Game/Generic/Loadouts/Precast/T1..T5. Hero loadouts have the same shape but a
// messier relationship with the table (35 of 48 have no row at all, and several
// asset names carry variant suffixes like "Huscarl - Vintage" where the table
// has the base name), so they are left alone rather than overridden on a weaker
// basis.
type HullName struct {
	Asset    string `json:"asset"`
	Name     string `json:"name"`
	Subclass string `json:"subclass"`
	Class    string `json:"class"`
	Size     string `json:"size"`
	Tier     int    `json:"tier"`
}

var (
	hullNames        []HullName
	hullNameByAsset  map[string]string
	hullNamesOnce    sync.Once
	hullNamesLoadErr error
)

// LoadHullNames reads data/assets/HullNames.json.
//
// A missing file is NOT an error: the server ran without it for a long time and
// still resolves every other name through ItemIDConversionTable. It only means
// the four renamed hulls keep their legacy names, which is the behaviour that
// was there before. A malformed file is an error, because that is a mistake
// rather than an absence.
func LoadHullNames() error {
	hullNamesOnce.Do(func() {
		data, err := os.ReadFile(AssetPath("HullNames.json"))
		if err != nil {
			if !os.IsNotExist(err) {
				hullNamesLoadErr = fmt.Errorf("read HullNames: %w", err)
			}
			return
		}
		var parsed struct {
			Hulls []HullName `json:"hulls"`
		}
		if err := json.Unmarshal(data, &parsed); err != nil {
			hullNamesLoadErr = fmt.Errorf("parse HullNames: %w", err)
			return
		}
		byAsset := make(map[string]string, len(parsed.Hulls))
		for _, hull := range parsed.Hulls {
			if hull.Asset == "" || hull.Name == "" {
				continue
			}
			byAsset[hull.Asset] = hull.Name
		}
		hullNames = parsed.Hulls
		hullNameByAsset = byAsset
	})
	return hullNamesLoadErr
}

// GetAllHullNames returns the loaded hull entries.
func GetAllHullNames() []HullName {
	_ = LoadHullNames()
	return append([]HullName(nil), hullNames...)
}

// hullNameForAsset returns the blueprint-derived display name for a precast
// loadout asset path, if there is one.
func hullNameForAsset(assetPath string) (string, bool) {
	_ = LoadHullNames()
	name, ok := hullNameByAsset[assetPath]
	return name, ok
}
