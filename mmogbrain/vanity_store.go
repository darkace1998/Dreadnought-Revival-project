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
// Pricing is the operator's decision: every sold cosmetic costs vanityPrice in
// vanityCurrency (premium), the "free" defaults (dreadconfig.VanityItemIsFree)
// included. The assets carry no price.
//
// Why a cosmetic is only OWNED once bought: ownership reaches
// the client only through the Items list in player data, and that list is
// capped by the client's 32768-byte receive ring (playerDataFrameBudget; an
// account owning every ship and module already overflows it). The parser
// (FUN_2A6CED0) resets the list before filling it, so it cannot be streamed in
// pieces, and nothing the client sends says which menu it is in. Owning all
// 1027 listed cosmetics up front would push modules out of that list; owning
// what a player actually chose grows it by one entry per choice.
const vanityPrice = 100

// coatingPrice is a ship coating's original GP price (see vanityOffer).
const coatingPrice = 350

// vanityCurrency is what cosmetics are priced and charged in: premium currency
// (SP), the operator's choice (2026-09-28: every cosmetic 100 premium). The
// client shows a cosmetic's premium price (SPPrice, from an SP offer): with the
// credit price (CRPrice) it displayed 0 and refused the purchase as
// "insufficient funds" without ever sending it.
const vanityCurrency = "SP"

// Wallet names the client accepts in a YA_PurchaseItem reply's currency field
// (see buildMmogPurchasePayload). The catalog uses CR/SP internally (gatewayWireCurrencyID).
const (
	mmogCurrencyCredits = "CR"
	mmogCurrencyPremium = "SP_regular"
)

// vanityOffer reports whether itemID is a cosmetic, and if so whether it is
// sold and at what price.
//
// A ship cosmetic may arrive in its per-ship form (the customizer's, see
// vanity_ownership.go); it is the same item as its shared form.
func vanityOffer(itemID int32) (isVanity, sold bool, price int32) {
	v, ok := dreadconfig.VanityItemByID(itemID)
	if !ok && isShipVanityItemID(itemID) {
		v, ok = dreadconfig.VanityItemByID(sharedGearID(itemID))
	}
	if !ok {
		return false, false, 0
	}
	if !dreadconfig.VanityItemIsSold(v) {
		return true, false, 0
	}
	// Every sold cosmetic, the former free defaults included, costs the same
	// (operator, 2026-09-28) -- except ship coatings, whose original price is
	// documented: "350" GP per coating, for one manufacturer and class line
	// (Steam forum, "Skin Coatings"). The rest have no surviving price.
	if v.Category() == 22 {
		return true, true, coatingPrice
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
	// Off with its own switch, or with the older ship-offer switch, which
	// restores the plain shared-id offers (isShipVanityOffer).
	perShip := os.Getenv("DN_VANITY_PERSHIP_OFFERS") != "0" && os.Getenv("DN_SHIP_VANITY_OFFER_FIX") != "0"
	var seeds []gatewayCatalogEntitySeed
	for _, v := range dreadconfig.VanityItems() {
		_, sold, price := vanityOffer(v.ItemID)
		if !sold {
			continue
		}
		_, have := owned[v.ItemID]
		// The item's own name and description, in every language, as the
		// locale objects the Market converter reads (as for hero ships,
		// gateway_catalog.go). They went out as the asset's export name
		// ("Head_Male_A02") and a bare localization key with no description,
		// which the captain editor and Market showed (operator, 2026-10-06).
		// Only with English text: the localized object replaces "Name", and a
		// few items are named in other languages only.
		name, description, _ := dreadconfig.VanityItemText(v.ItemID)
		displayName := v.Name
		if en := name["en"]; en != "" {
			displayName = en
		} else {
			name = nil
		}
		if description["en"] == "" {
			description = dreadconfig.Localized{"en": displayName}
		}
		seed := gatewayCatalogEntitySeed{
			itemID: v.ItemID,
			// The client's own offer-id form ("999" + item id, CatalogIDTable's
			// SKU shape), which itemIDFromPurchaseOffer resolves back.
			externalID:           "999" + strconv.Itoa(int(v.ItemID)),
			displayName:          displayName,
			localizationKey:      v.HeadlineKey, // the asset's own name key; never a display name
			localizedName:        name,
			localizedDescription: description,
			entityType:           "item",
			// The same type the purchase is recorded under (purchasedItemType);
			// the store section comes from vanityCategoryName.
			itemType:        "vanity",
			priceCurrencyID: vanityCurrency,
			priceAmount:     price,
			quantity:        1,
			owned:           have,
		}
		if !perShip || !isShipVanityItemID(v.ItemID) {
			seeds = append(seeds, seed)
			continue
		}
		// Ship cosmetics, in the form the hangar customizer asks for: its
		// buy button (RequestVanityPurchaseData, 0x1404C3DB0) finds the
		// offer whose MAIN item is the per-ship id (FUN_14041F4C0 without
		// its deep search), and its price/owned display searches every id an
		// offer references (the deep search). Only shared-id offers existed,
		// so no ship cosmetic was ever bought from the customizer -- not one
		// purchase request in the logs (2026-10-06).
		//
		// A hull-specific one (mesh part, hull pattern) is offered as its
		// per-ship id, one offer per class it fits -- the same count as
		// before but for figureheads (one per size). One that fits every ship
		// (emblem, paint, decal) keeps its shared-id offer and references its
		// per-ship forms: an offer per class would add ~5 KB x 15 per cosmetic
		// to the store download. It is bought from the Market.
		//
		// The shared id stays FIRST among the referenced ids: the client
		// derives the Market section from them (see isShipVanityOffer).
		if classes := shipVanityClasses(v); classes != nil {
			for _, c := range classes {
				one := seed
				one.itemID = shipVanityClientID(v.ItemID, c)
				one.externalID = "999" + strconv.Itoa(int(one.itemID))
				one.offerItemIDs = []int32{v.ItemID}
				seeds = append(seeds, one)
			}
			continue
		}
		seed.offerItemIDs = []int32{v.ItemID}
		for c := int32(1); c <= 15; c++ {
			seed.offerItemIDs = append(seed.offerItemIDs, shipVanityClientID(v.ItemID, c))
		}
		seeds = append(seeds, seed)
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
