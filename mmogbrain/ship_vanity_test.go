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
		mesh := strings.Split(groups[0], "#")
		if len(mesh) != 4 {
			t.Fatalf("%s: mesh group %q has %d entries, the importer demands 4", loadout.loadoutName, groups[0], len(mesh))
		}
		// Slot types 4-7 are UI_Category_APPEARANCE_HULL, a slot every ship has,
		// and each hull line ships exactly four defaults for it.
		for _, value := range mesh {
			if value == dreadconfig.VanityUnsetSlot {
				t.Errorf("%s: a mesh slot is unset; every hull line has four defaults", loadout.loadoutName)
				continue
			}
			id, err := strconv.Atoi(value)
			if err != nil {
				t.Fatalf("%s: mesh slot %q is not numeric", loadout.loadoutName, value)
			}
			if got := int32((id >> 24) & 0xff); got != 20 {
				t.Errorf("%s: mesh slot %d is category %d, want 20 (YShipVanityMeshPart)", loadout.loadoutName, id, got)
			}
			if _, ok := dreadconfig.ItemByID(int32(id)); !ok {
				t.Errorf("%s: mesh slot %d resolves to no item", loadout.loadoutName, id)
			}
			checked++
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

// Every hull line must have exactly four default mesh parts, since the importer
// demands a four-entry mesh group and padding one with -1 costs a warning per
// ship per refresh.
func TestEveryHullLineHasFourDefaultMeshParts(t *testing.T) {
	for _, hull := range baseShipLoadouts {
		if parts := dreadconfig.DefaultShipMeshPartIDs(hull.hullLine); len(parts) != 4 {
			t.Errorf("hull line %s has %d default mesh parts, want 4", hull.hullLine, len(parts))
		}
	}
}

// Every ship in the roster -- all 52 hulls and all 48 heroes -- must produce a
// fully-filled appearance, not just the four starters. An unset slot is not
// free: the client reads it as an item id and logs a warning per ship per
// refresh, and a slot shorter than two characters ("" or "0") costs a second
// warning because GetItemIDsFromDisplayInfoString substitutes -1 for it anyway.
func TestEveryShipGetsACompleteAppearance(t *testing.T) {
	categoryOfSlot := []int32{20, 20, 20, 20, 21, 22, 23, 24}

	check := func(t *testing.T, label, hullLine, manufacturer string) {
		t.Helper()
		info := dreadconfig.DefaultShipDisplayInfo(hullLine, manufacturer)
		groups := strings.Split(info, ";")
		if len(groups) != 5 {
			t.Errorf("%s: %q is not five groups", label, info)
			return
		}
		slots := append(strings.Split(groups[0], "#"), groups[1:]...)
		if len(slots) != len(categoryOfSlot) {
			t.Errorf("%s: %q yields %d slots, want %d", label, info, len(slots), len(categoryOfSlot))
			return
		}
		for i, value := range slots {
			if value == dreadconfig.VanityUnsetSlot {
				t.Errorf("%s: slot %d (category %d) is unset", label, i, categoryOfSlot[i])
				continue
			}
			id, err := strconv.Atoi(value)
			if err != nil {
				t.Errorf("%s: slot %d is %q, not numeric", label, i, value)
				continue
			}
			if got := int32((id >> 24) & 0xff); got != categoryOfSlot[i] {
				t.Errorf("%s: slot %d is category %d, want %d", label, i, got, categoryOfSlot[i])
			}
			if _, ok := dreadconfig.ItemByID(int32(id)); !ok {
				t.Errorf("%s: slot %d id %d resolves to no item", label, i, id)
			}
		}
	}

	for _, hull := range baseShipLoadouts {
		check(t, hull.name, hull.hullLine, baseShipManufacturerByClassSize[hull.hullLine])
	}
	for _, hero := range heroShipLoadouts {
		check(t, hero.name, hero.hullLine, hero.manufacturer)
	}
	t.Logf("checked %d hulls + %d heroes", len(baseShipLoadouts), len(heroShipLoadouts))
}

// Each maker's ships wear that maker's coating.
func TestPaintFollowsTheManufacturer(t *testing.T) {
	for _, manufacturer := range []string{"JupiterArms", "AkulaVektor", "Oberon"} {
		id := dreadconfig.DefaultShipPaintID(manufacturer)
		if id == 0 {
			t.Errorf("%s has no base coating", manufacturer)
			continue
		}
		if got := (id >> 24) & 0xff; got != 22 {
			t.Errorf("%s coating %d is category %d, want 22 (YShipVanityPaint)", manufacturer, id, got)
		}
	}
	if dreadconfig.DefaultShipPaintID("JupiterArms") == dreadconfig.DefaultShipPaintID("Oberon") {
		t.Error("Jupiter Arms and Oberon share a coating")
	}
}
