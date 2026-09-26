package main

import (
	"os"
	"strconv"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// Cosmetics in the store (2026-09-26).
//
// Every ship and captain cosmetic the developers marked ready for players is
// listed, built from the client's own data assets (dreadconfig.VanityItems;
// data/vanity/VanityItems_cooked.jsonl) -- NOT from the retail catalog's
// "Captain Vanity"/"Coatings"/... offers, whose ids are storefront offer ids
// with no mapping to items (see asset_tables_validation.go).
//
// Pricing is the operator's decision: free body features and each hull's
// default look (dreadconfig.VanityItemIsFree), everything else vanityPrice
// credits. The assets carry no price.
//
// Why a cosmetic is only OWNED once bought (free ones for 0): ownership reaches
// the client only through the Items list in player data, and that list is
// capped by the client's 32768-byte receive ring (playerDataFrameBudget; an
// account owning every ship and module already overflows it). The parser
// (FUN_2A6CED0) resets the list before filling it, so it cannot be streamed in
// pieces, and nothing the client sends says which menu it is in. Owning all
// 1027 listed cosmetics up front would push modules out of that list; owning
// what a player actually chose grows it by one entry per choice.
const vanityPrice = 10000

// vanityOffer reports whether itemID is a cosmetic, and if so whether it is
// sold and at what price.
func vanityOffer(itemID int32) (isVanity, sold bool, price int32) {
	v, ok := dreadconfig.VanityItemByID(itemID)
	if !ok {
		return false, false, 0
	}
	if !dreadconfig.VanityItemIsSold(v) {
		return true, false, 0
	}
	if dreadconfig.VanityItemIsFree(v) {
		return true, true, 0
	}
	return true, true, vanityPrice
}

// vanityCategoryName is the store section a cosmetic appears under -- the
// retail catalog's own bucket names where one exists.
func vanityCategoryName(v dreadconfig.VanityItem) string {
	switch v.Category() {
	case 21:
		return "Emblems Collection"
	case 22:
		return "Coatings Collection"
	case 23:
		return "Patterns Collection"
	case 24:
		return "Decals Collection"
	case 20:
		return "Ship Parts"
	default:
		return "Captain Vanity"
	}
}

// vanityCatalogSeeds is the store entry for every listed cosmetic. owned marks
// the ones the player has bought.
//
// It adds ~1000 entries to the store catalog: the catalog JSON grows from ~0.25
// to ~4.9 MB (each entity goes out three times, as entities/Items/ItemOffers).
// That is HTTP, not the mmog receive ring, but if the store turns slow or
// misbehaves, DN_VANITY_STORE=0 lists no cosmetics; owned ones and purchases
// keep working.
func vanityCatalogSeeds(owned map[int32]struct{}) []gatewayCatalogEntitySeed {
	if os.Getenv("DN_VANITY_STORE") == "0" {
		return nil
	}
	var seeds []gatewayCatalogEntitySeed
	for _, v := range dreadconfig.VanityItems() {
		_, sold, price := vanityOffer(v.ItemID)
		if !sold {
			continue
		}
		_, have := owned[v.ItemID]
		seeds = append(seeds, gatewayCatalogEntitySeed{
			itemID: v.ItemID,
			// The client's own offer-id form ("999" + item id, CatalogIDTable's
			// SKU shape), which itemIDFromPurchaseOffer resolves back.
			externalID:      "999" + strconv.Itoa(int(v.ItemID)),
			displayName:     v.Name,
			localizationKey: v.HeadlineKey, // the asset's own name key; never a display name
			entityType:      "item",
			// The same type the purchase is recorded under (purchasedItemType);
			// the store section comes from vanityCategoryName.
			itemType:        "vanity",
			priceCurrencyID: "CR",
			priceAmount:     price,
			quantity:        1,
			owned:           have,
		})
	}
	return seeds
}

// isVanityItemID is the category law for cosmetics: 20-24 ship, 50-55 captain.
func isVanityItemID(itemID int32) bool {
	c := (itemID >> 24) & 0xff
	return (c >= 20 && c <= 24) || (c >= 50 && c <= 55)
}

// withoutVanity drops cosmetics from an id list that is not about them (the
// tech tree's PurchasesData / ProgressionData), keeping those replies' size
// independent of how many cosmetics a player owns.
func withoutVanity(ids []int32) []int32 {
	out := ids[:0:0]
	for _, id := range ids {
		if !isVanityItemID(id) {
			out = append(out, id)
		}
	}
	return out
}
