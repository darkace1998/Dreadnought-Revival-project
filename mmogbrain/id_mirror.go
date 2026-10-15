package main

// id_mirror.go -- the DN_ID_MIRROR alignment mirror.
//
// Why: the equip gate's two client-side inputs are now verified to be
// (a) the +0x39E8 array, filled from the YA_PlayerGet "Items" inventory (or a
// YA_ClaimItem push), scanned by IsItemOwnedByPlayer (0x548860, stride 0x10,
// dword0 == id), and (b) the +0x3F90 IntArray, filled from PurchasesData,
// scanned by GetTechTreeItemState (0x543890) as its FIRST check -- exact
// int32 match, stride 4, hit ⇒ state 4. The hull record the gate queries
// carries the per-ship (INFLATED) id of a module -- middle byte = the hull's
// EYShipClass -- while the server grants (and stores) the SHARED form
// (middle byte 0xff). A gate query for the inflated form misses a shared
// grant.
//
// The mirror sends every ALREADY-GRANTED id in the other fields/forms too:
//
//   - the inflated form of every granted category-4/5 id, for each ship class
//     the player owns a hull of (exactly the forms the client's per-ship
//     records can query);
//   - the shared (de-inflated, 0xff) form of every fitted inflated id
//     (clientOwnedItemIDs already adds the inflated ones -- the mirror is the
//     reverse leg);
//   - the loadout id and prerequisite loadout ids (category 1/3) of every
//     owned hull -- GetTechTreeItemState's prerequisite loop must resolve
//     every prereq or the item is state 1, "Requirements Not Met".
//
// Only categories 4 (ability) and 5 (weapon) inflate (inflatedItemID,
// verified 2026-09-23 against the cooked blueprints: shared 0x04FF001E,
// inflated 0x040E001E); briefings/perks stay single-form and are NOT mirrored
// -- their real ownership path (officerBriefingsOwnedThroughShips) already
// runs in both lists. Nothing the player was NOT granted becomes owned: a
// test with a fresh player confirmed mirroring whole research trees would
// grant unbought modules and break progression.
//
// Deliberately over-mirrored: if the test cycle still fails, each variant is
// named dead by which mirror count line appears in the log.
//
// Size discipline: measured by TestOwnedItemListsFitTheRingWhenEverythingIsOwned
// (PurchasesData) and TestOwnedItemsCutFromPlayerGetArriveInThePush (the
// 28000-byte push budget) -- a ring failure fails the build, not the client.

import (
	"os"
	"strconv"
	"sync"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
	"github.com/sirupsen/logrus"
)

var (
	idMirrorOnce sync.Once
	idMirrorOn   bool
)

// idMirrorEnabled: DN_ID_MIRROR, default on. Turn off with DN_ID_MIRROR=0 to
// compare a cycle against the old single-form grants.
func idMirrorEnabled() bool {
	idMirrorOnce.Do(func() { idMirrorOn = os.Getenv("DN_ID_MIRROR") != "0" })
	return idMirrorOn
}

// deInflatedItemID is the shared form of a per-ship id: the middle byte back
// to 0xff. Verified rule: shared 0x04FF001E "Tempest Missiles T0"
// (ItemIDRegister) vs inflated 0x040E001E (the same module on an
// AssaultMedium, class 14). Applies to categories 4/5 only.
func deInflatedItemID(id int32) int32 {
	top := (id >> 24) & 0xff
	mid := (id >> 16) & 0xff
	if (top == 4 || top == 5) && mid >= 1 && mid <= 15 {
		return (id &^ (0xff << 16)) | (0xff << 16)
	}
	return id
}

// mirrorCounts is what the mirror added, for the per-variant counts the
// pre-registered outcome sheet reasons about (variants named dead by absence).
type mirrorCounts struct {
	hulls    int // owned hull + prerequisite loadout ids (category 1/3)
	inflated int // inflated forms of granted ability/weapon ids, per owned class
	fitted   int // shared forms of fitted inflated ids
}

func (c mirrorCounts) fields() logrus.Fields {
	return logrus.Fields{
		"hulls":       c.hulls,
		"inflated":    c.inflated,
		"fitted":      c.fitted,
		"total_added": c.hulls + c.inflated + c.fitted,
	}
}

