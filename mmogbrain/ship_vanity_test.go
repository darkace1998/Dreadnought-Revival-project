package main

import (
	"strconv"
	"strings"
	"testing"

	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
)

// The client reads every slot of m_displayInfo as an item id and async-loads it,
// so a slot holding -1 costs a warning per ship per refresh:
//
//	UYItemIDList::LoadItemsAsync | Asset with ID -1 has no valid FStringReference
//
// The shape is fixed by UYShipCustomisationComponent::ImportFromDisplayInfo,
// which rejects anything that is not five ';' groups whose first splits into
// exactly four on '#'.
func TestShipDisplayInfoIsWellFormedAndResolves(t *testing.T) {
	checked := 0
	for _, loadout := range starterShipLoadouts() {
		info := loadout.displayInfo()
		groups := strings.Split(info, ";")
		if len(groups) != 5 {
			t.Fatalf("%s: display info %q has %d groups, the importer demands 5", loadout.loadoutName, info, len(groups))
		}
		if mesh := strings.Split(groups[0], "#"); len(mesh) != 4 {
			t.Fatalf("%s: mesh group %q has %d entries, the importer demands 4", loadout.loadoutName, groups[0], len(mesh))
		}

		// Every slot that is not deliberately unset must name a real item.
		for i, slot := range []struct {
			label    string
			value    string
			category int32
		}{
			{"emblem", groups[1], 21},
			{"pattern", groups[3], 23},
			{"decal", groups[4], 24},
		} {
			if slot.value == dreadconfig.VanityUnsetSlot {
				t.Errorf("%s: %s slot is unset; it has a derivable default", loadout.loadoutName, slot.label)
				continue
			}
			id, err := strconv.Atoi(slot.value)
			if err != nil {
				t.Fatalf("%s: %s slot %q is not numeric", loadout.loadoutName, slot.label, slot.value)
			}
			if got := int32((id >> 24) & 0xff); got != slot.category {
				t.Errorf("%s: %s slot %d is category %d, want %d", loadout.loadoutName, slot.label, id, got, slot.category)
			}
			if _, ok := dreadconfig.ItemByID(int32(id)); !ok {
				t.Errorf("%s: %s slot %d resolves to no item", loadout.loadoutName, slot.label, id)
			}
			_ = i
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no starter loadouts were checked")
	}
	t.Logf("checked %d filled vanity slots across %d loadouts", checked, len(starterShipLoadouts()))
}

// Each hull line has its own default pattern; sending another line's would put
// the wrong camo on the ship.
func TestEveryHullLineHasItsOwnDefaultPattern(t *testing.T) {
	seen := map[int32]string{}
	for _, hull := range baseShipLoadouts {
		_, pattern, _ := dreadconfig.DefaultShipVanityItemIDs(hull.hullLine)
		if pattern == 0 {
			t.Errorf("hull line %s has no default pattern", hull.hullLine)
			continue
		}
		if other, clash := seen[pattern]; clash && other != hull.hullLine {
			t.Errorf("pattern %d is shared by %s and %s", pattern, other, hull.hullLine)
		}
		seen[pattern] = hull.hullLine
	}
	t.Logf("%d distinct default patterns", len(seen))
}
