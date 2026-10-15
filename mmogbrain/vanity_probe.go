package main

// vanity_probe.go -- the DN_VANITY_PROBE single-hull A/B. Behind a switch, exactly one hull gets its cosmetic ids as
// TechTrees blob rows (proxyType -1 → the per-ship modules array) and the
// grants ride the normal vanity purchase path, while every other hull shows
// only its real modules. The A/B answer is whether the probe hull's hangar
// shows cosmetic tiles the other hulls lack -- i.e. whether blob entries
// surface vanity at all, before any shop/UI work.
//
// Nothing invented: the probe ids are real VanityItems_cooked.jsonl rows
// (verified present in the loaded catalog at startup and skipped otherwise),
// one per cosmetic category: coating(22) / pattern(23) / emblem(21) / decal(24).
// The client's cooked m_compatibleShips arrays are ALL ZEROS
//, so per-hull cosmetic
// binding cannot come from that field -- which the probe may also expose.

import (
	"database/sql"
	"os"
	"strconv"
	"strings"
	"sync"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
	"github.com/sirupsen/logrus"
)

var (
	vanityProbeOnce   sync.Once
	vanityProbeOn     bool
	vanityProbeHullID int32
)

// vanityProbeEnabled: DN_VANITY_PROBE=1 turns the single-hull A/B on.
func vanityProbeEnabled() bool {
	vanityProbeOnce.Do(func() {
		vanityProbeOn = os.Getenv("DN_VANITY_PROBE") == "1"
		vanityProbeHullID = vanityProbeResolveHull()
	})
	return vanityProbeOn
}

// vanityProbeResolveHull picks the A/B hull: DN_VANITY_PROBE_HULL may name it
// (case-insensitive, e.g. "Agosta") or give its numeric loadoutID; default is
// the T1 AssaultMedium starter every account owns.
func vanityProbeResolveHull() int32 {
	want := strings.ToLower(strings.TrimSpace(os.Getenv("DN_VANITY_PROBE_HULL")))
	if want == "" {
		want = "agosta"
	}
	for _, hull := range baseShipLoadouts {
		if want == strings.ToLower(hull.name) || want == strconv.Itoa(int(hull.loadoutID)) {
			logrus.WithFields(logrus.Fields{"hull": hull.name, "loadout_id": hull.loadoutID}).
				Info("vanity_probe: A/B hull selected (DN_VANITY_PROBE)")
			return hull.loadoutID
		}
	}
	logrus.WithField("want", want).Warn("vanity_probe: DN_VANITY_PROBE_HULL not found in the roster; A/B off")
	vanityProbeOn = false
	return 0
}

// vanityProbeItemIDs: one real catalog row per cosmetic category. Category law:
// the id's top byte IS the ItemIDTable CategoryID (21 emblem / 22 coating /
// 23 pattern / 24 decal) -- the same byte isVanityItemID keys on.
var vanityProbeCandidateIDs = []int32{
	0x15FF0001, // VAN_EMB_Bear_DA
	0x16FF0002, // VAN_PN_Afariz_DA
	0x17FF00AE, // VAN_PTN_Dazzle_DA
	0x18FF0001, // VAN_DCL_Afariz_Black_DA
}

func vanityProbeItemIDs() []int32 {
	known := map[int32]bool{}
	for _, v := range dreadconfig.VanityItems() {
		known[v.ItemID] = true
	}
	ids := vanityProbeCandidateIDs[:0]
	for _, id := range vanityProbeCandidateIDs {
		if known[id] {
			ids = append(ids, id)
		} else {
			logrus.WithField("item_id", id).
				Warn("vanity_probe: candidate id absent from the cooked vanity catalog; skipping")
		}
	}
	return ids
}

// vanityProbeTechTreeItems builds the blob rows: each granted cosmetic id
// keyed to the probe hull's ClassId with the hull itself as prerequisite --
// the same shape a module research row takes, so it lands in the hull's
// modules array (ProxyType -1).
func vanityProbeTechTreeItems(hull baseShipLoadout, manufacturerID int32) []techTreeItem {
	ids := vanityProbeItemIDs()
	items := make([]techTreeItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, techTreeItem{
			id:           id,
			classID:      hull.loadoutID,
			manufacturer: manufacturerID,
			tier:         techTreeWireTier(1),
			xpCost:       0,
			prereq:       []int32{hull.loadoutID},
			module:       true,
		})
	}
	logrus.WithFields(logrus.Fields{
		"hull": hull.name, "count": len(items), "ids": ids,
	}).Info("vanity_probe: cosmetic blob entries appended to techtrees (DN_VANITY_PROBE)")
	return items
}

// vanityProbeProvision runs once per player at the YA_PlayerGet dispatch,
// same shape as autoProvisionEverything: per-process cache, NOT EXISTS guard.
var vanityProbeDone sync.Map // pid -> true, once this process granted it

func vanityProbeProvision(pid string) {
	if !vanityProbeEnabled() || pid == "" {
		return
	}
	if _, done := vanityProbeDone.LoadOrStore(pid, true); done {
		return
	}
	database := currentMmogPlayerStateDB()
	if database == nil {
		vanityProbeDone.Delete(pid) // retry on the next PlayerGet
		return
	}
	vanityProbeGrant(database, pid)
}

// vanityProbeGrant records the probe ids through the normal vanity purchase
// path so ownership rides the existing Items-list branch (like every bought
// cosmetic). INSERT OR IGNORE: idempotent, price 0.
func vanityProbeGrant(database *sql.DB, pid string) {
	if !vanityProbeEnabled() {
		return
	}
	var inserted int
	for _, id := range vanityProbeItemIDs() {
		res, err := database.Exec(`INSERT INTO player_purchases(user_id,item_id,item_type,price_paid,currency)
			SELECT ?,?,'vanity',0,'admin' WHERE NOT EXISTS
			(SELECT 1 FROM player_purchases WHERE user_id=? AND item_id=?)`, pid, id, pid, id)
		if err != nil {
			logrus.WithError(err).WithField("player", pid).
				Warn("vanity_probe: grant failed -- will retry on next login")
			return // partial grant; the retry re-runs only the missing ones
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		}
	}
	logrus.WithFields(logrus.Fields{"player": pid, "granted": inserted, "ids": vanityProbeItemIDs()}).
		Info("vanity_probe: cosmetic grants recorded (DN_VANITY_PROBE)")
}
