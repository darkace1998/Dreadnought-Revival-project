package main

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// A ship's page shows, under its tree, the ships it leads to and the briefing
// it unlocks (techTreeShipPageLinks): the client takes them from the ship's
// modules array (ProxyType -1) when they carry a UI child keyed with the
// ship's id and a NumTechTreeItemsRequired above 0. The overview (page 0)
// still has to find every hull.
func TestTechTreeShipPageCarriesNextShipsAndBriefing(t *testing.T) {
	const (
		agosta     = 33489262 // Jupiter T1, a line root
		dover      = 33489267 // T2, from Agosta
		jutland    = 33489286 // T4, unlocks Retaliator
		retaliator = 117374978
	)
	items := techTreeBaseItems()
	find := func(id int32) techTreeItem {
		for _, item := range items {
			if item.id == id {
				return item
			}
		}
		t.Fatalf("no tree item %d", id)
		return techTreeItem{}
	}
	encode := func(item techTreeItem) []byte {
		b, _ := appendMmogTechTreeItem(nil, nil, item)
		return b
	}
	uiChild := func(page int32) []byte { return appendFieldMarker(strconv.Itoa(int(page)), 0x0c) }
	has := func(b []byte, name, value string) bool {
		return bytes.Contains(b, protocol.AppendStringField(nil, name, value))
	}

	next := encode(find(dover))
	if !bytes.Contains(next, uiChild(0)) || !bytes.Contains(next, uiChild(agosta)) {
		t.Error("Dover needs UI children for the overview (0) and for Agosta's page")
	}
	if !has(next, "ProxyType", "-1") || !has(next, "ClassId", strconv.Itoa(agosta)) {
		t.Error("Dover must be filed in Agosta's modules array: ClassId Agosta, ProxyType -1")
	}
	if !has(next, "NumTechTreeItemsRequired", "5") {
		t.Error("Dover needs Agosta's 5 modules (tier I), or the progression row drops it")
	}

	root := encode(find(agosta))
	if !has(root, "ProxyType", "9") || bytes.Contains(root, uiChild(jutland)) {
		t.Error("a line root has no parent page and keeps ProxyType 9")
	}

	briefing := find(retaliator)
	if briefing.classID != jutland {
		t.Fatalf("Retaliator is filed under %d, want Jutland", briefing.classID)
	}
	b := encode(briefing)
	if !bytes.Contains(b, uiChild(jutland)) || bytes.Contains(b, uiChild(0)) {
		t.Error("Retaliator belongs on Jutland's page only")
	}
	if !has(b, "NumTechTreeItemsRequired", strconv.Itoa(officerBriefingModulesRequired)) || !has(b, "ProxyType", "-1") {
		t.Error("Retaliator needs its 2-module gate and the modules array")
	}
	// Module entries carrying Prereq crashed the client (2026-09-29): the
	// ship's id may appear as ClassId and as the UI child's name, not a third
	// time as a prerequisite.
	if n := bytes.Count(b, []byte(strconv.Itoa(jutland))); n != 2 {
		t.Errorf("Jutland's id appears %d times in the briefing entry, want 2 (ClassId, UI child) -- no Prereq", n)
	}

	// Every hull with a parent is on that parent's page; no id appears twice
	// (a second entry would be listed twice wherever the client lists all
	// ships, e.g. UUI_PersistentScreen::GetShipModuleData).
	for _, item := range items {
		if item.module || len(item.prereq) == 0 || item.techItemsRequired == 0 {
			continue
		}
		if item.shipPage != item.prereq[0] {
			t.Errorf("hull %d is on page %d, want its parent %d", item.id, item.shipPage, item.prereq[0])
		}
	}
}

// Every weapon and module a ship comes fitted with is in the tech tree, or the
// module details screen of an owned ship shows the "99999" placeholder for it
// (techTreeFittedAnchorItems). The ones no ship researches are filed under
// their line's anchor -- never under a ship whose page would draw them, and
// never in the global list (ClassId -1) that every ship's item list includes.
func TestTechTreeKnowsEveryFittedItem(t *testing.T) {
	items := techTreeBaseItems()
	byID := map[int32]techTreeItem{}
	for _, item := range items {
		byID[item.id] = item
	}
	ships := map[int32]bool{}
	for _, hull := range baseShipLoadouts {
		ships[hull.loadoutID] = true
	}
	anchored := 0
	for _, hull := range baseShipLoadouts {
		class := eyShipClassByKey[hull.hullLine]
		for _, shared := range append(append([]int32{hull.primary, hull.secondary}, hull.abilities[:]...), hull.perks[:]...) {
			id := inflatedItemID(shared, class)
			if shared <= 0 || isOfficerBriefing(id) {
				continue
			}
			item, ok := byID[id]
			if !ok {
				t.Errorf("%s's fitted %d is in no tree", hull.name, id)
				continue
			}
			if item.classID <= 0 {
				t.Errorf("%s's fitted %d has ClassId %d", hull.name, id, item.classID)
			}
			if !ships[item.classID] {
				anchored++
				if item.classID != techTreeLineAnchor(hull.hullLine) {
					t.Errorf("%s's fitted %d is filed under %d, not its line's anchor", hull.name, id, item.classID)
				}
			}
		}
	}
	// Furia (Oberon's first artillery cruiser) fits 6, all in no other tree.
	if anchored < 6 {
		t.Errorf("only %d fitted items filed under anchors", anchored)
	}
}
