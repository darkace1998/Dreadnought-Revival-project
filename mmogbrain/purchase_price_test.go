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

// What a purchase is RECORDED as has to match what it was SOLD as, or ownership
// downstream reads a different kind of thing than the player bought.
//
// One item in the catalog failed this: Vulture Missiles (83825291) is an
// ability, and ItemIDTable has no row for it, so purchasedItemType fell through
// to a hardcoded "ship" -- the least likely answer and the most consequential
// one. The fallback is now the category law, which is total.
func TestPurchasesAreRecordedAsWhatTheyWereSoldAs(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	for _, seed := range gatewayItemCatalogSeeds("") {
		if got := purchasedItemType(seed.itemID); got != seed.itemType {
			t.Errorf("%s (%d): sold as %q, recorded as %q",
				seed.displayName, seed.itemID, seed.itemType, got)
		}
	}
}

// The category law is the fallback, so it has to be right about the categories
// the catalog actually contains, and honest about the ones it does not.
func TestCategoryLawItemTypes(t *testing.T) {
	for _, tc := range []struct {
		id   int32
		want string
		what string
	}{
		{33489262, "loadout", "precast loadout (category 1)"},
		{83825291, "ability", "the orphan that started this"},
		{100597772, "weapon", "weapon (category 5)"},
		{117374977, "perk", "perk (category 6)"},
		{184483950, "ship", "ship pawn (category 10)"},
		{0x14000001, "item", "a vanity category with no gameplay type"},
	} {
		if got := itemTypeFromCategoryLaw(tc.id); got != tc.want {
			t.Errorf("%s: id %d = %q, want %q", tc.what, tc.id, got, tc.want)
		}
	}
}

// The client may send a SKU instead of a numeric id. Every SKU the store
// advertises has to buy the thing it advertises, or the entry is decoration.
//
// 25 of 52 did not: itemIDFromPurchaseOffer scanned the T1 ships and the starter
// inventory and answered "missing ItemID for purchase" for everything else --
// every ability but the starter four, and every hull above Tier 1.
func TestEveryStoreSKUCanActuallyBeBought(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	for _, seed := range gatewayItemCatalogSeeds("") {
		if got := itemIDFromPurchaseOffer(seed.externalID); got != seed.itemID {
			t.Errorf("%s: SKU %q resolves to %d, want %d",
				seed.displayName, seed.externalID, got, seed.itemID)
		}
	}
}

// The id is recovered from the SKU, so a SKU we did not issue must not resolve.
// Otherwise the trailing number is an open door: name any id, pay its price or
// not, own it.
func TestForgedSKUsAreRejected(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	for _, offer := range []string{
		"loadout_free_33489300",     // right id, wrong slug
		"ship_athos_33489300",       // right id and slug, wrong type prefix
		"loadout_athos_33489300_x",  // trailing junk
		"loadout_athos_0",           // zero
		"loadout_athos_-33489300",   // negative
		"_33489300",                 // no name at all
		"loadout_athos_99999999999", // overflows int32
	} {
		if got := itemIDFromPurchaseOffer(offer); got != 0 {
			t.Errorf("forged SKU %q resolved to item %d; only SKUs this server issues may resolve", offer, got)
		}
	}
}
