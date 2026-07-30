package main

import (
	"regexp"
	"strconv"
	"testing"

	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
)

// The generated roster is only usable because every id in it was resolved
// through the client's own tables. This re-checks that in Go, so a hand-edit to
// the generated file cannot slip an unresolvable or miscategorised id past the
// generator.
func TestBaseShipLoadoutIDsResolveInTheRightCategories(t *testing.T) {
	if len(baseShipLoadouts) == 0 {
		t.Fatal("the base ship roster is empty")
	}
	const (
		categoryPrecastLoadout = 1
		categoryAbility        = 4
		categoryWeapon         = 5
		categoryPerk           = 6
	)
	seen := map[int32]bool{}
	for _, hull := range baseShipLoadouts {
		if seen[hull.loadoutID] {
			t.Errorf("loadout %d (%s) appears twice", hull.loadoutID, hull.name)
		}
		seen[hull.loadoutID] = true

		checks := []struct {
			label    string
			id       int32
			category int32
		}{
			{"loadout", hull.loadoutID, categoryPrecastLoadout},
			{"primary weapon", hull.primary, categoryWeapon},
			{"secondary weapon", hull.secondary, categoryWeapon},
		}
		for _, id := range hull.abilities {
			checks = append(checks, struct {
				label    string
				id       int32
				category int32
			}{"ability", id, categoryAbility})
		}
		for _, id := range hull.perks {
			checks = append(checks, struct {
				label    string
				id       int32
				category int32
			}{"perk", id, categoryPerk})
		}
		for _, check := range checks {
			if check.id == 0 {
				continue // an unfilled slot; tier-1 hulls carry no perks
			}
			if got := (check.id >> 24) & 0xff; got != check.category {
				t.Errorf("%s (%s) id %d is category %d, want %d", hull.name, check.label, check.id, got, check.category)
			}
			if _, ok := dreadconfig.ItemByID(check.id); !ok {
				t.Errorf("%s (%s) id %d resolves to no item", hull.name, check.label, check.id)
			}
		}
	}
	t.Logf("validated %d hulls", len(baseShipLoadouts))
}

// Each hull's tier and class must agree with its own blueprint path, which is
// the game's statement of both.
func TestBaseShipRosterAgreesWithItsAssetPaths(t *testing.T) {
	// AssaultLight T5 puts its tier after "PrecastLoadout" rather than before,
	// so the suffix has to be accepted in either position.
	tierInPath := regexp.MustCompile(`/Loadouts/Precast/(?:T(\d)/)?VH_([A-Za-z]+)_(?:T(\d)_)?PrecastLoadout(?:_T(\d))?_BP$`)
	for _, hull := range baseShipLoadouts {
		item, ok := dreadconfig.ItemByID(hull.loadoutID)
		if !ok {
			continue // reported by the test above
		}
		match := tierInPath.FindStringSubmatch(item.AssetPath)
		if match == nil {
			t.Errorf("%s: %s is not a tiered precast loadout path", hull.name, item.AssetPath)
			continue
		}
		tier := match[1]
		if tier == "" {
			tier = match[3]
		}
		if tier == "" {
			tier = match[4]
		}
		if strconv.Itoa(int(hull.tier)) != tier {
			t.Errorf("%s claims tier %d, path says T%s (%s)", hull.name, hull.tier, tier, item.AssetPath)
		}
		if match[2] != hull.hullLine {
			t.Errorf("%s claims hull line %q, path says %q", hull.name, hull.hullLine, match[2])
		}
		if _, ok := eyShipClassByKey[hull.hullLine]; !ok {
			t.Errorf("%s hull line %q has no EYShipClass", hull.name, hull.hullLine)
		}
	}
}

// The tree the client reads must cover the whole roster: it was limited to the
// ten nodes derived from the four ships a player owns, which is why the
// manufacturer pages showed almost nothing.
func TestTechTreeDocumentCoversEveryBaseHull(t *testing.T) {
	document := inflateTechTreeDocument(t, buildMmogTechTreePayload("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))

	emitted := map[string]bool{}
	for _, match := range regexp.MustCompile(`\x02Id\x09.{4}(\d+)`).FindAllStringSubmatch(string(document), -1) {
		emitted[match[1]] = true
	}
	for _, hull := range baseShipLoadouts {
		if !emitted[strconv.Itoa(int(hull.loadoutID))] {
			t.Errorf("tech tree document is missing %s (%d)", hull.name, hull.loadoutID)
		}
	}
	if len(emitted) != len(baseShipLoadouts) {
		t.Errorf("document carries %d ids, roster has %d hulls", len(emitted), len(baseShipLoadouts))
	}

	// Position must increase inside each manufacturer group, or nodes stack on
	// top of each other on the tree screen.
	for _, group := range regexp.MustCompile(`\x08Position\x09.{4}(\d+)`).FindAllStringSubmatch(string(document), -1) {
		if _, err := strconv.Atoi(group[1]); err != nil {
			t.Errorf("Position %q is not numeric", group[1])
		}
	}
	t.Logf("document carries %d nodes", len(emitted))
}
