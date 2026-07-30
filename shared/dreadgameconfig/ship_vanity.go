package dreadgameconfig

import (
	"regexp"
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
// Only the three slots below are filled, because only those have an
// unambiguous default in the client's data:
//
//   - emblem and decal each have exactly one "_Default_DA" asset, shared by
//     every ship.
//   - pattern has one per hull line, /Patterns/<Class>/<Size>/VAN_PTN_*_Default_DA.
//
// Paint and the mesh parts are deliberately left at -1. There is no
// "_Default_DA" paint except VAN_PN_JupA_Default_DA, which is Jupiter Arms
// specific with no Akula or Oberon counterpart, so picking one for the other two
// makers would be a guess. The mesh parts are called m_heroShipParts and every
// candidate asset lives under /VanityItems/Heroships/, so they most likely do
// not belong on a standard hull at all.
const (
	sharedVanityDefaultEmblemPath = "/Game/Generic/VanityItems/_Shared/Emblems/Default/VAN_EMB_Default_DA"
	sharedVanityDefaultDecalPath  = "/Game/Generic/VanityItems/_Shared/Decal/VAN_DCL_Default_DA"

	// VanityUnsetSlot is what an unfilled slot carries on the wire.
	VanityUnsetSlot = "-1"
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

var (
	vanityOnce             sync.Once
	defaultPatternByHull   map[string]int32
	defaultEmblemItemID    int32
	defaultDecalItemID     int32
	vanityCategoryItemName = map[string]string{
		"YShipVanityEmblem":  sharedVanityDefaultEmblemPath,
		"YShipVanityDecal":   sharedVanityDefaultDecalPath,
		"YShipVanityPattern": "",
	}
)

func buildDefaultShipVanity() {
	defaultPatternByHull = map[string]int32{}
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
	mesh := strings.Join([]string{VanityUnsetSlot, VanityUnsetSlot, VanityUnsetSlot, VanityUnsetSlot}, "#")
	return strings.Join([]string{
		mesh,
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
