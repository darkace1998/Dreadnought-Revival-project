package main

import (
	"strconv"
	"testing"
)

// The client colours a tech tree tile with TierColors[Tier - 1], and that array
// has five entries. A Tier of 0 indexes -1:
//
//	Script Msg: Attempted to access index -1 from array TierColors of length 5!
//
// Reported nine times in one session of browsing ships. Fifty-five module
// entries were sending 0, all of them sibling lines whose lowest variant is a
// /T0/ asset.
func TestTechTreeNeverSendsTierZero(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	zero := countWireTierValue(string(buildMmogTechTreeDocument()), "0")
	if zero != 0 {
		t.Errorf("%d tech tree entries carry Tier=0; the client indexes TierColors[-1] for each", zero)
	}
}

// The normalisation must not flatten everything to 1: the tiers that mean
// something still have to arrive intact.
func TestTechTreeStillReportsRealTiers(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	payload := string(buildMmogTechTreeDocument())
	for tier := 1; tier <= 5; tier++ {
		if countWireTierValue(payload, strconv.Itoa(tier)) == 0 {
			t.Errorf("no tech tree entry reports Tier=%d; the tier is being collapsed", tier)
		}
	}
	if outOfRange := countWireTierValue(payload, "6") + countWireTierValue(payload, "-1"); outOfRange != 0 {
		t.Errorf("%d entries report a tier outside 1..5", outOfRange)
	}
}

// The tree has one layout row per tier, and the game has five. A raw tier 0
// produced a SIXTH row -- two of them claiming Tier 1, one carrying the row id
// techTreeLayoutRowID reserves for tier 0 -- against a client whose TierColors
// array has exactly five entries.
func TestTechTreeHasOneLayoutRowPerRealTier(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	byManufacturer := map[int32][]techTreeItem{}
	var order []int32
	seen := map[int32]bool{}
	for _, item := range append(techTreeBaseItems(), techTreeHeroItems()...) {
		byManufacturer[item.manufacturer] = append(byManufacturer[item.manufacturer], item)
		if !seen[item.manufacturer] {
			seen[item.manufacturer] = true
			order = append(order, item.manufacturer)
		}
	}
	tiers := techTreeTiersPresent(byManufacturer, order)
	if len(tiers) != 5 {
		t.Errorf("tech tree emits %d layout rows (%v), want 5 -- one per tier", len(tiers), tiers)
	}
	for _, tier := range tiers {
		if tier < 1 || tier > 5 {
			t.Errorf("layout row for tier %d, which is outside 1..5", tier)
		}
	}
}

// Nothing may be offered at zero cost by accident. This used to assert exactly
// 55 free modules -- the /T0/ variants the ungated rule surfaced as alternatives.
// Gating alternatives to the hull's tier removed all of them: a /T0/ item is
// what a hull already flies, not something it researches. So the expected count
// is now zero, and the assertion is the durable one -- a module that costs
// nothing is a module the player gets for free.
func TestNoModuleIsFreeToResearch(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	free := 0
	for _, item := range techTreeBaseItems() {
		if item.module && item.xpCost == 0 {
			free++
		}
	}
	if free != 0 {
		t.Errorf("%d offered modules cost 0 XP to research", free)
	}
}

func TestTechTreeWireTierFloorsAtOne(t *testing.T) {
	for in, want := range map[int32]int32{-1: 1, 0: 1, 1: 1, 3: 3, 5: 5} {
		if got := techTreeWireTier(in); got != want {
			t.Errorf("techTreeWireTier(%d) = %d, want %d", in, got, want)
		}
	}
}

// countWireTierValue counts "Tier" string fields carrying exactly want.
// Field encoding is <name> 0x09 <u32 length> <bytes>.
func countWireTierValue(payload, want string) int {
	n := 0
	for i := 0; i+9 < len(payload); i++ {
		if payload[i:i+4] != "Tier" || payload[i+4] != 0x09 {
			continue
		}
		size := int(uint32(payload[i+5]) | uint32(payload[i+6])<<8 |
			uint32(payload[i+7])<<16 | uint32(payload[i+8])<<24)
		if size > 0 && i+9+size <= len(payload) && payload[i+9:i+9+size] == want {
			n++
		}
	}
	return n
}

// A ship must never be offered a module it could not equip. The Tier 1 Agosta's
// tech tree offered Tier 5 modules -- Blast Ram, Missile Repeater, Flashpoint
// Torpedo Salvo -- because every sibling line contributed its lowest variant
// regardless of tier. Reported from a live client as "the module tech tree shows
// the wrong modules for each ship".
func TestNoShipIsOfferedAModuleAboveItsTier(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	checked := 0
	for _, hull := range baseShipLoadouts {
		for _, item := range techTreeModuleItems(hull, 1) {
			checked++
			if item.tier > hull.tier {
				t.Errorf("%s (tier %d) is offered module %d at tier %d",
					hull.name, hull.tier, item.id, item.tier)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no modules emitted at all; the test is not exercising anything")
	}
	t.Logf("checked %d offered modules across %d hulls", checked, len(baseShipLoadouts))
}

// The gate must not empty the tree for the tiers that have data. Measured across
// all 52 hulls after the fix: tier 2 averages 8 modules, tiers 3 and 4 average
// 15, tier 5 averages 23. Tier 1 is 0-2 and that is the assets' own answer --
// the Assault, Dreadnought and Support sibling lines have no variant below tier
// 2, so a starter hull has almost nothing to research and its rails show the
// modules it is already flying.
func TestHullsWithDataStillHaveAModuleTree(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	thin := 0
	for _, hull := range baseShipLoadouts {
		if hull.tier < 3 {
			continue // see above; tier 1-2 lines genuinely start higher
		}
		if len(techTreeModuleItems(hull, 1)) < 5 {
			thin++
			t.Logf("%s (tier %d) offers few modules", hull.name, hull.tier)
		}
	}
	// One or two hulls whose whole fitted set sits on tier-less asset paths can
	// still come out thin. A general collapse cannot.
	if thin > 2 {
		t.Errorf("%d hulls at tier 3+ offer fewer than 5 modules; the gate is too strict", thin)
	}
}

// Tier 5 hulls fit abilities that ItemIDRegister maps to the PREVIOUS build's
// tier-less asset path, which the tiered pattern cannot see. That left them
// resolving to no slot group at all and offered two modules -- both weapons --
// in the entire tech tree.
func TestTopTierHullsResolveTheirAbilitySlots(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	techTreeBuildSlotIndex()

	for _, hull := range baseShipLoadouts {
		if hull.tier != 5 {
			continue
		}
		for _, id := range hull.abilities {
			if id == 0 {
				continue
			}
			if _, ok := techTreeSlotOf[id]; !ok {
				t.Errorf("%s (tier 5) fits ability %d, which is in no slot group; "+
					"its sibling lines cannot be offered", hull.name, id)
			}
		}
	}
}
