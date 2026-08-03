package main

import "testing"

// Ship PAWN ids must not reach the store.
//
// A live client turned ten YPawn offers into exactly ten
// "GetCategoryImagePath: Unhandled loadout vanity slot type <0>" warnings and
// no other category produced one: the UI cannot classify a pawn for a store
// tile, so it falls into the vanity overload with an unset slot. The game's own
// CatalogIDTable agrees — 6630 SKUs, not one of them a YPawn id. See
// marketCatalogSellsCategory for the full evidence.
func TestItemCatalogNeverOffersShipPawnIDs(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	pawns := []int32{}
	for _, seed := range gatewayItemCatalogSeeds("") {
		if (seed.itemID>>24)&0xff == mmogItemCategoryShipPawn {
			pawns = append(pawns, seed.itemID)
		}
	}
	if len(pawns) != 0 {
		t.Errorf("item catalog offers %d ship pawn ids (%v); the client renders no icon for them "+
			"and the shipped catalog never sold one", len(pawns), pawns)
	}
}

// The filter must not take the ships with it. Hulls are sold as precast
// loadouts (category 1), which is how the real store did it and the path that
// produces no warning — dropping those would empty the ship store instead of
// fixing its icons.
func TestItemCatalogStillOffersHullsAsPrecastLoadouts(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	byID := map[int32]gatewayCatalogEntitySeed{}
	loadouts := 0
	for _, seed := range gatewayItemCatalogSeeds("") {
		byID[seed.itemID] = seed
		if (seed.itemID>>24)&0xff == mmogItemCategoryShipLoadoutPrecast {
			loadouts++
		}
	}
	if loadouts == 0 {
		t.Fatal("no precast loadout entries left in the catalog; the ship store is empty")
	}
	// The starter hulls specifically, since they are the ones a new account sees.
	for id, name := range map[int32]string{33489262: "Agosta", 33489263: "Rurik", 33489264: "Cerberus"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("%s (%d) is no longer in the catalog", name, id)
		}
	}
}

// Three hulls in the store were sold as Tier 1 at the Tier 1 price because
// ItemIDRegister points their ids at the previous build's tier-less asset. The
// store is where a player sees the number and pays the price, so the catalog
// asserts it too and not just the config package.
func TestStoreHullsCarryTheirRealTierAndPrice(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	seeds := map[int32]gatewayCatalogEntitySeed{}
	for _, seed := range gatewayItemCatalogSeeds("") {
		seeds[seed.itemID] = seed
	}
	for _, tc := range []struct {
		id   int32
		name string
		tier int32
	}{
		{33489315, "Athos", 5},
		{33489318, "Zmey", 5},
		{33489331, "Aion", 4},
		{33489262, "Agosta", 1}, // unchanged: its path states T1
	} {
		seed, ok := seeds[tc.id]
		if !ok {
			t.Errorf("%s (%d) is not in the catalog", tc.name, tc.id)
			continue
		}
		if got := gatewayMarketItemTier(tc.id); got != tc.tier {
			t.Errorf("%s (%d) tier = %d, want %d", tc.name, tc.id, got, tc.tier)
		}
		// The price is derived from the tier, so a wrong tier was also a wrong
		// price -- a T5 hull for the T1 fee.
		wantPrice := gatewayMarketCreditPrice(seed.itemType, tc.tier)
		if seed.priceAmount != wantPrice {
			t.Errorf("%s (%d) price = %d, want %d (tier %d)",
				tc.name, tc.id, seed.priceAmount, wantPrice, tc.tier)
		}
	}
}
