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
	// Prereq is a bare array (0x0d) of unnamed string entries. See
	// appendMmogTechTreeItem for why the named-object form was reverted.
	for _, match := range regexp.MustCompile(`\x06Prereq\x0d.{4}(.{0,64})`).FindAllStringSubmatch(string(document), -1) {
		for _, candidate := range regexp.MustCompile(`\x00\x09.{4}(\d+)`).FindAllStringSubmatch(match[1], -1) {
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

	// ClassId must equal the item's OWN id, for every item. The loader keys the
	// array at manager+0x48 on ClassId, and the client looks that array up by
	// SHIP ID (FUN_1403f5050, called by ComposeModuleUiDataForShip), so any
	// ship whose ClassId is not its own id cannot resolve its modules. A
	// previous version shared the hull line's root id across the line, which
	// left every tier above the root logging "Modules not found for ship id".
	for _, item := range items {
		if item.classID != item.id {
			t.Fatalf("item %d has ClassId %d; it must be the item's own id or "+
				"ComposeModuleUiDataForShip cannot find its modules", item.id, item.classID)
		}
	}
}

// TestTechTreeDocumentHasTwoWrappingArrays pins the nesting depth that finally
// made UYTechTreeManager populate its manufacturer groups.
//
// The client's parser makes the document's FIRST field the root when that field
// is unnamed (root type = (nameLen < 1) + 5), so a wrapping array does not
// become a child of the root -- it BECOMES the root. The loader then walks
//
//	outer (1403ffe50)  RCX = docChildren + i*0x50   ; a group
//	                   FUN_140347e00 -> its children
//	inner (1403ffe90)  RDI = those + j*0x50         ; an item
//
// so with only one wrapper the outer loop iterated our ITEMS and the inner loop
// their FIELDS. Verified live: breaking at 1403ffec9 gave RDI type 4, names 0,
// children 0, strLen 9 -- an item's Id VALUE as a bare string. Every field read
// then returned the Id, which is why manager+0x38 held 37 groups keyed by
// loadout ids instead of 3 keyed by 0/1/2.
//
// With two wrappers RDI is type 5 with 12 names and 12 children, a ProxyType
// canary reports from all 100 items, and manager+0x38 holds exactly:
//
//	key 0 (JupiterArms) 33 items
//	key 1 (AkulaVektor) 34 items
//	key 2 (Oberon)      33 items
func TestTechTreeDocumentHasTwoWrappingArrays(t *testing.T) {
	document := inflateTechTreeDocument(t, buildMmogTechTreePayload("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	// Two unnamed array headers (00 0d + u32 length) before the first item.
	if len(document) < 12 {
		t.Fatal("tech tree document is too short to carry its wrappers")
	}
	if document[0] != 0x00 || document[1] != 0x0d {
		t.Fatalf("document must open with an unnamed ARRAY, got % x", document[:2])
	}
	if document[6] != 0x00 || document[7] != 0x0d {
		t.Fatalf("document needs a SECOND unnamed array; the outer one becomes the "+
			"root, so a single wrapper leaves items one level too shallow. got % x", document[6:8])
	}
	// The first item must then be an unnamed OBJECT, not another array.
	if document[12] != 0x00 || document[13] != 0x0c {
		t.Fatalf("expected an unnamed OBJECT item after the two wrappers, got % x", document[12:14])
	}
}
