package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
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
	hullIDByName     map[string]int32
	hullTierByName   map[string]int
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

// CanonicalPrecastLoadoutID returns the id the GAME sells for a hull.
//
// A hull has two live precast ids. One points at the current asset,
// /Loadouts/Precast/T<n>/VH_<Class><Size>_T<n>_PrecastLoadout_BP; the other at
// the previous build's, /Loadouts/Precast/VH_<Class><Size>_PrecastLoadout_BP.
// Both are registered, both resolve to the same ship name, and nothing about
// either looks wrong in isolation:
//
//	Athos  33489300  /Precast/T5/VH_AssaultMedium_T5_PrecastLoadout_BP   <- SKU
//	Athos  33489315  /Precast/VH_AssaultMedium_PrecastLoadout_BP         <- legacy
//
// The shipped CatalogIDTable settles which one is real: its 49 category-1 SKUs
// are 33489265..33489312, i.e. every TIERED id from T2 up, and not one of the
// fifteen tier-less ids (33489313..33489331) appears in any bucket. The legacy
// id was never purchasable.
//
// Serving the legacy id is not cosmetic. It disagrees with the tech tree, which
// keys a ship's modules on its own id, and it carries no description, because
// the description index joins on the asset path and the legacy path is not one
// of the 52 the blueprints describe -- the client renders that as
// "<sku><DNT> Invalid Description Field in Json".
//
// Returns the input unchanged when there is nothing to substitute, so it is
// safe to call on any id.
func CanonicalPrecastLoadoutID(itemID int32) int32 {
	if (itemID>>24)&0xff != categoryIDShipLoadoutPrecast {
		return itemID
	}
	item, ok := ItemByID(itemID)
	if !ok {
		return itemID
	}
	if _, current := hullNameForAsset(item.AssetPath); current {
		return itemID // already the asset the blueprints describe
	}
	name, ok := AuthoritativeItemName(itemID)
	if !ok {
		return itemID
	}
	ensureHullIDIndex()
	nameCacheMu.Lock()
	defer nameCacheMu.Unlock()
	if canonical, ok := hullIDByName[name]; ok {
		return canonical
	}
	return itemID
}

// ensureHullIDIndex builds the hull name -> current item id index.
//
// Only assets HullNames knows are indexed, which is exactly the 52 player hulls
// in the tiered directories, and an ambiguous name is dropped rather than
// resolved arbitrarily -- the same rule as ensureHullTierIndex, for the same
// reason.
func ensureHullIDIndex() {
	nameCacheMu.Lock()
	defer nameCacheMu.Unlock()
	if len(hullIDByName) > 0 {
		return
	}
	if len(GetAllHullNames()) == 0 {
		return
	}
	counts := map[string]int{}
	candidate := map[string]int32{}
	for _, category := range GetAllCategories() {
		if category.CategoryName != "YShipLoadoutPrecast" {
			continue
		}
		for _, itemID := range category.ItemIDs {
			item, ok := ItemByID(itemID)
			if !ok {
				continue
			}
			name, ok := hullNameForAsset(item.AssetPath)
			if !ok {
				continue
			}
			counts[name]++
			candidate[name] = itemID
		}
	}
	index := make(map[string]int32, len(candidate))
	for name, itemID := range candidate {
		if counts[name] == 1 {
			index[name] = itemID
		}
	}
	hullIDByName = index
}

// HullTierForItemID returns the tier of a precast hull, 1..5.
//
// The asset path is normally the game's own statement of a tier
// (/Loadouts/Precast/T<n>/...), and where it carries one it is used directly.
// The trap is that ItemIDRegister maps a handful of ids to the PREVIOUS build's
// tier-less path -- the same "the table describes an older build" problem that
// gave four hulls the wrong NAME (see the note at the top of this file). Those
// ids resolve to /Loadouts/Precast/VH_<Class><Size>_PrecastLoadout_BP with no
// tier segment at all, and reading a tier off that path silently yields the
// fallback:
//
//	Athos  33489315  registered /Precast/VH_AssaultMedium_PrecastLoadout_BP     really T5
//	Zmey   33489318  registered /Precast/VH_DreadnoughtMedium_PrecastLoadout_BP really T5
//	Aion   33489331  registered /Precast/VH_SupportMedium_PrecastLoadout_BP     really T4
//
// All three went out of the market as Tier 1, at the Tier 1 price.
//
// So the name is the join of last resort: the blueprint the client loads names
// the hull AND sits in the tiered directory, and the 52 player hull names are
// distinct (asserted by a test), so a name identifies exactly one tier. The
// caller must only ask about precast-loadout ids -- a name match is meaningless
// for anything else, and display names are not unique across categories.
func HullTierForItemID(itemID int32) (int, bool) {
	if (itemID>>24)&0xff != categoryIDShipLoadoutPrecast {
		return 0, false
	}
	if item, ok := ItemByID(itemID); ok {
		if match := hullAssetTierPattern.FindStringSubmatch(item.AssetPath); match != nil {
			if tier, err := strconv.Atoi(match[1]); err == nil && tier >= 1 && tier <= 5 {
				return tier, true
			}
		}
	}
	name, ok := AuthoritativeItemName(itemID)
	if !ok {
		return 0, false
	}
	ensureHullTierIndex()
	nameCacheMu.Lock()
	defer nameCacheMu.Unlock()
	tier, ok := hullTierByName[name]
	return tier, ok
}

// categoryIDShipLoadoutPrecast is YShipLoadoutPrecast's ItemIDTable CategoryID,
// which by the category law is the top byte of every id in it.
const categoryIDShipLoadoutPrecast = 1

var hullAssetTierPattern = regexp.MustCompile(`/Loadouts/Precast/T(\d)/`)

// ensureHullTierIndex builds the name -> tier index over the player hulls.
//
// Names that are not unique are dropped rather than resolved arbitrarily. There
// are none today (all 52 differ, and TestHullNamesAreUniqueEnoughToJoinOnTier
// keeps it that way), so this only guards against a future asset change turning
// a silent wrong answer into no answer.
func ensureHullTierIndex() {
	nameCacheMu.Lock()
	defer nameCacheMu.Unlock()
	if len(hullTierByName) > 0 {
		return
	}
	hulls := GetAllHullNames()
	if len(hulls) == 0 {
		return
	}
	counts := make(map[string]int, len(hulls))
	for _, hull := range hulls {
		counts[hull.Name]++
	}
	index := make(map[string]int, len(hulls))
	for _, hull := range hulls {
		if hull.Name == "" || hull.Tier < 1 || counts[hull.Name] != 1 {
			continue
		}
		index[hull.Name] = hull.Tier
	}
	hullTierByName = index
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
