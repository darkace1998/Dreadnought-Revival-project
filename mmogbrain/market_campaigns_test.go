package main

import (
	"testing"
	"time"
)

func withMarketClock(t *testing.T, at time.Time) {
	t.Helper()
	old := marketClock
	marketClock = func() time.Time { return at }
	t.Cleanup(func() { marketClock = old })
}

// The sale rides on the catalog entities, in the form the client's converter
// collects campaigns from (market_campaigns.go).
func TestHalloweenCampaign(t *testing.T) {
	withMarketClock(t, time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))

	covered := map[int32]bool{}
	discounted := map[int32]int32{}
	for _, s := range vanityCatalogSeeds(nil) {
		e := gatewayMarketEntity(s, true)
		d, has := e["discounts"]
		onSale := halloweenSaleItems[campaignSeedItem(s)]
		if has != onSale {
			t.Fatalf("offer %d (%s): discounts present %v, in the sale %v", s.itemID, s.displayName, has, onSale)
		}
		if !has {
			continue
		}
		covered[campaignSeedItem(s)] = true
		disc := d.([]any)[0].(map[string]any)
		if disc["campaign_id"] == "" || disc["start_date"] == "" || disc["discount"] != 0.5 {
			t.Fatalf("offer %d discount = %v", s.itemID, disc)
		}
		amount := disc["prices"].([]any)[0].(map[string]any)["amount"].(int32)
		if amount != s.priceAmount/2 {
			t.Errorf("offer %d: discounted %d of %d", s.itemID, amount, s.priceAmount)
		}
		discounted[s.itemID] = amount
		// Not in Featured (one nameless tile without a picture, live), at
		// its full price: OriginalPrice = price, the campaign carries the
		// discount.
		for _, f := range e["PromotionFlagSet"].([]any) {
			if f == "featured" {
				t.Errorf("offer %d is in Featured", s.itemID)
			}
		}
		if e["OriginalPrice"] != int(s.priceAmount) {
			t.Errorf("offer %d OriginalPrice %v, price %d", s.itemID, e["OriginalPrice"], s.priceAmount)
		}
	}
	for id := range halloweenSaleItems {
		if !covered[id] {
			t.Errorf("Halloween item %d has no Market offer (not sold), so it is not in the sale", id)
		}
	}
	// Wild Hunt: a coating, 350 -> 175; Mischief: a decal, 100 -> 50.
	if got := discounted[385810656]; got != coatingPrice/2 {
		t.Errorf("Wild Hunt campaign price %d, want %d", got, coatingPrice/2)
	}
	if got := discounted[419365118]; got != vanityPrice/2 {
		t.Errorf("Mischief campaign price %d, want %d", got, vanityPrice/2)
	}

	// The purchase charges the campaign price, and only for sale items.
	if p, _ := purchasePriceForItemChecked(385810656); p != coatingPrice/2 {
		t.Errorf("Wild Hunt charged %d during the sale", p)
	}
	if p, _ := purchasePriceForItemChecked(385810444); p != coatingPrice { // The Reaper
		t.Errorf("The Reaper charged %d during the sale", p)
	}

	// After 24 November (CET) the sale is gone.
	withMarketClock(t, time.Date(2026, 11, 24, 23, 0, 0, 0, time.UTC))
	for _, s := range vanityCatalogSeeds(nil) {
		if _, has := gatewayMarketEntity(s, true)["discounts"]; has {
			t.Fatalf("offer %d still discounted after the sale", s.itemID)
		}
	}
	if p, _ := purchasePriceForItemChecked(385810656); p != coatingPrice {
		t.Errorf("Wild Hunt charged %d after the sale", p)
	}
}
