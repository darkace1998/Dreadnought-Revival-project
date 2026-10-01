package main

import (
	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// Every documented bundle resolves its heroes from the client's data, is
// Featured, references its contents, and costs more GP than nothing.
func TestMarketBundlesAreRealAndFeatured(t *testing.T) {
	seeds := marketBundleCatalogSeeds()
	if len(seeds) != len(marketBundles) {
		t.Fatalf("%d bundle offers for %d bundles", len(seeds), len(marketBundles))
	}
	featured := 0
	defer func() {
		// The Featured section shows at most four offers.
		if featured == 0 || featured > 4 {
			t.Errorf("%d featured bundles, want 1-4", featured)
		}
	}()
	for i, b := range marketBundles {
		heroes, _ := b.contents()
		if len(heroes) != len(b.heroes) {
			t.Errorf("%s: %d of %d heroes resolve (%v)", b.name, len(heroes), len(b.heroes), b.heroes)
		}
		e := gatewayMarketEntity(seeds[i], true)
		// The section comes from PromotionFlagSet (0x142a7e1a0), by name.
		set, _ := e["PromotionFlagSet"].([]any)
		if isFeatured := len(set) == 1 && set[0] == "featured"; isFeatured != b.featured || len(set) > 1 {
			t.Errorf("%s: PromotionFlagSet %v, featured %v", b.name, set, b.featured)
		}
		if b.featured {
			featured++
		}
		// "name" replaces "Name" in the client, so it is the localized
		// object with the bundle's name, and "description" has its text.
		if loc, _ := e["name"].(dreadconfig.Localized); loc["en"] != b.name {
			t.Errorf("%s: name %v, want {en: %q}", b.name, e["name"], b.name)
		}
		if loc, _ := e["description"].(dreadconfig.Localized); loc["en"] == "" {
			t.Errorf("%s: no localized description", b.name)
		}
		if e["full_image_url"] == "" {
			t.Errorf("%s: no picture", b.name)
		}
		if ids, _ := e["ItemIDs"].([]any); len(ids) == 0 {
			t.Errorf("%s: references no items", b.name)
		}
		if b.priceGP() <= 0 {
			t.Errorf("%s: price %d", b.name, b.priceGP())
		}
	}
}

// Bundles are full offers of the virtual currency catalog -- where the
// client converts them AND takes the list of bundles to keep -- and nowhere
// else, so none is offered twice.
func TestBundlesAreVirtualCurrencyCatalogOffers(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	for _, seed := range gatewayItemCatalogSeeds("") {
		if seed.entityType == "bundle" {
			t.Errorf("bundle %s is in the item catalog too", seed.displayName)
		}
	}
	listing := gatewayBootstrapPayload("", "currency_catalog_virtual", true)["entities"].([]any)
	if len(listing) != len(marketBundles) {
		t.Fatalf("%d bundle offers, want %d", len(listing), len(marketBundles))
	}
	for _, e := range listing {
		offer := e.(map[string]any)
		// What the client read and found missing in the bare listing.
		for _, field := range []string{"ID", "prices", "PromotionFlagSet", "entity_type", "external_id"} {
			if _, ok := offer[field]; !ok {
				t.Errorf("bundle offer %v lacks %s", offer["external_id"], field)
			}
		}
		// The client parses the offer id as an int32.
		if id, err := strconv.ParseInt(offer["entity_id"].(string), 10, 32); err != nil || id <= 0 {
			t.Errorf("bundle offer id %v does not fit an int32", offer["entity_id"])
		}
		if _, ok := offer["currency"].([]any); !ok {
			t.Errorf("bundle offer %v lacks its currency array", offer["external_id"])
		}
		// The bundle converter takes the contents from items[].external_id.
		if items, _ := offer["items"].([]any); len(items) == 0 {
			t.Errorf("bundle offer %v lists no items", offer["external_id"])
		} else if _, ok := items[0].(map[string]any)["external_id"].(string); !ok {
			t.Errorf("bundle offer %v items carry no external_id string", offer["external_id"])
		}
		if offer["entity_id"] != offer["external_id"] {
			t.Errorf("bundle offer entity_id %v != external_id %v", offer["entity_id"], offer["external_id"])
		}
	}
}

// Buying a bundle charges its GP price once and grants its heroes (as ships),
// cosmetics, credits and Elite days -- and grants no GP, so a bundle can never
// pay for itself.
func TestBuyingABundleGrantsItsContents(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	b, _ := marketBundleByID(marketBundleIDBase + 2) // Renegade Stash: Zaratan + Venomous set
	if _, err := database.Exec(`UPDATE player_state SET premium_currency=?, soft_currency=0 WHERE user_id=?`, b.priceGP(), pid); err != nil {
		t.Fatal(err)
	}
	buy := func() string {
		req := protocol.AppendStringField(nil, "RT", "YA_PurchaseItem")
		req = append(req, protocol.AppendStringField(nil, "offer", "999"+strconv.Itoa(int(b.id)))...)
		return protocol.ExtractStringField(buildMmogPurchasePayload("YA_PurchaseItem", pid, protocol.AppendRootEnd(req)), "result")
	}
	if got := buy(); got != "bought" {
		t.Fatalf("bundle purchase: %q", got)
	}
	var gp, credits int32
	_ = database.QueryRow(`SELECT premium_currency, soft_currency FROM player_state WHERE user_id=?`, pid).Scan(&gp, &credits)
	if gp != 0 || credits != b.credits {
		t.Errorf("after buying: GP %d (want 0, no GP granted) credits %d (want %d)", gp, credits, b.credits)
	}
	heroes, vanity := b.contents()
	owned := map[int32]bool{}
	for _, l := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(pid), pid) {
		owned[l.precastLoadoutID] = true
	}
	for _, id := range heroes {
		if !owned[id] {
			t.Errorf("hero %d not granted as a ship", id)
		}
	}
	have := map[int32]bool{}
	for _, id := range ownedPurchaseItemIDs(pid) {
		have[id] = true
	}
	for _, id := range vanity {
		if !have[id] {
			t.Errorf("cosmetic %d not granted", id)
		}
	}
	if membershipExpiresAt(pid) == 0 {
		t.Error("no Elite days granted")
	}
	if got := buy(); got == "bought" {
		t.Error("a second purchase with 0 GP went through")
	}
}

