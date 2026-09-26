package main

import (
	"os"
	"strconv"
	"strings"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
	"github.com/sirupsen/logrus"
)

// Loadout edits were stored without checking ownership (audit 2026-09-26):
// persistUpdateShipLoadout wrote whatever weapon/module/perk ids and appearance
// string the client sent for its own loadout. The stock client only offers what
// the player owns, but a modified client or a hand-built request could fit
// anything -- and the battle server spawns the stored fit (/battle/loadout), so
// that is unowned weapons in a match and unpaid cosmetics.
//
// DN_LOADOUT_OWNERSHIP:
//
//	warn (default)  store as before, log every item the player does not own
//	enforce         additionally skip the unowned slot / appearance change
//	off             no check
//
// warn first, because a FALSE positive in enforce mode silently loses a real
// player's fit -- ownership has corners (per-ship ids, hero hulls, fitted
// defaults) -- and that is worse than the abuse it prevents. Switch to enforce
// once live logs show no "loadout: unowned" lines for honest players.
func loadoutOwnershipMode() string {
	switch os.Getenv("DN_LOADOUT_OWNERSHIP") {
	case "enforce":
		return "enforce"
	case "off":
		return "off"
	default:
		return "warn"
	}
}

// ownedItemSet is what the player owns for fitting purposes: purchases
// (cosmetics included) plus every owned ship's fitted defaults, the same set
// the client is told (clientOwnedItemIDs).
//
// Weapons, abilities and perks are keyed by their SHARED form. Real stored fits
// use both forms -- per-ship ids (middle byte = EYShipClass, see inflatedItemID)
// and shared ones (middle byte 0xFF) -- even within one account, while the
// owned list holds per-ship ids for fitted defaults and shared ids for the
// starter kit. Comparing shared forms means owning a weapon on one hull
// permits it on another: slightly lenient, but it still catches the abuse
// that matters (a weapon never obtained at all) without flagging honest fits.
func ownedItemSet(playerPID string) map[int32]bool {
	set := map[int32]bool{}
	add := func(id int32) {
		set[id] = true
		set[sharedGearID(id)] = true
	}
	for _, id := range clientOwnedItemIDs(playerPID) {
		add(id)
	}
	for _, item := range starterOwnedInventorySeeds() {
		add(item.itemID)
	}
	return set
}

// sharedGearID maps a per-ship id to its shared form (middle byte 0xFF):
// weapons, abilities and perks (4-6), and SHIP cosmetics (20-24) -- the client
// stores a ship's appearance with per-ship ids too (e.g. 336461851 =
// VAN_H_AssaultM_Hull_Default_DA 352256027 with class byte 0x0E, from a real
// saved fit). Captain items and everything else are returned unchanged.
func sharedGearID(id int32) int32 {
	switch (id >> 24) & 0xff {
	case 4, 5, 6, 20, 21, 22, 23, 24:
		return id | 0x00FF0000
	}
	return id
}

// unownedAppearanceItems lists the cosmetic ids in a ship's display info
// ("m#m#m#m;emblem;paint;pattern;decal") that the player neither owns nor has
// by default. -1 and 0 are empty slots.
func unownedAppearanceItems(owned map[int32]bool, displayInfo string) []int32 {
	defaults := dreadconfig.AllDefaultShipVanityItemIDs()
	var out []int32
	for _, group := range strings.Split(displayInfo, ";") {
		for _, field := range strings.Split(group, "#") {
			v, err := strconv.ParseInt(strings.TrimSpace(field), 10, 32)
			if err != nil || v <= 0 {
				continue
			}
			id := int32(v)
			if !owned[id] && !owned[sharedGearID(id)] && !defaults[id] && !defaults[sharedGearID(id)] {
				out = append(out, id)
			}
		}
	}
	return out
}

// allowLoadoutItem reports whether a slot may be set to itemID, logging when
// the player does not own it.
func allowLoadoutItem(mode string, owned map[int32]bool, playerPID string, loadoutID int32, slot string, itemID int32) bool {
	if mode == "off" || itemID <= 0 || owned[itemID] || owned[sharedGearID(itemID)] {
		return true
	}
	logrus.WithFields(logrus.Fields{"player": playerPID, "loadout": loadoutID, "slot": slot,
		"item_id": itemID, "mode": mode}).Warn("loadout: unowned item fitted")
	return mode != "enforce"
}

// allowAppearance is the same check for the ship's display info.
func allowAppearance(mode string, owned map[int32]bool, playerPID string, loadoutID int32, displayInfo string) bool {
	if mode == "off" {
		return true
	}
	unowned := unownedAppearanceItems(owned, displayInfo)
	if len(unowned) == 0 {
		return true
	}
	logrus.WithFields(logrus.Fields{"player": playerPID, "loadout": loadoutID,
		"unowned": unowned, "mode": mode}).Warn("loadout: unowned cosmetic in ship appearance")
	return mode != "enforce"
}
