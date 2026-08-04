package main

import (
	"bytes"
	"encoding/binary"
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

	// ClassId must equal the item's OWN id -- for SHIP nodes. The loader keys
	// the array at manager+0x48 on ClassId, and the client looks that array up
	// by SHIP ID (FUN_1403f5050 / FindShipTechTreeData, called by
	// ComposeModuleUiDataForShip), so any ship whose ClassId is not its own id
	// cannot resolve its modules. A previous version shared the hull line's
	// root id across the line, which left every tier above the root logging
	// "Modules not found for ship id".
	//
	// MODULE entries are the deliberate exception, and they are the reason that
	// array is keyed this way at all: a module carries the HULL's id as its
	// ClassId, which is precisely what files it under that ship's record. Its
	// own id is the module item id, recovered by the classifier at RVA 0x541CD0
	// to derive the slot identifier. An earlier version of this test asserted
	// the rule over every item without exception, which is correct only for a
	// document that has no modules in it -- the state that made every ship
	// report "0/0 modules available".
	for _, item := range items {
		if item.module {
			if item.classID == item.id {
				t.Errorf("module %d has ClassId equal to its own id; it must carry its hull's id to be filed under that ship", item.id)
			}
			continue
		}
		if item.classID != item.id {
			t.Fatalf("ship node %d has ClassId %d; it must be the item's own id or "+
				"ComposeModuleUiDataForShip cannot find its modules", item.id, item.classID)
		}
	}

	// Every module must name a ship node that exists, or it is filed under a
	// record no lookup will ever produce.
	ships := map[int32]bool{}
	for _, item := range items {
		if !item.module {
			ships[item.id] = true
		}
	}
	for _, item := range items {
		if item.module && !ships[item.classID] {
			t.Errorf("module %d is filed under ClassId %d, which is not a ship node in the document", item.id, item.classID)
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

// The tech tree screen renders from LAYOUT data, and the layout lives on each
// item's "UI" field -- not on the item.
//
// UYTechTreeManager's loader reads Position, Visible and Wires from the CHILDREN
// of the item's UI node (1404002b5 indexes the UI node's children pointer, and
// the loop is bounded by that node's child count at [RBP+0x1c0]). An item with
// no UI therefore contributes no layout node at all, which is what left the
// screen empty with "Attempted to access index 0 from array TreeWidgetList of
// length 0!" long after the manufacturer groups, tiers, costs and ProxyType had
// all been fixed and verified in memory. Position/Visible sent flat on the item
// are read by nothing in this loader.
func TestTechTreeItemsCarryUILayoutNodes(t *testing.T) {
	document := buildMmogTechTreeDocument()

	for _, field := range []string{"UI", "Position", "x", "y", "Wires"} {
		if !strings.Contains(string(document), field) {
			t.Errorf("document has no %q field; the loader reads it for layout", field)
		}
	}

	// Position must be an OBJECT of x and y, both numeric. The loader narrows
	// each to float32 (MOVSS at 140400456/14040050f) after the usual numeric
	// union, so a string value is fine but it has to parse as a number.
	// Field layout is <namelen:1><name><tag 0x09><u32 len><value>.
	marker := append([]byte{1, 'x', 0x09}, 0, 0, 0, 0)
	idx := bytes.Index(document, marker[:3])
	if idx < 0 {
		t.Fatal(`no "x" string field found inside Position`)
	}
	n := int(binary.LittleEndian.Uint32(document[idx+3 : idx+7]))
	value := string(document[idx+7 : idx+7+n])
	if _, err := strconv.ParseFloat(value, 64); err != nil {
		t.Errorf("Position.x %q does not parse as a number: %v", value, err)
	}
}

// The tier ROW layout table at manager+0x58 is fed by items whose Id falls in a
// negative sentinel range, and by nothing else.
//
//	140400fbc  MOV EAX,dword ptr [RBP + -0x80]  ; Id
//	140400fbf  ADD EAX,0x1e8480                 ; +2,000,000
//	140400fc4  CMP EAX,0xf423f                  ; <= 999,999 unsigned
//	140400fc9  JA  -> treat as a NORMAL item
//
// No real item id can satisfy that, so the array was empty in every live
// measurement -- which read as "this structure is unreachable" and cost a lot of
// time. It is reachable; it just needs rows of its own.
func TestTechTreeLayoutRowIdsPassTheClassLookupGate(t *testing.T) {
	const (
		lo = -2000000
		hi = -1000001
	)
	for tier := int32(1); tier <= 5; tier++ {
		id := techTreeLayoutRowID(tier)
		if id < lo || id > hi {
			t.Errorf("tier %d layout row id %d is outside the gate range [%d, %d]", tier, id, lo, hi)
		}
		// The gate is an unsigned range check on Id+2,000,000, so verify it the
		// way the binary does rather than trusting the signed comparison above.
		if uint32(id+2000000) > 999999 {
			t.Errorf("tier %d layout row id %d fails the unsigned gate", tier, id)
		}
	}

	document := buildMmogTechTreeDocument()
	if !strings.Contains(string(document), strconv.Itoa(int(techTreeLayoutRowID(1)))) {
		t.Error("document carries no tier-1 layout row; manager+0x58 will stay empty")
	}
}

// A slot's module entries are the ALTERNATIVES only -- never anything the hull
// already fields.
//
// The client draws a ship's fitted modules from its own UYCachedItemIDData slot
// list (tags 1..10 = primary weapon, secondary weapon, four abilities, four
// briefings), so whatever we send for a slot is IN ADDITION to what is already
// on the rail. Three separate attempts each put a second copy of the default
// loadout on screen: emitting the equipped items, emitting the equipped line's
// tier chain (two ids, one module -- "Tempest Missiles N" beside an identical
// "Tempest Missiles N"), and finally emitting the equipped item once alongside
// the alternatives. Only the alternatives belong here.
func TestTechTreeModulesAreAlternativesOnly(t *testing.T) {
	var otranto, agosta baseShipLoadout
	for _, hull := range baseShipLoadouts {
		switch hull.name {
		case "Otranto":
			otranto = hull
		case "Agosta":
			agosta = hull
		}
	}
	if otranto.loadoutID == 0 || agosta.loadoutID == 0 {
		t.Skip("roster does not carry the sampled hulls")
	}
	techTreeBuildSlotIndex()

	for _, hull := range []baseShipLoadout{agosta, otranto} {
		fitted := map[int32]bool{hull.primary: true, hull.secondary: true}
		for _, a := range hull.abilities {
			if a != 0 {
				fitted[a] = true
			}
		}
		for _, p := range hull.perks {
			if p != 0 {
				fitted[p] = true
			}
		}

		seen := map[int32]bool{}
		for _, m := range techTreeModuleItems(hull, 0) {
			if fitted[m.id] {
				t.Errorf("%s re-sends fitted module %d; the client already draws it", hull.name, m.id)
			}
			if seen[m.id] {
				t.Errorf("%s emits module id %d more than once", hull.name, m.id)
			}
			seen[m.id] = true
		}

		// No two entries may come from the same line either: a module's tier is
		// not a separate node, so two tiers of one line read as one module
		// drawn twice.
		lines := map[techTreeSlotKey]int{}
		for _, m := range techTreeModuleItems(hull, 0) {
			if key, ok := techTreeSlotOf[m.id]; ok {
				lines[key]++
				if lines[key] > 1 {
					t.Errorf("%s emits %d entries from line %v; one per line only", hull.name, lines[key], key)
				}
			}
		}
	}

	// ASSERTION CHANGED 2026-08-04, and the reason matters. This used to require
	// a tier-1 hull to get at least four alternatives, on the reasoning that
	// gating by hull tier "left the starter ships with nothing to research".
	// That is true, and it is the correct answer: measured across the assets, a
	// tier-1 hull has 0-2 of ~26 sibling lines available at or below its tier,
	// because the Assault, Dreadnought and Support lines have no variant below
	// tier 2. Filling the gap with the lines' lowest variants is what put Tier 5
	// modules -- Blast Ram, Missile Repeater -- in the Tier 1 Agosta's tech
	// tree, reported from a live client as "the module tech tree shows the wrong
	// modules for each ship".
	//
	// So a starter hull may legitimately offer none. What must still hold is
	// that whatever IS offered is a real alternative of the right category.
	starter := techTreeSlotUpgrades(agosta.abilities[0], agosta.tier)
	for _, v := range starter {
		if v.tier > agosta.tier {
			t.Errorf("Agosta (tier 1) is offered a tier %d module (%d)", v.tier, v.itemID)
		}
	}
	for _, v := range starter {
		if v.itemID == agosta.abilities[0] {
			t.Errorf("Agosta ability slot re-sends its own equipped module %d", v.itemID)
		}
		if category := (v.itemID >> 24) & 0xff; category != 4 {
			t.Errorf("ability slot entry %d has category %d, want 4 (YAbility)", v.itemID, category)
		}
	}

	// A primary weapon family is the hull's own size, not a choice, so it has
	// no alternatives at all -- and must not fall back to re-sending itself.
	if got := techTreeSlotUpgrades(otranto.primary, otranto.tier); len(got) != 0 {
		t.Errorf("primary weapon slot offered %+v; Light/Medium/Heavy are the hull's size, not alternatives", got)
	}
}

// No two ship nodes may occupy the same cell of the tech tree grid.
//
// Position was left at its zero value for every ship in the roster. That was
// invisible for as long as Position travelled flat on the item, where
// UYTechTreeManager's loader never reads it -- but the node's x coordinate is
// derived from it, so an entire manufacturer's ships landed on one point and
// the screen drew them stacked. Whichever node won the overlap is the hull that
// rendered, which is how a tier-1 slot could show a different ship's model
// (reported as "the rurik is loading the furia").
//
// A hull LINE is a column and a tier is a row. Heroes have the same problem in
// their own grid and get a running column per (manufacturer, tier).
func TestTechTreeNodesDoNotShareAGridCell(t *testing.T) {
	type cell struct {
		manufacturer, tier, position int32
		hero                         bool
	}
	occupied := map[cell][]int32{}
	for _, item := range append(techTreeBaseItems(), techTreeHeroItems()...) {
		if item.module {
			continue // modules are not drawn on the grid
		}
		c := cell{item.manufacturer, item.tier, item.position, item.hero}
		occupied[c] = append(occupied[c], item.id)
	}
	for c, ids := range occupied {
		if len(ids) > 1 {
			t.Errorf("manufacturer=%d tier=%d position=%d hero=%v is shared by %v; they will draw on top of each other",
				c.manufacturer, c.tier, c.position, c.hero, ids)
		}
	}

	// One hull line must be one column, or a line zig-zags across the screen.
	columnOfLine := map[string]int32{}
	idToLine := map[int32]string{}
	for _, hull := range baseShipLoadouts {
		idToLine[hull.loadoutID] = hull.hullLine
	}
	for _, item := range techTreeBaseItems() {
		if item.module {
			continue
		}
		line := idToLine[item.id]
		if existing, seen := columnOfLine[line]; seen && existing != item.position {
			t.Errorf("hull line %s spans columns %d and %d; a line must be one column", line, existing, item.position)
		}
		columnOfLine[line] = item.position
	}
}