// Prices from the documented store: coatings 350 GP, heroes by tier, and the
// GP-to-credits rate 1:105.
func TestDocumentedMarketPrices(t *testing.T) {
	for _, v := range vanityCatalogSeeds(map[int32]struct{}{}) {
		if (v.itemID>>24)&0xff == 22 {
			if v.priceAmount != coatingPrice {
				t.Errorf("coating %d costs %d, want %d", v.itemID, v.priceAmount, coatingPrice)
			}
			break
		}
	}
	for _, h := range heroShipLoadouts {
		if p, _ := purchasePriceForItemChecked(h.loadoutID); p != heroPriceGP(h.tier) {
			t.Errorf("hero %s charges %d, the Market shows %d", h.name, p, heroPriceGP(h.tier))
		}
	}
	if gatewayGpToCreditsRate != 105 {
		t.Errorf("GP to credits rate %d, want 105", gatewayGpToCreditsRate)
	}
	if len(heroCatalogSeeds("")) != len(heroShipLoadouts) {
		t.Error("not every hero ship is offered")
	}
}

// Every hero offer carries the client's own text and, where the client has
// one, its picture; the picture is served by the gateway.
func TestHeroOffersCarryTextAndPictures(t *testing.T) {
	pictures := 0
	for _, seed := range heroCatalogSeeds("") {
		e := gatewayMarketEntity(seed, true)
		name, _ := e["name"].(dreadconfig.Localized)
		desc, _ := e["description"].(dreadconfig.Localized)
		if name["en"] == "" || desc["en"] == "" {
			t.Errorf("hero %d: name %v description %v", seed.itemID, name, desc)
		}
		if seed.image != "" {
			pictures++
			if _, err := os.Stat(filepath.Join(dreadconfig.MarketImagesDir(), seed.image)); err != nil {
				t.Errorf("hero %d: picture %s missing: %v", seed.itemID, seed.image, err)
			}
		}
	}
	if pictures < 30 {
		t.Errorf("only %d hero pictures", pictures)
	}
	rec := httptest.NewRecorder()
	handleMarketImage(rec, httptest.NewRequest(http.MethodGet, marketImagePath+"UI_ScoutL_Hermes.png", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("serving a picture: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	rec = httptest.NewRecorder()
	handleMarketImage(rec, httptest.NewRequest(http.MethodGet, marketImagePath+"..%2fHeroMarketData.json", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("a path outside the pictures was served: %d", rec.Code)
	}
}

// The Bundles section shows a /bundles entry only if the virtual currency
// catalog lists its external_id as an entity_id (0x142a60370 -> 0x142a34700).
func TestEveryBundleIsListedForTheBundlesSection(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	listed := map[string]bool{}
	for _, e := range gatewayBootstrapPayload("", "currency_catalog_virtual", true)["entities"].([]any) {
		listed[e.(map[string]any)["entity_id"].(string)] = true
	}
	bundles := gatewayBootstrapPayload("", "bundles", true)["Bundles"].([]any)
	if len(bundles) < len(marketBundles) {
		t.Fatalf("%d bundles, want at least %d", len(bundles), len(marketBundles))
	}
	if listed["starter_bundle"] {
		t.Error("the synthetic Starter Bundle placeholder is listed")
	}
	for _, b := range bundles {
		id, _ := b.(map[string]any)["external_id"].(string)
		if id == "starter_bundle" {
			continue
		}
		if !listed[id] {
			t.Errorf("bundle %q is not listed, so the client drops it", id)
		}
	}
}
