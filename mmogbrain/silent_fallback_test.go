package main

import (
	"testing"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// A default that cannot be told apart from a real value in the log is not a
// default, it is a hidden failure (AGENT-CHAT C33.5, named after a client-side
// `shipClass = 6` that made a missed lookup look like a Dreadnought Medium on
// all 52 transitions).
//
// This server ships two of exactly that shape, and both have already cost us a
// live bug:
//
//	shipTierForID        -> 1     the tier every ship reported when it was hardcoded
//	purchasePriceForItem -> 1000  what the Athos charged instead of 400,000
//
// Neither is visible in a payload: a hull that fell back is byte-identical to a
// real Tier 1 hull, and a 1,000-credit fallback is byte-identical to a real
// 1,000-credit item. Both now log when they fire, but a warning in a log nobody
// reads is only half the fix. These tests are the other half -- they fail the
// build if anything we actually send reaches either fallback.

func TestNoShippedHullFallsBackToTierOne(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	const pid = "00000000000000000000000000000001"
	ships := realShipsOnly(playerOwnedTechTreeShips(pid))
	if len(ships) == 0 {
		t.Fatal("no owned ships; the test is not exercising anything")
	}

	fellBack := 0
	for _, ship := range ships {
		if _, derived := shipTierForIDChecked(ship.id); !derived {
			fellBack++
			name, _ := dreadconfig.AuthoritativeShipName(ship.id)
			t.Errorf("%s (%d) has no derivable tier, so it goes out as Tier 1 -- "+
				"indistinguishable from a real Tier 1 hull in the payload", name, ship.id)
		}
	}
	t.Logf("%d owned ships, %d on the fallback", len(ships), fellBack)
}

func TestEveryCatalogItemHasADerivedPrice(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	seeds := gatewayItemCatalogSeeds("")
	if len(seeds) == 0 {
		t.Fatal("empty catalog; the test is not exercising anything")
	}

	fellBack := 0
	for _, seed := range seeds {
		if _, derived := purchasePriceForItemChecked(seed.itemID); !derived {
			fellBack++
			t.Errorf("item %d is offered in the store but has no derivable price, so it "+
				"charges the flat 1000 -- the shelf and the till disagree again", seed.itemID)
		}
	}
	t.Logf("%d catalog items, %d on the fallback", len(seeds), fellBack)
}

// The till must charge what the shelf advertises. This is the invariant the
// fallback broke; it is asserted here against the derived value specifically, so
// that a future change which makes both sides fall back in the same way cannot
// keep the test green while charging the wrong number.
func TestCatalogShelfPriceEqualsDerivedTillPrice(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	for _, seed := range gatewayItemCatalogSeeds("") {
		price, derived := purchasePriceForItemChecked(seed.itemID)
		if !derived {
			continue // reported by TestEveryCatalogItemHasADerivedPrice
		}
		if seed.priceAmount != price {
			t.Errorf("item %d: store advertises %d, purchase charges %d",
				seed.itemID, seed.priceAmount, price)
		}
	}
}
