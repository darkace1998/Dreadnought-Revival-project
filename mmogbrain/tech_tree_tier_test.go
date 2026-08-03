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

// Normalising the tier must not reprice anything: a /T0/ alternative is the
// base variant of its line and stays free to research.
func TestTierNormalisationDidNotRepriceModules(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	free := 0
	for _, item := range techTreeBaseItems() {
		if item.module && item.xpCost == 0 {
			free++
		}
	}
	if free != 55 {
		t.Errorf("%d modules are free to research, want 55; the cost is no longer taken from the raw tier", free)
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
