package main

import (
	"database/sql"
	"regexp"
	"strconv"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// A module's prerequisite is its hull, in the tree the client reads and on
// the server. Modules carried none, so every module of every ship was
// researchable with free XP ("u can research modules of ships u dont own",
// operator 2026-09-29).
func TestModulesRequireTheirHull(t *testing.T) {
	for _, hull := range baseShipLoadouts {
		for _, item := range techTreeModuleItems(hull, 0) {
			if len(item.prereq) != 1 || item.prereq[0] != hull.loadoutID {
				t.Fatalf("%s module %d: prereq %v, want its hull %d", hull.name, item.id, item.prereq, hull.loadoutID)
			}
		}
	}
}

func TestModuleResearchRefusedWithoutTheHull(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_state SET free_xp=1000000 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	owned := map[int32]bool{}
	for _, l := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(pid), pid) {
		owned[l.precastLoadoutID] = true
	}
	researched := func(item int32) bool {
		for _, id := range researchedOrOwnedItemIDs(pid) {
			if id == item {
				return true
			}
		}
		return false
	}
	research := func(item int32) {
		req := protocol.AppendStringField(nil, "RT", "YA_UnlockItem")
		req = append(req, protocol.AppendStringField(nil, "ItemID", strconv.Itoa(int(item)))...)
		req = append(req, protocol.AppendStringField(nil, "FreeXp", "5000")...)
		if err := persistUnlockItem(database, pid, protocol.AppendRootEnd(req)); err != nil {
			t.Fatal(err)
		}
	}

	var foreign, own int32
	for _, hull := range baseShipLoadouts {
		items := techTreeModuleItems(hull, 0)
		if len(items) == 0 {
			continue
		}
		if !owned[hull.loadoutID] && foreign == 0 {
			foreign = items[0].id
		}
		if owned[hull.loadoutID] && own == 0 {
			own = items[0].id
		}
	}
	if foreign == 0 || own == 0 {
		t.Fatal("need a module of an unowned hull and one of an owned hull")
	}
	research(foreign)
	if researched(foreign) {
		t.Error("a module of an unowned, unresearched hull was researched")
	}
	research(own)
	if !researched(own) {
		t.Error("a module of an owned hull could not be researched")
	}
}

// grantModuleHull records the module's hull as researched for pid, so a test
// that researches the module meets its prerequisite (the hull).
func grantModuleHull(t *testing.T, database interface {
	Exec(string, ...any) (sql.Result, error)
}, pid string, module int32) {
	t.Helper()
	hull, ok := researchHullLoadout(module)
	if !ok {
		t.Fatalf("module %d has no hull", module)
	}
	if _, err := database.Exec(`INSERT OR IGNORE INTO player_purchases(user_id,item_id,item_type,price_paid,currency,research_xp) VALUES(?,?,'ship',0,'freexp',0)`, pid, hull); err != nil {
		t.Fatalf("grant hull %d: %v", hull, err)
	}
}

// ...and the prerequisite reaches the wire: module entries are written by a
// minimal writer (appendMmogTechTreeModuleItem) that used to drop Prereq, so
// setting it on the item alone changed nothing the client received.
//
// Off by default since it crashed the client; runs with the switch on.
func TestModulePrereqsAreSent(t *testing.T) {
	old := techTreeModulePrereq
	techTreeModulePrereq = true
	t.Cleanup(func() { techTreeModulePrereq = old })
	modules := 0
	for _, hull := range baseShipLoadouts {
		modules += len(techTreeModuleItems(hull, 0))
	}
	doc := string(inflateTechTreeDocument(t, buildMmogTechTreePayload("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")))
	withEntry := len(regexp.MustCompile(`\x06Prereq\x0d.{4}\x00\x09`).FindAllStringIndex(doc, -1))
	if withEntry < modules {
		t.Errorf("%d tech tree entries carry a prerequisite, want at least the %d modules", withEntry, modules)
	}
}
