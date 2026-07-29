package main

import (
	"regexp"
	"sync"

	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
)

// The tech tree's ship rows are keyed by PRECAST LOADOUT id, not by ship-pawn id.
//
// The gate deciding whether a row enters the array TechTreeManager::
// FindItemForShipId searches compares the top byte of the id -- a category tag,
// extracted by FUN_1402cf640 as (Id >> 24) & 0xff -- against the resolved
// YShipLoadoutHero and YShipLoadoutPrecast classes. ItemIDTable gives those
// categories IDs 3 and 1, so only ids whose top byte is 3 or 1 are admitted.
// YPawn is category 10, so every pawn id is rejected. That is why the client
// asks for 33489262 (top byte 1) and never for 184483982 (top byte 10).
//
// Rather than hand-write the ship -> loadout table, both halves are derived from
// the registered asset paths, which encode class, size and tier:
//
//	ship:    /Game/Generic/Ships/Assault/Medium/T1/VH_AssaultM_Pawn_T1_BP
//	loadout: /Game/Generic/Loadouts/Precast/T1/VH_AssaultMedium_T1_PrecastLoadout_BP
//
// A hand-written table is what produced the Development-blueprint mess this
// replaces, so the derivation is validated instead: it reproduces all four
// starter loadout ids that were already known independently (33489262, 33489423,
// 33489263, 33489264) and fills in the six T2 ships that had none.
var (
	shipAssetPathPattern    = regexp.MustCompile(`^/Game/Generic/Ships/([A-Za-z]+)/([A-Za-z]+)/T(\d)/`)
	precastAssetPathPattern = regexp.MustCompile(`^/Game/Generic/Loadouts/Precast/(?:T(\d)/)?VH_([A-Za-z]+)_(?:T(\d)_)?PrecastLoadout_BP$`)

	precastLoadoutOnce  sync.Once
	precastLoadoutByKey map[string]int32
)

// buildPrecastLoadoutIndex indexes the player-facing precast loadouts by
// "<Class><Size>|<tier>". It deliberately matches only loadouts sitting directly
// under /Loadouts/Precast/, which excludes the Havoc, AI-boss and Development
// variants that share the category.
func buildPrecastLoadoutIndex() {
	precastLoadoutByKey = map[string]int32{}
	for _, category := range dreadconfig.GetAllCategories() {
		if category.CategoryName != "YShipLoadoutPrecast" {
			continue
		}
		for _, itemID := range category.ItemIDs {
			item, ok := dreadconfig.ItemByID(itemID)
			if !ok {
				continue
			}
			match := precastAssetPathPattern.FindStringSubmatch(item.AssetPath)
			if match == nil {
				continue
			}
			tier := match[1]
			if tier == "" {
				tier = match[3]
			}
			precastLoadoutByKey[match[2]+"|"+tier] = itemID
		}
	}
}

// techTreePrecastLoadoutID returns the precast-loadout id that represents a ship
// in the tech tree, deriving it from the two assets' paths.
func techTreePrecastLoadoutID(shipID int32) (int32, bool) {
	precastLoadoutOnce.Do(buildPrecastLoadoutIndex)

	item, ok := dreadconfig.ItemByID(shipID)
	if !ok {
		return 0, false
	}
	match := shipAssetPathPattern.FindStringSubmatch(item.AssetPath)
	if match == nil {
		return 0, false
	}
	id, ok := precastLoadoutByKey[match[1]+match[2]+"|"+match[3]]
	return id, ok
}

// eyShipClassByKey is EYShipClass from the SDK, keyed by "<Class><Size>" as it
// appears in a ship's asset path.
var eyShipClassByKey = map[string]int32{
	"DreadnoughtLight": 1, "ScoutLight": 2, "SniperLight": 3, "SupportLight": 4, "AssaultLight": 5,
	"DreadnoughtMedium": 6, "DreadnoughtHeavy": 7, "ScoutMedium": 8, "ScoutHeavy": 9,
	"SniperMedium": 10, "SniperHeavy": 11, "SupportMedium": 12, "SupportHeavy": 13,
	"AssaultMedium": 14, "AssaultHeavy": 15,
}

// derivedShipClassID returns a ship's EYShipClass from its registered asset
// path, which encodes class and size.
//
// This exists because the hand-written classID values in the ship seeds had
// drifted: Sniper Light T2 (184483954) carried 10 (SNIPER_MEDIUM) instead of 3
// (SNIPER_LIGHT). Validating every seed against its asset path found that one
// and nothing else, and deriving it keeps the two from diverging again.
func derivedShipClassID(shipID int32) (int32, bool) {
	item, ok := dreadconfig.ItemByID(shipID)
	if !ok {
		return 0, false
	}
	match := shipAssetPathPattern.FindStringSubmatch(item.AssetPath)
	if match == nil {
		return 0, false
	}
	id, ok := eyShipClassByKey[match[1]+match[2]]
	return id, ok
}

// techTreeRowClassID is the ClassId a tech tree row reports: derived from the
// ship's asset path where possible, and otherwise the seed's own value.
func techTreeRowClassID(ship mmogShipSeed) int32 {
	if id, ok := derivedShipClassID(ship.id); ok {
		return id
	}
	return ship.classID
}

// techTreeRowID is the id a tech tree row is keyed on: the ship's precast
// loadout id where one can be derived, and otherwise the id as given -- which
// covers the fleet rows that are already precast loadout ids.
func techTreeRowID(ship mmogShipSeed) int32 {
	if id, ok := techTreePrecastLoadoutID(ship.id); ok {
		return id
	}
	return ship.id
}
