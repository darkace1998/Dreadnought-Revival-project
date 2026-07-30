package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Prereq entries are matched against other items' Id, so they have to live in
// the same id space the rows are keyed on. They used to be ship-pawn ids while
// Id was the precast loadout id, which made every prerequisite dangling -- and
// a pawn id (category 10) could never be admitted anyway, since the gate only
// accepts YShipLoadoutPrecast (1) and YShipLoadoutHero (3).
func TestTechTreePrereqsNameRowsInTheSameDocument(t *testing.T) {
	document := inflateTechTreeDocument(t, buildMmogTechTreePayload("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))

	// Field names are length-prefixed, so anchor on \x02Id\x09 -- a bare "Id"
	// also matches the tail of "ClassId", which would let a prereq "pass" by
	// colliding with a class number.
	ids := map[string]bool{}
	for _, match := range regexp.MustCompile(`\x02Id\x09.{4}(\d+)`).FindAllStringSubmatch(string(document), -1) {
		ids[match[1]] = true
	}
	if len(ids) == 0 {
		t.Fatal("no item ids found in the tech tree document; the scan is not working")
	}

	// Every id in the document, prereq or row, must be a loadout or hero id.
	prereqs := 0
	// Prereq is an object (0x0c) whose children are named "0", "1", ... so the
	// client keeps a name table for it; see appendMmogTechTreeItem for why an
	// array (0x0d) made the loader index the container by position instead.
	for _, match := range regexp.MustCompile(`\x06Prereq\x0c.{4}(.{0,64})`).FindAllStringSubmatch(string(document), -1) {
		for _, candidate := range regexp.MustCompile(`\x01\d\x09.{4}(\d+)`).FindAllStringSubmatch(match[1], -1) {
			prereqs++
			id, err := strconv.Atoi(candidate[1])
			if err != nil {
				t.Fatalf("prereq %q is not numeric", candidate[1])
			}
			if category := (id >> 24) & 0xff; category != 1 && category != 3 {
				t.Errorf("prereq %d is category %d; the gate only admits 1 (precast) and 3 (hero)", id, category)
			}
			if !ids[candidate[1]] {
				t.Errorf("prereq %s names no row in the document (rows: %s)", candidate[1], strings.Join(keysOf(ids), ", "))
			}
		}
	}
	if prereqs == 0 {
		t.Fatal("no prereq entries were checked; the tree carries none, so this test proves nothing")
	}
	t.Logf("checked %d prereq entries against %d rows", prereqs, len(ids))
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestTechTreeClassIdsPassTheManagerStoreGate pins the invariant that made the
// tech tree screen empty for months.
//
// UYTechTreeManager's loader only stores an item when its ClassId survives
//
//	MOVSXD R15,[RBP-0x78]   ; ClassId, parsed at 140400006
//	TEST R15D,R15D / JLE skip
//	CALL FUN_1405483e0      ; (ClassId >> 24) & 0xff in {1, 3}?
//	JZ skip
//
// where FUN_1405483e0 compares FUN_1402cf640(ClassId) -- the top byte -- against
// the registered category ids for YShipLoadoutPrecast (1) and YShipLoadoutHero
// (3). ClassId used to carry the 1..15 EYShipClass ordinal, whose top byte is 0,
// so every node was dropped before it reached a manufacturer group. The screen
// then reported "GetManufacturerData Could not find a manufacturer with id
// 0/1/2" and "Attempted to access index 0 from array TreeWidgetList of length
// 0!" -- symptoms that look like a missing response but were a rejected field.
func TestTechTreeClassIdsPassTheManagerStoreGate(t *testing.T) {
	items := append(techTreeBaseItems(), techTreeHeroItems()...)
	if len(items) == 0 {
		t.Fatal("tech tree carries no items")
	}
	rows := map[int32]bool{}
	for _, item := range items {
		rows[item.id] = true
	}
	for _, item := range items {
		if item.classID <= 0 {
			t.Fatalf("item %d has ClassId %d; the gate rejects anything <= 0", item.id, item.classID)
		}
		if category := (item.classID >> 24) & 0xff; category != 1 && category != 3 {
			t.Fatalf("item %d has ClassId %d (category %d); only 1 (precast) and 3 (hero) are stored",
				item.id, item.classID, category)
		}
		// The class root is a node in this same document -- a column has to
		// point at a row that exists, or the UI groups against nothing.
		if !rows[item.classID] {
			t.Errorf("item %d has ClassId %d, which names no row in the document", item.id, item.classID)
		}
	}

	// Every tier of one hull line shares a ClassId: that is what makes them a
	// single column rather than N one-node columns.
	classesPerLine := map[int32]map[int32]bool{}
	for _, item := range techTreeBaseItems() {
		if classesPerLine[item.classID] == nil {
			classesPerLine[item.classID] = map[int32]bool{}
		}
		classesPerLine[item.classID][item.tier] = true
	}
	multiTier := 0
	for _, tiers := range classesPerLine {
		if len(tiers) > 1 {
			multiTier++
		}
	}
	if multiTier == 0 {
		t.Error("no hull line shares a ClassId across tiers; the tree would render as single-node columns")
	}
}
