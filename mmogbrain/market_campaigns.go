package main

import (
	"os"
	"time"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// Market campaigns: time-limited discounts, through the client's own campaign
// system (2026-11-03, the operator: a Halloween sale "until 24 november").
//
// The original store ran seasonal sales: "Previous Halloween items are
// discounted 50%" (Steam, "Halloween Update", 2019-10-23); "all previous
// Halloween-themed items ... at a reduced price" (October 2020 Update, live
// 2020-10-29 to the November update of 2020-11-24); "Previous seasonal vanity
// has returned at a discount!" (Release 27, 2021-10-20).
//
// What the client reads, verified in the exe. The catalog's root "Campaigns"
// is NOT read as sent: the catalog rebuild (0x142A5C800) overwrites it with
// campaigns it collects from the entities, so a campaign is carried on each
// discounted entity, in the original store's (Aviary) form:
//
//	entity   "discounts": [{campaign_id, campaign_name, discount,
//	         start_date, end_date, campaign_tags, prices: [{amount}]}]
//	         (0x142A5F4D0). Each discount registers its campaign once by
//	         campaign_id (0x142A59170 -> 0x142A5C220) and adds this entity
//	         (its entity_id) with prices[0].amount to the campaign's offers;
//	         campaign_tags are also added to the entity's PromotionFlagSet,
//	         i.e. its Market sections. An entity with an EMPTY "discounts"
//	         array has its PromotionFlagSet cleared, so the key is only sent
//	         when there is a discount.
//	convert  0x142A5AD90 makes the root "Campaigns" entry from it: ID, Name,
//	         Tags, the discount as DiscountPercent (0x142A7E070), Offers
//	         [{ID, DiscountedPrice}] (0x142A81480). The dates are not
//	         carried: the server ends a campaign by no longer sending it.
//	shop     UpdateOfferCampaignData (YShop.cpp, 0x140430A50) matches each
//	         Offers ID against the offer's own ID (case-insensitive), takes
//	         its DiscountedPrice, shows DiscountPercent (DiscountType 0, the
//	         converter's default, with a positive percent) and compares the
//	         Tags with "Hot", "New", "onSale" and "Limited". It warns when an
//	         offer's OriginalPrice is below its price, so offers keep
//	         OriginalPrice = price.
//
// The shop applies a campaign to every offer it matches, so the discount
// shows where the cosmetic is bought: the ship customizer and the captain
// screen. The Market has no On Sale tab (operator 2026-11-03: Featured,
// Currency, Bundles, Elite Status, Hero Ships), and a "featured" tag put ONE
// tile in Featured, nameless and without a picture (Featured tiles download
// one; cosmetics have only local icons): not used.
//
// "discount" is a FRACTION: 50 showed as "5000%" (operator 2026-11-03), so the
// client multiplies it by 100. DN_MARKET_CAMPAIGNS=0 sends none.

type marketCampaign struct {
	id, name   string
	start, end time.Time
	percent    int32
	// item reports whether a cosmetic (its shared id) is in the sale.
	item func(itemID int32) bool
}

// halloweenSaleItems are the earlier Halloween cosmetics, by asset name and
// the yearly notes: the Halloween 001/002 sets (Wild Hunt, Harvest, Mischief,
// Malevolence), the 2020 set (Halloween20: the Bastion coating and decal),
// the 2020 Ghost decal and coating (and Red Ghost), and the 2021 Metal Goblin
// decal and coating. Not in it: the Halloween tint (Mat_Tint_Halloween01:
// captain materials are free here, VanityItemIsFree, so everyone owns it),
// the 2020 NanoDoc outfit
// (in no captain template, so the store does not sell it,
// VanityItemIsSold), Memento Mori (November 2020) and the Havoc/Dead Horde
// rewards.
var halloweenSaleItems = map[int32]bool{
	385810656: true, // VAN_PN_Halloween001_DA  Wild Hunt
	385810657: true, // VAN_PN_Halloween002_DA  Harvest
	385810671: true, // VAN_PN_Halloween20_DA
	385810670: true, // VAN_PN_Ghost_DA
	385810672: true, // VAN_PN_RedGhost_DA
	385810678: true, // VAN_PN_Goblin_DA
	419365118: true, // VAN_DCL_Halloween001_DA  Mischief
	419365119: true, // VAN_DCL_Halloween002_DA  Malevolence
	419365141: true, // VAN_DCL_Halloween20_DA
	419365140: true, // VAN_DCL_Ghost_DA
	419365142: true, // VAN_DCL_RedGhost_DA
	419365149: true, // VAN_DCL_Goblin_DA
	419365150: true, // VAN_DCL_Goblin_Red_DA
}

var cet = time.FixedZone("CET", 3600)

// marketClock is the time campaigns are judged by (tests set it).
var marketClock = time.Now

var marketCampaigns = []marketCampaign{{
	id:      "halloween_2026",
	name:    "Halloween",
	start:   time.Date(2026, 10, 29, 0, 0, 0, 0, cet),
	end:     time.Date(2026, 11, 25, 0, 0, 0, 0, cet), // through 24 November
	percent: 50,
	item:    func(id int32) bool { return halloweenSaleItems[id] },
}}

func (c marketCampaign) active(now time.Time) bool { return !now.Before(c.start) && now.Before(c.end) }

func (c marketCampaign) discounted(price int32) int32 { return price * (100 - c.percent) / 100 }

// campaignForItem is the running campaign a cosmetic is in, if any. A ship
// cosmetic in its per-ship form is the shared item it stands for.
func campaignForItem(itemID int32, now time.Time) (marketCampaign, bool) {
	if os.Getenv("DN_MARKET_CAMPAIGNS") == "0" {
		return marketCampaign{}, false
	}
	if _, ok := dreadconfig.VanityItemByID(itemID); !ok && isShipVanityItemID(itemID) {
		itemID = sharedGearID(itemID)
	}
	for _, c := range marketCampaigns {
		if c.active(now) && c.item(itemID) {
			return c, true
		}
	}
	return marketCampaign{}, false
}

// campaignSeedItem is the cosmetic an offer seed sells: its own id, or for a
// per-ship offer the shared id it references first.
func campaignSeedItem(seed gatewayCatalogEntitySeed) int32 {
	if len(seed.offerItemIDs) > 0 {
		return seed.offerItemIDs[0]
	}
	return seed.itemID
}

// campaignTags are the shop's own campaign tags (0x140430A50).
var campaignTags = []any{"onSale", "Limited"}

// campaignDiscounts is the "discounts" field of a catalog entity in a running
// campaign, or nil (no field at all, see above).
func campaignDiscounts(seed gatewayCatalogEntitySeed) []any {
	if seed.itemType != "vanity" {
		return nil
	}
	c, ok := campaignForItem(campaignSeedItem(seed), marketClock())
	if !ok {
		return nil
	}
	return []any{map[string]any{
		"campaign_id":   c.id,
		"campaign_name": c.name,
		"discount":      float64(c.percent) / 100,
		"start_date":    c.start.UTC().Format(time.RFC3339),
		"end_date":      c.end.UTC().Format(time.RFC3339),
		"campaign_tags": campaignTags,
		"prices":        []any{map[string]any{"amount": c.discounted(seed.priceAmount)}},
	}}
}

// campaignShelfPrice is what the Market shows for an offer: its price, or the
// running campaign's price for it.
func campaignShelfPrice(seed gatewayCatalogEntitySeed) int32 {
	if c, ok := campaignForItem(campaignSeedItem(seed), marketClock()); ok {
		if isVanity, sold, _ := vanityOffer(campaignSeedItem(seed)); isVanity && sold {
			return c.discounted(seed.priceAmount)
		}
	}
	return seed.priceAmount
}
