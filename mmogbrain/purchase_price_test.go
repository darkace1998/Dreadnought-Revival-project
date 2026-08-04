package main

import "testing"

// The shelf and the till have to agree. They did not: the catalog prices every
// entry with gatewayMarketCreditPrice(itemType, tier), while the purchase path
// read a hand-written map of about twenty ids and charged a flat 1000 for
// everything else.
//
//	Athos            store 400,000   charged 1,000
//	Repeater Turrets store   5,000   charged 2,000
//
// Nobody could have noticed before now: until AGENT-CHAT S10.3 there was no way
// to put credits on an account, so nothing had ever been bought.
func TestEveryCatalogItemIsChargedThePriceItAdvertises(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	mismatches := 0
	for _, seed := range gatewayItemCatalogSeeds("") {
		charged := purchasePriceForItem(seed.itemID)
		if charged == seed.priceAmount {
			continue
		}
		mismatches++
		if mismatches <= 10 {
			t.Errorf("%s (%d): store shows %d credits, purchase charges %d",
				seed.displayName, seed.itemID, seed.priceAmount, charged)
		}
	}
	if mismatches > 10 {
		t.Errorf("...and %d more", mismatches-10)
	}
}

// The tier drives the price, so the tier fixes have to be visible at the till
// too -- a T5 hull sold for the T1 fee is the same bug wearing a different hat.
func TestHullPurchasePricesFollowTheirTier(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	for _, tc := range []struct {
		id    int32
		name  string
		price int32
	}{
		{33489262, "Agosta", 25000}, // T1
		{33489297, "Aion", 200000},  // T4
		{33489300, "Athos", 400000}, // T5
		{33489303, "Zmey", 400000},  // T5
	} {
		if got := purchasePriceForItem(tc.id); got != tc.price {
			t.Errorf("%s (%d) costs %d, want %d", tc.name, tc.id, got, tc.price)
		}
	}
}

// An id the catalog does not carry must still get a price rather than a zero,
// or it would be free.
func TestUncatalogedItemsStillCostSomething(t *testing.T) {
	for _, id := range []int32{1, 999999, 184483950} {
		if got := purchasePriceForItem(id); got <= 0 {
			t.Errorf("item %d costs %d; nothing may be free by accident", id, got)
		}
	}
}
