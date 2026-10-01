package main

import "testing"

// Ship cosmetics reach the Market's ship section and the ship customization
// screen only through the promotion flags the client derives from an offer's
// referenced items (ItemOffer::SetPromotionFlagsFromOfferedItemsCollection).
// A ship cosmetic offer must reference its own item and carry ShipVanity;
// captain cosmetics, whose section works, stay as they were.
func TestShipCosmeticOffersReferenceTheirItem(t *testing.T) {
	seeds := vanityCatalogSeeds(map[int32]struct{}{})
	var shipSeen, captainSeen bool
	for _, s := range seeds {
		c := (s.itemID >> 24) & 0xff
		e := gatewayMarketEntity(s, true)
		ids, _ := e["ItemIDs"].([]any)
		switch {
		case c >= 20 && c <= 24 && !shipSeen:
			shipSeen = true
			if len(ids) != 1 || ids[0] != s.itemID {
				t.Errorf("ship cosmetic %d: ItemIDs %v, want [%d]", s.itemID, ids, s.itemID)
			}
			if e["PromotionFlags"] != promotionFlagShipVanity {
				t.Errorf("ship cosmetic %d: PromotionFlags %v, want ShipVanity (%d)", s.itemID, e["PromotionFlags"], promotionFlagShipVanity)
			}
		case c >= 50 && c <= 55 && !captainSeen:
			captainSeen = true
			if len(ids) != 0 || e["PromotionFlags"] != 0 {
				t.Errorf("captain cosmetic %d changed: ItemIDs %v flags %v", s.itemID, ids, e["PromotionFlags"])
			}
		}
	}
	if !shipSeen || !captainSeen {
		t.Fatalf("need both a ship and a captain cosmetic in the catalog (ship %v captain %v)", shipSeen, captainSeen)
	}
	t.Setenv("DN_SHIP_VANITY_OFFER_FIX", "0")
	for _, s := range seeds {
		if c := (s.itemID >> 24) & 0xff; c >= 20 && c <= 24 {
			e := gatewayMarketEntity(s, true)
			if ids, _ := e["ItemIDs"].([]any); len(ids) != 0 || e["PromotionFlags"] != 0 {
				t.Errorf("switch off: ship cosmetic still changed")
			}
			break
		}
	}
}
