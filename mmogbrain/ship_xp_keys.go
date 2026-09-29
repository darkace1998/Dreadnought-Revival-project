package main

import "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"

// Ship XP has two id spaces, and they must not be mixed.
//
// STORED by pawn (ship item) id, category 10 (0x0AFF....): what battle results
// resolve a flown loadout to (flownShipIDs) and what player_ship_xp keys.
//
// READ BY THE CLIENT by hull LOADOUT id, category 1 (0x01FF....), the tech
// tree's hull ids. Verified from the binary 2026-09-29:
// YCtATechTreeInterface::CanResearchItem (0x31E880) takes the item's record
// from the tech tree (0x3F51A0), then compares the ship XP returned by
// 0x542130 -> 0x3FC410 -- a linear search of the player's {ShipID, ShipXp}
// list for the record's +0x28 -- with the XP cost. +0x28 is ClassId (the
// loader 0x3FFDE0 stores Id at +0x20, ClassId at +0x28), and ClassId is the
// hull loadout id: a module's is its hull (appendMmogTechTree research items),
// a tier-2+ hull's is its parent hull (techTreeHullClassID).
//
// FIXED 2026-09-29: ShipXps went out keyed by pawn id, every lookup missed and
// ship XP counted as 0, so research was possible only for items costing
// nothing -- tier-1 ships played, tier-2 hulls stayed "research requirements
// not met" (operator). Only an account with free XP to convert got through.

// hullLoadoutsForPawn is every base hull loadout (tech-tree hull id) built on
// the pawn: the client's ship-XP keys for XP stored under that pawn.
func hullLoadoutsForPawn(pawn int32) []int32 {
	var out []int32
	for _, hull := range baseShipLoadouts {
		if id, ok := dreadgameconfig.ShipIDForPrecastLoadout(hull.loadoutID); ok && id == pawn {
			out = append(out, hull.loadoutID)
		}
	}
	return out
}

// clientShipXPs is the ShipXps list as the client keys it: each stored pawn's
// XP under every hull loadout id of that pawn. The pawn-keyed entry is kept
// too; nothing reads it that we know of, and it is what earlier builds sent.
func clientShipXPs(stored []shipXPEntry) []shipXPEntry {
	out := make([]shipXPEntry, 0, len(stored)*2)
	for _, e := range stored {
		for _, loadout := range hullLoadoutsForPawn(e.shipID) {
			out = append(out, shipXPEntry{shipID: loadout, xp: e.xp})
		}
		out = append(out, e)
	}
	return out
}

// researchShip is the ship whose XP pays for researching itemID: the ship the
// client charges (its tech-tree ClassId, a hull loadout id -- what the
// YA_UnlockItem reply's result.ShipID must name) and the pawn it is stored
// under. Per-ship weapons/modules: their hull. Hulls: their parent hull. Line
// roots and anything else: none.
func researchShip(itemID int32) (clientKey, pawn int32, ok bool) {
	if loadout, found := researchHullLoadout(itemID); found {
		clientKey = loadout
	} else if parent, found := techTreeHullParents[itemID]; found && parent != 0 && parent != itemID {
		clientKey = parent
	} else {
		return 0, 0, false
	}
	pawn, ok = dreadgameconfig.ShipIDForPrecastLoadout(clientKey)
	return clientKey, pawn, ok
}

// researchHullLoadout is the base hull (loadout id) whose research list a
// per-ship weapon/module is on; see researchHullPawn.
func researchHullLoadout(itemID int32) (int32, bool) {
	row, ok := perShipResearchRow(itemID)
	if !ok {
		return 0, false
	}
	class := (itemID >> 16) & 0xff
	for _, hull := range baseShipLoadouts {
		if hull.tier == row.Tier && eyShipClassByKey[hull.hullLine] == class {
			return hull.loadoutID, true
		}
	}
	return 0, false
}
