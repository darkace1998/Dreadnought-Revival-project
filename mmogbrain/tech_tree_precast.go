package main

import (
	"regexp"
	"strconv"

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
// The derivation itself now lives in shared/dreadgameconfig
// (authoritative_names.go), so legacy-api and the catalog resolve names and
// loadout ids the same way; these stay as the names the rest of this package
// uses.
var shipAssetPathPattern = regexp.MustCompile(`^/Game/Generic/Ships/([A-Za-z]+)/([A-Za-z]+)/T(\d)/`)

// techTreePrecastLoadoutID returns the precast-loadout id that represents a ship
// in the tech tree, deriving it from the two assets' paths.
func techTreePrecastLoadoutID(shipID int32) (int32, bool) {
	return dreadconfig.PrecastLoadoutIDForShip(shipID)
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

// authoritativeItemName / authoritativeShipName resolve an item's real display
// name through the client's ItemIDConversionTable. See
// shared/dreadgameconfig/authoritative_names.go for why the hardcoded names
// could not be trusted.
func authoritativeItemName(itemID int32) (string, bool) {
	return dreadconfig.AuthoritativeItemName(itemID)
}

func authoritativeShipName(shipID int32) (string, bool) {
	return dreadconfig.AuthoritativeShipName(shipID)
}

// shipDisplayName is the name to show for a ship, preferring the authoritative
// one and falling back to the seed's own value.
func shipDisplayName(ship mmogShipSeed) string {
	if name, ok := authoritativeShipName(ship.id); ok {
		return name
	}
	return ship.name
}

// derivedShipTier returns the tier encoded in a ship's registered asset path
// (/Ships/<Class>/<Size>/T<n>/), which is the game's own statement of it.
func derivedShipTier(shipID int32) (int, bool) {
	item, ok := dreadconfig.ItemByID(shipID)
	if !ok {
		return 0, false
	}
	match := shipAssetPathPattern.FindStringSubmatch(item.AssetPath)
	if match == nil {
		return 0, false
	}
	tier, err := strconv.Atoi(match[3])
	if err != nil {
		return 0, false
	}
	return tier, true
}