// techTreeMirrorIDs derives the mirror id set for one player: ids the server
// has ALREADY granted, expressed in the forms the client's equip gate can
// query. Derived from the same hull data the TechTrees document is built
// from (baseShipLoadouts / techTreeHullParents), for the hulls the player
// owns. Briefings and perks are deliberately absent (see the file comment).
func techTreeMirrorIDs(playerPID string, fittedByLoadout map[int32]baseShipLoadout) ([]int32, mirrorCounts) {
	var out []int32
	seen := map[int32]bool{}
	var counts mirrorCounts
	add := func(id int32) {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	// The id forms the player has been granted (server stores the shared
	// form). Sets gate every mirror below: nothing ungranted becomes owned.
	grantedSet := map[int32]bool{}
	for _, id := range ownedPurchaseItemIDs(playerPID) {
		grantedSet[id] = true
	}

	state := mmogPlayerStateForPID(playerPID)
	ownedLoadouts := ownedShipLoadoutsForPlayerData(state, playerPID)
	// Same rule as clientOwnedItemIDs (0b3fb88): a hero's per-class
	// weapons/modules (categories 4/5) are research items of its base hull and
	// must not read as owned until that base hull is. Without this the fitted
	// leg below put them straight back (TestHeroFitOwnedOnClassOnlyWithItsBaseHull
	// fails with DN_ID_MIRROR on).
	heroLoadouts := map[int32]bool{}
	for _, h := range heroShipLoadouts {
		heroLoadouts[h.loadoutID] = true
	}
	ownedBaseHulls := map[string]bool{}
	for _, loadout := range ownedLoadouts {
		if hull, ok := fittedByLoadout[loadout.precastLoadoutID]; ok && !heroLoadouts[hull.loadoutID] {
			ownedBaseHulls[hull.hullLine+"/"+strconv.Itoa(int(hull.tier))] = true
		}
	}
	for _, loadout := range ownedLoadouts {
		hull, ok := fittedByLoadout[loadout.precastLoadoutID]
		if !ok {
			continue
		}
		heroWithoutBase := heroLoadouts[hull.loadoutID] && !ownedBaseHulls[hull.hullLine+"/"+strconv.Itoa(int(hull.tier))]
		// The hull itself and its prerequisite (category 1 loadout ids --
		// the exact ids the TechTrees hull nodes and their Prereq carry).
		add(hull.loadoutID)
		counts.hulls++
		if parent, ok := techTreeHullParents[hull.loadoutID]; ok {
			add(parent)
			counts.hulls++
		}
		class := eyShipClassByKey[hull.hullLine]
		if class <= 0 {
			continue
		}
		// Inflated leg, bounded to what this hull can query: the rows of the
		// hull's own research list (per-ship ids already -- ShipResearchItems
		// is the client's module-preview table), mirrored only when their
		// shared form is one the player actually owns. Secondary weapons and
		// modules are shared per ship CLASS (and their tier rows shared
		// within it), so this is ~one tree per hull, not one per (class,
		// granted id) -- the naive granted×class form blew the ring at 22,410
		// ids / 121,725 bytes against the 32768-byte receive ring.
		for _, row := range dreadconfig.ShipResearchItems(class, hull.tier) {
			shared := deInflatedItemID(row.ID)
			if grantedSet[shared] {
				add(row.ID)
				counts.inflated++
			}
		}
		// The shared form of every fitted inflated id (the reverse leg) --
		// and keep the fitted inflated form itself, which the client
		// queries verbatim from the hull record.
		for _, id := range append(append([]int32{hull.primary, hull.secondary}, hull.abilities[:]...), hull.perks[:]...) {
			if c := (id >> 24) & 0xff; heroWithoutBase && (c == 4 || c == 5) {
				continue // per-class weapon/module: see ownedBaseHulls
			}
			if id != 0 {
				add(inflatedItemID(id, class))
				shared := deInflatedItemID(inflatedItemID(id, class))
				if shared != inflatedItemID(id, class) && shared > 0 {
					add(shared)
					counts.fitted++
				}
			}
		}
	}
	return out, counts
}

// applyTechTreeMirror appends the mirror ids to a list and logs the counts.
// `target` names the field for the log line ("purchasesdata" / "items").
func applyTechTreeMirror(ids []int32, playerPID string, fittedByLoadout map[int32]baseShipLoadout, target string) ([]int32, mirrorCounts) {
	mirror, counts := techTreeMirrorIDs(playerPID, fittedByLoadout)
	ids = append(ids, mirror...)
	logrus.WithFields(counts.fields()).WithField("player", playerPID).
		Info("id_mirror: granted ids mirrored into " + target + " (DN_ID_MIRROR)")
	return ids, counts
}
