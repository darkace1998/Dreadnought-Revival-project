package main

import (
	"strings"
	"testing"
)

// The client renders a ship whose description it cannot read as
// "99933489263<DNT> Invalid Description Field in Json" -- its own SKU form of
// the id, followed by the error. Reported live for the Rurik; every entry in
// this catalog went out with an empty description.
func TestShipCatalogEntriesCarryTheirRealDescription(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	want := map[int32]string{
		33489263: "Rurik",  // the reported case
		33489262: "Agosta", // the other starter hulls travel the same path
		33489264: "Cerberus",
	}
	seen := map[int32]bool{}
	for _, seed := range gatewayItemCatalogSeeds("") {
		name, checked := want[seed.itemID]
		if !checked {
			continue
		}
		seen[seed.itemID] = true
		if strings.TrimSpace(seed.description) == "" {
			t.Errorf("%s (%d) has an empty description; the client will render the Invalid Description Field error",
				name, seed.itemID)
			continue
		}
		// The blueprint prose names the ship it describes.
		if !strings.Contains(seed.description, name) {
			t.Errorf("%s (%d) description does not mention it: %.80q", name, seed.itemID, seed.description)
		}
	}
	for id, name := range want {
		if !seen[id] {
			t.Errorf("%s (%d) is not in the item catalog at all", name, id)
		}
	}
}

// Eight hulls have no description in their blueprint, and no other source has
// one. They stay empty rather than getting invented prose -- the gap is honest,
// a fabricated description is not.
func TestHullsWithNoBlueprintDescriptionStayEmpty(t *testing.T) {
	// Kreshnik, ScoutHeavy T3: its blueprint's third FText is the shared
	// "A Sinley Bay blackmarket special!" subline, which the generator records
	// as absent rather than writing out as a description.
	if description := hullCatalogDescription(33489275); description != "" {
		t.Errorf("Kreshnik description = %q, want empty", description)
	}
}

// The store buckets label every one of their 6630 SKUs "<bucket> <sku>" with
// "<bucket> catalog item" for a description. Where the SKU encodes an item id --
// 999 followed by it, which is the shipped catalog's own convention -- the real
// name and description are available and should be used.
func TestCatalogSKUResolvesToTheRealItem(t *testing.T) {
	name, description := catalogSKUDisplay("un_typed", "99933489268")
	if name != "Furia" {
		t.Errorf("name = %q, want Furia", name)
	}
	if !strings.Contains(description, "Furia") {
		t.Errorf("description does not mention the ship: %.80q", description)
	}
}

// A SKU that encodes nothing we can resolve keeps the placeholder rather than
// getting a guess.
func TestUnresolvableCatalogSKUKeepsThePlaceholder(t *testing.T) {
	for _, sku := range []string{"ryantest0123", "99900001", "not-a-sku"} {
		name, description := catalogSKUDisplay("Weapons", sku)
		if name != "Weapons "+sku || description != "Weapons catalog item" {
			t.Errorf("sku %q resolved to %q/%q; unresolvable SKUs must keep the placeholder",
				sku, name, description)
		}
	}
}
