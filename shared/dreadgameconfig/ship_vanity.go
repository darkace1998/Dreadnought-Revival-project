package dreadgameconfig

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// A ship's appearance travels in one string, "m_displayInfo", shaped as
//
//	mesh#mesh#mesh#mesh;emblem;paint;pattern;decal
//
// The five groups are the five members of FYShipAppeareaceContainer, in
// declaration order (the game's own spelling): m_heroShipParts (a TArray, hence
// the '#'-separated group), m_emblem, m_paint, m_pattern, m_decal. That order is
// also the ItemIDTable category order -- YShipVanityMeshPart 20, Emblem 21,
// Paint 22, Pattern 23, Decal 24.
//
// The server used to send -1 in all eight slots. The client does not treat -1 as
// "nothing"; it takes every slot as an item id and tries to async-load it, which
// is where eight of these came from per ship:
//
//	UYItemIDList::LoadItemsAsync | Asset with ID -1 has no valid FStringReference
//
// Seven of the eight slots are filled, each from a default the client's own data
// states unambiguously:
//
//   - emblem and decal each have exactly one "_Default_DA" asset, shared by
//     every ship.
//   - pattern has one per hull line, /Patterns/<Class>/<Size>/VAN_PTN_*_Default_DA.
//
// The four mesh slots are filled too. They looked hero-only at first -- the
// member is m_heroShipParts and every candidate asset sits under
// /VanityItems/Heroships/ -- but the client's own
// YUI::Util::GetCategoryImagePath switches slot types 4,5,6,7 to
// UI_Category_APPEARANCE_HULL, an ordinary slot every ship has, and each of the
// 15 hull lines has exactly four "_Default_DA" parts. The path is just where the
// art lives. Slots are filled in item-id order; all four are that hull's
// defaults, so a permutation would look the same either way.
//
// Only paint stays at -1. The one "_Default_DA" paint is VAN_PN_JupA_Default_DA,
// Jupiter Arms specific with no Akula or Oberon counterpart, so choosing for the
// other two makers would be a guess rather than a derivation.
const (
	sharedVanityDefaultEmblemPath = "/Game/Generic/VanityItems/_Shared/Emblems/Default/VAN_EMB_Default_DA"
	sharedVanityDefaultDecalPath  = "/Game/Generic/VanityItems/_Shared/Decal/VAN_DCL_Default_DA"

	// VanityUnsetSlot is what an unfilled slot carries on the wire.
	VanityUnsetSlot = "-1"

	// meshSlotCount is the size the importer demands of the mesh group.
	meshSlotCount = 4
)

// patternDefaultPathPattern matches the per-hull default pattern assets. The
// directory spells Dreadnought "Dread", so the hull line is reassembled rather
// than taken from the path verbatim.
var patternDefaultPathPattern = regexp.MustCompile(
	`^/Game/Generic/VanityItems/Patterns/([A-Za-z]+)/([A-Za-z]+)/VAN_PTN_[A-Za-z]+_Default_DA$`)

var patternDirectoryClassNames = map[string]string{
	"Assault": "Assault", "Dread": "Dreadnought", "Scout": "Scout",
	"Sniper": "Sniper", "Support": "Support",
}

// meshDefaultPathPattern matches the per-hull default mesh parts. Part names
// vary across hulls (Bridge on some, Quarterdeck on others, and one asset spells
// it "Quaterdeck"), so the name is not parsed -- only the hull line is.
var meshDefaultPathPattern = regexp.MustCompile(
	`^/Game/Generic/VanityItems/Heroships/([A-Za-z]+)/([A-Za-z]+)/Default/VAN_H_[A-Za-z]+_[A-Za-z]+_Default_DA$`)

var (
	vanityOnce             sync.Once
	defaultMeshByHull      map[string][]int32
	defaultPatternByHull   map[string]int32
	defaultEmblemItemID    int32
	defaultDecalItemID     int32
	vanityCategoryItemName = map[string]string{
		"YShipVanityEmblem":   sharedVanityDefaultEmblemPath,
		"YShipVanityDecal":    sharedVanityDefaultDecalPath,
		"YShipVanityPattern":  "",
		"YShipVanityMeshPart": "",
	}
)

func buildDefaultShipVanity() {
	defaultPatternByHull = map[string]int32{}
	defaultMeshByHull = map[string][]int32{}
	for _, category := range GetAllCategories() {
		wanted, relevant := vanityCategoryItemName[category.CategoryName]
		if !relevant {
			continue
		}
		for _, itemID := range category.ItemIDs {
			item, ok := ItemByID(itemID)
			if !ok {
				continue
			}
			if wanted != "" {
				if item.AssetPath != wanted {
					continue
				}
				switch category.CategoryName {
				case "YShipVanityEmblem":
					defaultEmblemItemID = itemID
				case "YShipVanityDecal":
					defaultDecalItemID = itemID
				}
				continue
			}
			if category.CategoryName == "YShipVanityMeshPart" {
				match := meshDefaultPathPattern.FindStringSubmatch(item.AssetPath)
				if match == nil {
					continue
				}
				hull := match[1] + match[2]
				defaultMeshByHull[hull] = append(defaultMeshByHull[hull], itemID)
				continue
			}
			match := patternDefaultPathPattern.FindStringSubmatch(item.AssetPath)
			if match == nil {
				continue
			}
			class, known := patternDirectoryClassNames[match[1]]
			if !known {
				continue
			}
			defaultPatternByHull[class+match[2]] = itemID
		}
	}
	// Registry iteration order is not stable, and the slot a part lands in
	// depends on its position, so sort.
	for hull := range defaultMeshByHull {
		ids := defaultMeshByHull[hull]
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	}
}

// DefaultShipDisplayInfo returns the m_displayInfo string for a standard hull,
// filling the slots whose default the client's own data states unambiguously.
// An unknown hull line still yields a well-formed string, just with the pattern
// slot unset.
func DefaultShipDisplayInfo(hullLine string) string {
	vanityOnce.Do(buildDefaultShipVanity)

	slot := func(id int32) string {
		if id == 0 {
			return VanityUnsetSlot
		}
		return itoa(id)
	}
	mesh := make([]string, 0, meshSlotCount)
	for _, id := range defaultMeshByHull[hullLine] {
		mesh = append(mesh, itoa(id))
	}
	// The importer demands exactly four, so pad rather than emit a short group.
	for len(mesh) < meshSlotCount {
		mesh = append(mesh, VanityUnsetSlot)
	}
	return strings.Join([]string{
		strings.Join(mesh[:meshSlotCount], "#"),
		slot(defaultEmblemItemID),
		VanityUnsetSlot, // paint: no unambiguous default, see above
		slot(defaultPatternByHull[hullLine]),
		slot(defaultDecalItemID),
	}, ";")
}

// DefaultShipVanityItemIDs reports the ids DefaultShipDisplayInfo would use, for
// tests and for callers that need them separately.
func DefaultShipVanityItemIDs(hullLine string) (emblem, pattern, decal int32) {
	vanityOnce.Do(buildDefaultShipVanity)
	return defaultEmblemItemID, defaultPatternByHull[hullLine], defaultDecalItemID
}

// DefaultShipMeshPartIDs reports the hull's four default mesh parts, in the
// order they are sent.
func DefaultShipMeshPartIDs(hullLine string) []int32 {
	vanityOnce.Do(buildDefaultShipVanity)
	return defaultMeshByHull[hullLine]
}

func itoa(v int32) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var digits [12]byte
	i := len(digits)
	for v > 0 {
		i--
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
