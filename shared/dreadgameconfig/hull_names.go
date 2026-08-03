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
	// Description is the blueprint's own prose. Empty for the eight hulls whose
	// blueprint has no description field.
	Description string `json:"description"`
	Class       string `json:"class"`
	Size        string `json:"size"`
	Tier        int    `json:"tier"`
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

// HullDescriptionForItemID returns the blueprint description for a precast
// loadout id.
//
// Eight hulls have no description in their blueprint at all -- the field is
// absent and the generator records that rather than substituting the subline
// that sits next to it -- so a false second return is "the game has none", not
// "we failed to look it up".
func HullDescriptionForItemID(itemID int32) (string, bool) {
	ensureHullDescriptionIndex()
	nameCacheMu.Lock()
	defer nameCacheMu.Unlock()
	description, ok := hullDescriptionByID[itemID]
	return description, ok && description != ""
}

// ensureHullDescriptionIndex builds the id -> description index if it is
// missing, on the same rebuild-while-empty rule as the name index and for the
// same reason: it needs the item catalog, which is not necessarily loaded when
// this package is first touched.
func ensureHullDescriptionIndex() {
	nameCacheMu.Lock()
	defer nameCacheMu.Unlock()
	if len(hullDescriptionByID) > 0 {
		return
	}
	hulls := GetAllHullNames()
	if len(hulls) == 0 {
		return
	}
	byAsset := make(map[string]string, len(hulls))
	for _, hull := range hulls {
		if hull.Description != "" {
			byAsset[hull.Asset] = hull.Description
		}
	}
	index := map[int32]string{}
	for _, category := range GetAllCategories() {
		if category.CategoryName != "YShipLoadoutPrecast" {
			continue
		}
		for _, itemID := range category.ItemIDs {
			item, ok := ItemByID(itemID)
			if !ok {
				continue
			}
			if description, ok := byAsset[item.AssetPath]; ok {
				index[itemID] = description
			}
		}
	}
	hullDescriptionByID = index
}
