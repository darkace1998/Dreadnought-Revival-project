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
			// CHANGED 2026-10-06: offers are per-ship (vanity_store.go). The
			// SHARED id comes first (the client resolves the section from
			// it), then the offer's own id, each once.
			shared := sharedGearID(s.itemID)
			if len(ids) < 1 || ids[0] != shared || !containsAny(ids, s.itemID) || len(ids) != len(uniqueAny(ids)) {
				t.Errorf("ship cosmetic %d: ItemIDs %v, want the shared %d first, its own id, no repeats", s.itemID, ids, shared)
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
	for _, s := range vanityCatalogSeeds(map[int32]struct{}{}) {
		if c := (s.itemID >> 24) & 0xff; c >= 20 && c <= 24 {
			e := gatewayMarketEntity(s, true)
			if ids, _ := e["ItemIDs"].([]any); len(ids) != 0 || e["PromotionFlags"] != 0 {
				t.Errorf("switch off: ship cosmetic still changed")
			}
			break
		}
	}
}

func containsAny(ids []any, id int32) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func uniqueAny(ids []any) map[any]bool {
	out := map[any]bool{}
	for _, x := range ids {
		out[x] = true
	}
	return out
}
