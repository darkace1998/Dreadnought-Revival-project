//nolint:goconst // Gateway bootstrap fixtures intentionally mirror protocol field names verbatim.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dreadnought-ps/mmogbrain/protocol"
	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
)

const (
	claimUserIDKey            = "user_id"
	testGatewayUserID         = "b7c42c0f-3ac6-48a1-82cc-fd35eb24f128"
	testNameBundles           = "bundles"
	gatewayKeyItemCatalogReal = "item_catalog_real"
	gatewayKeyCurrencyReal    = "currency_catalog_real"
	gatewayKeyItemCatalogVC   = "item_catalog_virtual"
	gatewayKeyCurrencyVC      = "currency_catalog_virtual"
	// The request key stays "bundles" (the endpoint name); the field the
	// payload carries them under is "Bundles", the single spelling the
	// client's catalog parser reads.
	gatewayKeyBundles    = "bundles"
	gatewayFieldBundles  = "Bundles"
	gatewayKeyOwnedItems = "owned_items"
	gatewayKeyWallet     = "wallet"
)

func gatewayTestClaims() jwt.MapClaims {
	return jwt.MapClaims{claimUserIDKey: testGatewayUserID}
}

func resetGatewayPlayerDataReady(t *testing.T, userID string) {
	t.Helper()

	setGatewayPlayerDataReadyState(userID, false)
	t.Cleanup(func() {
		setGatewayPlayerDataReadyState(userID, false)
	})
}

func setGatewayBootstrapWaitTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()

	previous := gatewayBootstrapPlayerDataReadyTimeout
	gatewayBootstrapPlayerDataReadyTimeout = timeout
	t.Cleanup(func() {
		gatewayBootstrapPlayerDataReadyTimeout = previous
	})
}

func decodeGatewayPayload(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode gateway payload: %v", err)
	}
	return payload
}

func TestGatewayPlayReturnsMmogConnectionInfo(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/play", nil)
	rec := httptest.NewRecorder()

	handleGWPlay(rec, req, gatewayTestClaims())
	payload := decodeGatewayPayload(t, rec)

	if got := payload["Code"]; got != float64(0) {
		t.Fatalf("Code = %v, want 0", got)
	}
	if got := payload[fieldStatus]; got != "ok" {
		t.Fatalf("status = %v, want ok", got)
	}
	if got := payload["serverHost"]; got == "" {
		t.Fatal("serverHost should be present")
	}
	if got := payload["serverPort"]; got == "" {
		t.Fatal("serverPort should be present")
	}
}

func waitForGatewayPlayerDataWaiter(t *testing.T, userID string, timeout time.Duration) {
	t.Helper()

	key := protocol.GatewayPlayerDataReadyKey(userID)
	deadline := time.Now().Add(timeout)
	for {
		gatewayPlayerDataReadyMu.Lock()
		state := gatewayPlayerDataReady[key]
		waiterCount := 0
		if state != nil {
			waiterCount = len(state.waiters)
		}
		gatewayPlayerDataReadyMu.Unlock()
		if waiterCount > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway bootstrap handler did not wait for player data readiness for %s", userID)
		}
		time.Sleep(time.Millisecond)
	}
}

type gatewayOwnedItemIdentity struct {
	externalID string
	itemType   string
	shipID     int32
	loadoutID  int32
}

func gatewayCatalogEntities(t *testing.T, payload map[string]any, key string) []any {
	t.Helper()

	catalog, ok := payload[key].(map[string]any)
	if !ok {
		if requestedCatalog, _ := payload["requested_catalog"].(string); requestedCatalog != key {
			t.Fatalf("%s has unexpected type %T", key, payload[key])
		}
		// There is no top-level "entities" any more: the client's catalog
		// parser reads Items/ItemOffers/ForexOffers and never entities, so
		// sending it was a third verbatim copy of the same objects. The
		// contents now come from whichever array this catalog kind populates.
		alias := "Items"
		if key == gatewayKeyCurrencyReal || key == gatewayKeyCurrencyVC {
			alias = "ForexOffers"
		}
		entities, ok := payload[alias].([]any)
		if !ok {
			t.Fatalf("%s has unexpected type %T", alias, payload[alias])
		}
		return entities
	}
	entities, ok := catalog["entities"].([]any)
	if !ok {
		t.Fatalf("%s.entities has unexpected type %T", key, catalog["entities"])
	}
	if len(entities) == 0 {
		t.Fatalf("%s.entities should not be empty", key)
	}
	return entities
}

func gatewayExpectedCatalogAliasCount(catalogKey string, alias string, entityCount int) int {
	switch catalogKey {
	case gatewayKeyItemCatalogReal, gatewayKeyItemCatalogVC:
		// Items is the definition list and ItemOffers is what the store
		// presents; the client's market grid and per-ship purchase data come
		// from the offers, so both carry every entity.
		if alias == "Items" || alias == "ItemOffers" {
			return entityCount
		}
	case gatewayKeyCurrencyReal, gatewayKeyCurrencyVC:
		if alias == "ForexOffers" {
			return entityCount
		}
	}
	return 0
}

func gatewayJSONMap(t *testing.T, value any, field string) map[string]any {
	t.Helper()

	entry, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s has unexpected type %T", field, value)
	}
	return entry
}

func gatewayJSONArray(t *testing.T, value any, field string) []any {
	t.Helper()

	items, ok := value.([]any)
	if !ok {
		t.Fatalf("%s has unexpected type %T", field, value)
	}
	return items
}

func gatewayJSONString(t *testing.T, entry map[string]any, field string) string {
	t.Helper()

	value, ok := entry[field].(string)
	if !ok {
		t.Fatalf("%s has unexpected type %T", field, entry[field])
	}
	return value
}

func gatewayJSONInt32(t *testing.T, value any, field string) int32 {
	t.Helper()

	switch typed := value.(type) {
	case float64:
		return int32(typed)
	case int32:
		return typed
	default:
		t.Fatalf("%s has unexpected type %T", field, value)
		return 0
	}
}

func gatewayOwnedInventoryIdentities(t *testing.T, payload map[string]any) map[int32]gatewayOwnedItemIdentity {
	t.Helper()

	ownedItems := gatewayJSONArray(t, payload[gatewayKeyOwnedItems], gatewayKeyOwnedItems)
	identities := make(map[int32]gatewayOwnedItemIdentity, len(ownedItems))
	for _, item := range ownedItems {
		entry := gatewayJSONMap(t, item, gatewayKeyOwnedItems+" item")
		itemID := gatewayJSONInt32(t, entry["item_id"], gatewayKeyOwnedItems+".item_id")
		identities[itemID] = gatewayOwnedItemIdentity{
			externalID: gatewayJSONString(t, entry, "external_id"),
			itemType:   gatewayJSONString(t, entry, "item_type"),
			shipID:     gatewayJSONInt32(t, entry["ship_id"], gatewayKeyOwnedItems+".ship_id"),
			loadoutID:  gatewayJSONInt32(t, entry["loadout_id"], gatewayKeyOwnedItems+".loadout_id"),
		}
	}
	return identities
}

func assertGatewayMarketIdentity(t *testing.T, entry map[string]any, fieldPrefix string) {
	t.Helper()

	externalID := gatewayJSONString(t, entry, "external_id")
	if externalID == "" {
		t.Fatalf("%s external_id should not be empty", fieldPrefix)
	}
	if strings.ContainsAny(externalID, `/\."'`) {
		t.Fatalf("%s external_id = %q, want client-safe non-asset identifier", fieldPrefix, externalID)
	}
	if sku := gatewayJSONString(t, entry, "Sku"); strings.ContainsAny(sku, `/\."'`) {
		t.Fatalf("%s Sku = %q, want client-safe non-asset identifier", fieldPrefix, sku)
	}
	itemID := gatewayJSONInt32(t, entry["item_id"], fieldPrefix+".item_id")
	entityID := gatewayJSONString(t, entry, "entity_id")
	if entityID != strconv.Itoa(int(itemID)) {
		t.Fatalf("%s entity_id = %q, want %d", fieldPrefix, entityID, itemID)
	}
}

func assertGatewayMarketMatchesOwned(t *testing.T, entry map[string]any, fieldPrefix string, owned gatewayOwnedItemIdentity) {
	t.Helper()

	assertGatewayMarketIdentity(t, entry, fieldPrefix)
	if got := gatewayJSONString(t, entry, "external_id"); got != owned.externalID {
		t.Fatalf("%s external_id = %q, want %q", fieldPrefix, got, owned.externalID)
	}
	if got := gatewayJSONString(t, entry, "Sku"); got != owned.externalID {
		t.Fatalf("%s Sku = %q, want %q", fieldPrefix, got, owned.externalID)
	}
	if got := gatewayJSONString(t, entry, "item_type"); got != owned.itemType {
		t.Fatalf("%s item_type = %q, want %q", fieldPrefix, got, owned.itemType)
	}
	if got := gatewayJSONInt32(t, entry["ship_id"], fieldPrefix+".ship_id"); got != owned.shipID {
		t.Fatalf("%s ship_id = %d, want %d", fieldPrefix, got, owned.shipID)
	}
	if got := gatewayJSONInt32(t, entry["loadout_id"], fieldPrefix+".loadout_id"); got != owned.loadoutID {
		t.Fatalf("%s loadout_id = %d, want %d", fieldPrefix, got, owned.loadoutID)
	}
}

func TestStarterInventorySeedsCoverShipsLoadoutsAndSlots(t *testing.T) {
	loadouts := starterShipLoadouts()
	playerGet := buildMmogPlayerGetPayload(defaultMmogPlayerPID)
	seeds := starterOwnedInventorySeeds()

	seenItemIDs := map[int32]struct{}{}
	typeCounts := map[string]int{}
	for _, item := range seeds {
		typeCounts[item.itemType]++
		if item.itemID != 0 {
			if _, exists := seenItemIDs[item.itemID]; exists {
				t.Fatalf("duplicate starter inventory item id %d", item.itemID)
			}
			seenItemIDs[item.itemID] = struct{}{}
		}
	}

	if got := typeCounts["ship"]; got != len(loadouts) {
		t.Fatalf("starter ship seed count = %d, want %d", got, len(loadouts))
	}
	if got := typeCounts["loadout"]; got != len(loadouts) {
		t.Fatalf("starter loadout seed count = %d, want %d", got, len(loadouts))
	}
	if got := typeCounts["weapon"]; got != len(loadouts)*2 {
		t.Fatalf("starter weapon seed count = %d, want %d", got, len(loadouts)*2)
	}
	if got := typeCounts["ability"]; got != len(loadouts)*4 {
		t.Fatalf("starter ability seed count = %d, want %d", got, len(loadouts)*4)
	}
	if got := typeCounts["perk"]; got != 0 {
		t.Fatalf("starter perk seed count = %d, want 0", got)
	}

	// Items is the owned-item inventory the client reads into the player-data
	// snapshot (+0x150/+0x158); without it the hangar shows nothing.
	if !bytes.Contains(playerGet, appendFieldMarker("Items", 0x0d)) {
		t.Fatal("YA_PlayerGet must include the owned-item Items array")
	}
}

func TestStarterInventorySeedsUseExtractedStarterIDs(t *testing.T) {
	loadouts := starterShipLoadouts()
	seeds := starterOwnedInventorySeeds()

	bySlot := map[string]mmogInventoryItemSeed{}
	byLoadoutID := map[int32]mmogInventoryItemSeed{}
	seenItemIDs := map[int32]struct{}{}
	for _, item := range seeds {
		seenItemIDs[item.itemID] = struct{}{}
		if item.itemType == "loadout" {
			byLoadoutID[item.loadoutID] = item
		}
		if item.slotName != "" {
			bySlot[strconv.Itoa(int(item.loadoutID))+":"+item.slotName] = item
		}
	}

	firstLoadout := loadouts[0]
	firstLoadoutSeed := byLoadoutID[firstLoadout.loadoutID()]
	if firstLoadoutSeed.externalID != extractedMarketItemExternalID(firstLoadout.loadoutID(), "") {
		t.Fatalf("%s loadout external_id = %q", firstLoadout.ship.name, firstLoadoutSeed.externalID)
	}
	if firstPrimary := bySlot[strconv.Itoa(int(firstLoadout.loadoutID()))+":weaponPrimary"]; firstPrimary.itemID != firstLoadout.weaponPrimaryItemID() {
		t.Fatalf("%s primary weapon id = %d, want %d", firstLoadout.ship.name, firstPrimary.itemID, firstLoadout.weaponPrimaryItemID())
	}
	secondLoadout := loadouts[1]
	if secondSecondary := bySlot[strconv.Itoa(int(secondLoadout.loadoutID()))+":weaponSecondary"]; secondSecondary.itemID != secondLoadout.weaponSecondaryItemID() {
		t.Fatalf("%s secondary weapon id = %d, want %d", secondLoadout.ship.name, secondSecondary.itemID, secondLoadout.weaponSecondaryItemID())
	}
	lastLoadout := loadouts[len(loadouts)-1]
	if primaryAbility := bySlot[strconv.Itoa(int(lastLoadout.loadoutID()))+":abilityPrimary"]; primaryAbility.externalID != extractedMarketItemExternalID(lastLoadout.abilityItemID(0), "") {
		t.Fatalf("%s primary ability external_id = %q", lastLoadout.ship.name, primaryAbility.externalID)
	}
	if _, ok := seenItemIDs[firstLoadout.loadoutID()*10+1]; ok {
		t.Fatalf("legacy fabricated starter id %d should not remain", firstLoadout.loadoutID()*10+1)
	}
}

func TestGatewayItemCatalogIsPopulatedWithLocalizationKeys(t *testing.T) {
	// The client builds the tech tree, the market grid and the loadout/vanity
	// pickers from catalog items. An empty catalog leaves all three blank and
	// makes it log "Could not find item for ship id ..." and
	// "Attempted to access index 0 from array MarketGridItems of length 0".
	seeds := gatewayItemCatalogSeeds("CR")
	if len(seeds) == 0 {
		t.Fatal("market item catalog is empty; the tech tree and market render blank without it")
	}

	// Each entry's lowercase "name" is a localization KEY, not a label: the
	// client resolves it against its own string tables and renders
	// "<DNT>[[NotFound]]" for anything that is not a real key. Keys are 32 hex
	// characters. An empty value is allowed for the few items with no known
	// key; a human-readable display name is never acceptable there.
	named := 0
	for _, seed := range seeds {
		name := gatewayMarketLocalizationName(seed)
		if name == "" {
			continue
		}
		named++
		if len(name) != 32 {
			t.Fatalf("item %d name %q is not a 32-character localization key", seed.itemID, name)
		}
		for _, c := range name {
			if !strings.ContainsRune("0123456789ABCDEFabcdef", c) {
				t.Fatalf("item %d name %q is not hex; a display name here renders as <DNT>[[NotFound]]", seed.itemID, name)
			}
		}
	}
	if named == 0 {
		t.Fatal("no catalog entry carries a localization key")
	}

	// The currency store stays empty: this server sells nothing, and the
	// player's balance is delivered by the separate wallet field.
	if seeds := gatewayCurrencyCatalogSeeds("CR", "CR"); len(seeds) != 0 {
		t.Fatalf("market currency catalog should be empty, got %d seeds", len(seeds))
	}
}

func TestGatewayItemCatalogSeedsUseExtractedIdentityMappings(t *testing.T) {
	seeds := gatewayItemCatalogSeeds("CR")

	for _, seed := range seeds {
		// issue #37: seeds sourced from the real CatalogIDTable.json buckets
		// (realCatalogBucketSeeds) aren't in the curated ItemIDRegister-based
		// identity mapping this test checks — they're a different, much
		// larger real registry entirely. Only the original curated starter
		// set (gateIdentity: true) is expected to resolve here.
		if !seed.gateIdentity {
			continue
		}
		meta, ok := extractedMarketItemMetadataForID(seed.itemID)
		if !ok {
			t.Fatalf("catalog seed %q item_id %d missing extracted identity metadata", seed.displayName, seed.itemID)
		}
		if meta.itemType != seed.itemType {
			t.Fatalf("catalog seed %q item_type = %q, want extracted %q", seed.displayName, seed.itemType, meta.itemType)
		}
	}
}

func TestGatewayBootstrapPayloadsStayStructurallyComplete(t *testing.T) {
	claims := gatewayTestClaims()
	resetGatewayPlayerDataReady(t, testGatewayUserID)
	setGatewayPlayerDataReadyState(testGatewayUserID, true)
	tests := []struct {
		name         string
		path         string
		handler      func(http.ResponseWriter, *http.Request, jwt.MapClaims)
		requestedKey string
	}{
		{name: "real items", path: "/api/v1/catalog/digital_items_rmt", handler: handleGWCatalog, requestedKey: gatewayKeyItemCatalogReal},
		{name: "virtual items", path: "/api/v1/catalog/digital_items_vc", handler: handleGWCatalog, requestedKey: gatewayKeyItemCatalogVC},
		{name: "real currency", path: "/api/v1/catalog/currency_pack_rmt", handler: handleGWCatalog, requestedKey: gatewayKeyCurrencyReal},
		{name: "virtual currency", path: "/api/v1/catalog/currency_pack_vc", handler: handleGWCatalog, requestedKey: gatewayKeyCurrencyVC},
		{name: testNameBundles, path: "/api/v1/bundles", handler: handleGWBundles, requestedKey: gatewayKeyBundles},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			tc.handler(rec, req, claims)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}

			var payload map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode gateway payload: %v", err)
			}

			for _, key := range []string{
				"Code",
				"catalog_version",
				"requested_catalog",
				"player_id",
				gatewayKeyOwnedItems,
				gatewayKeyWallet,
				"starter_ship_ids",
				"starter_loadout_ids",
			} {
				if _, ok := payload[key]; !ok {
					t.Fatalf("missing %s in gateway bootstrap payload", key)
				}
			}
			if got := payload["requested_catalog"]; got != tc.requestedKey {
				t.Fatalf("requested_catalog = %v, want %s", got, tc.requestedKey)
			}
			for _, key := range []string{"items", "catalog", "currencies", "offers"} {
				if _, ok := payload[key]; ok {
					t.Fatalf("unexpected top-level alias %s in %s payload", key, tc.name)
				}
			}
			if tc.requestedKey == gatewayKeyBundles {
				if _, ok := payload["entities"]; ok {
					t.Fatalf("unexpected top-level entities in %s payload", tc.name)
				}
				bundles := gatewayJSONArray(t, payload[gatewayFieldBundles], gatewayFieldBundles)
				if len(bundles) == 0 {
					t.Fatal("bundles should not be empty")
				}
				firstBundle := gatewayJSONMap(t, bundles[0], "first bundle")
				items := gatewayJSONArray(t, firstBundle["items"], "bundle items")
				if got := len(items); got != 0 {
					t.Fatalf("bundle item count = %d, want 0 to avoid duplicate FYItemData loads", got)
				}
			} else {
				if _, present := payload["entities"]; present {
					t.Fatalf("top-level entities must not be sent in %s: the catalog parser never reads it", tc.name)
				}
				nestedEntities := gatewayCatalogEntities(t, payload, tc.requestedKey)
				for _, alias := range []string{"Items", "ItemOffers", "ForexOffers"} {
					topLevelAlias := gatewayJSONArray(t, payload[alias], alias)
					wantCount := gatewayExpectedCatalogAliasCount(tc.requestedKey, alias, len(nestedEntities))
					if len(topLevelAlias) != wantCount {
						t.Fatalf("top-level %s count = %d, want %d for %s", alias, len(topLevelAlias), wantCount, tc.name)
					}
				}
			}
			for _, key := range []string{gatewayKeyItemCatalogReal, gatewayKeyCurrencyReal, gatewayKeyItemCatalogVC, gatewayKeyCurrencyVC, gatewayKeyBundles} {
				_, exists := payload[key]
				if key != tc.requestedKey && exists {
					t.Fatalf("unexpected unrequested collection %s in %s payload", key, tc.name)
				}
				if key != tc.requestedKey || key == gatewayKeyBundles {
					continue
				}
				for _, item := range gatewayCatalogEntities(t, payload, key) {
					entry := gatewayJSONMap(t, item, "catalog entity")
					if entry["entity_type"] == "item" {
						assertGatewayMarketIdentity(t, entry, "catalog item")
					}
				}
				for _, alias := range []string{"Items", "ItemOffers", "ForexOffers"} {
					got := len(gatewayJSONArray(t, payload[alias], alias))
					want := gatewayExpectedCatalogAliasCount(key, alias, len(gatewayCatalogEntities(t, payload, key)))
					if got != want {
						t.Fatalf("%s.%s count = %d, want %d in %s payload", key, alias, got, want, tc.name)
					}
				}
			}
			ownedItems, ok := payload[gatewayKeyOwnedItems].([]any)
			if !ok {
				t.Fatalf("owned_items has unexpected type %T", payload[gatewayKeyOwnedItems])
			}
			if got := len(ownedItems); got != len(starterOwnedInventorySeeds()) {
				t.Fatalf("owned item count = %d, want %d", got, len(starterOwnedInventorySeeds()))
			}
		})
	}
}

func TestGatewayBootstrapOwnedInventoryAlignsWithMarketEntities(t *testing.T) {
	claims := gatewayTestClaims()
	resetGatewayPlayerDataReady(t, testGatewayUserID)
	setGatewayPlayerDataReadyState(testGatewayUserID, true)
	tests := []struct {
		name         string
		path         string
		handler      func(http.ResponseWriter, *http.Request, jwt.MapClaims)
		requestedKey string
	}{
		{name: "real items", path: "/api/v1/catalog/digital_items_rmt", handler: handleGWCatalog, requestedKey: gatewayKeyItemCatalogReal},
		{name: "virtual items", path: "/api/v1/catalog/digital_items_vc", handler: handleGWCatalog, requestedKey: gatewayKeyItemCatalogVC},
		{name: testNameBundles, path: "/api/v1/bundles", handler: handleGWBundles, requestedKey: gatewayKeyBundles},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			tc.handler(rec, req, claims)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}

			var payload map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode gateway payload: %v", err)
			}

			ownedByItemID := gatewayOwnedInventoryIdentities(t, payload)
			overlapCount := 0

			if tc.requestedKey != gatewayKeyBundles {
				for _, item := range gatewayCatalogEntities(t, payload, tc.requestedKey) {
					entry := gatewayJSONMap(t, item, "catalog entity")
					if entry["entity_type"] != "item" {
						continue
					}
					itemID := gatewayJSONInt32(t, entry["item_id"], "catalog item.item_id")
					owned, ok := ownedByItemID[itemID]
					if !ok {
						continue
					}
					assertGatewayMarketMatchesOwned(t, entry, "catalog item", owned)
					overlapCount++
				}
				// The real-money item catalog is empty by design: the two item
				// catalogs are split by the currency an item is priced in, and
				// nothing this server sells costs real money. They used to
				// return the same 62 items, which listed every item twice once
				// MarketManager concatenated the five catalogs.
				if tc.requestedKey == gatewayKeyItemCatalogReal {
					if overlapCount != 0 {
						t.Fatalf("real-money catalog carried %d credit-priced entries; it must only hold RMT-priced items", overlapCount)
					}
					return
				}
				// Every piece of starter gear appears in both the catalog
				// and owned_items, and the two must agree on identity --
				// assertGatewayMarketMatchesOwned above checks that per entry.
				if overlapCount == 0 {
					t.Fatal("no catalog entry overlaps the player's owned inventory; the catalog and owned_items have diverged")
				}
				return
			}

			for _, bundle := range gatewayJSONArray(t, payload[gatewayFieldBundles], gatewayFieldBundles) {
				bundleEntry := gatewayJSONMap(t, bundle, "bundle")
				if got := len(gatewayJSONArray(t, bundleEntry["items"], "bundle items")); got != 0 {
					t.Fatalf("bundle item count = %d, want 0 to avoid duplicate market item rows", got)
				}
			}
		})
	}
}

func TestGatewayBootstrapPinsSharedStarterIdentityListsAndOwnedMetadata(t *testing.T) {
	claims := gatewayTestClaims()
	resetGatewayPlayerDataReady(t, testGatewayUserID)
	setGatewayPlayerDataReadyState(testGatewayUserID, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/digital_items_rmt", nil)
	rec := httptest.NewRecorder()
	handleGWCatalog(rec, req, claims)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode gateway payload: %v", err)
	}

	shipIDs := gatewayJSONArray(t, payload["starter_ship_ids"], "starter_ship_ids")
	wantShipIDs := dreadconfig.StarterInventoryShipIDs()
	if len(shipIDs) != len(wantShipIDs) {
		t.Fatalf("starter_ship_ids count = %d, want %d", len(shipIDs), len(wantShipIDs))
	}
	for idx, wantID := range wantShipIDs {
		if got := gatewayJSONInt32(t, shipIDs[idx], "starter_ship_ids"); got != wantID {
			t.Fatalf("starter_ship_ids[%d] = %d, want %d", idx, got, wantID)
		}
	}

	loadoutIDs := gatewayJSONArray(t, payload["starter_loadout_ids"], "starter_loadout_ids")
	wantLoadoutIDs := dreadconfig.StarterInventoryLoadoutIDs()
	if len(loadoutIDs) != len(wantLoadoutIDs) {
		t.Fatalf("starter_loadout_ids count = %d, want %d", len(loadoutIDs), len(wantLoadoutIDs))
	}
	for idx, wantID := range wantLoadoutIDs {
		if got := gatewayJSONInt32(t, loadoutIDs[idx], "starter_loadout_ids"); got != wantID {
			t.Fatalf("starter_loadout_ids[%d] = %d, want %d", idx, got, wantID)
		}
	}

	ownedByItemID := gatewayOwnedInventoryIdentities(t, payload)
	if len(ownedByItemID) != len(dreadconfig.StarterInventoryItems()) {
		t.Fatalf("owned item count = %d, want %d", len(ownedByItemID), len(dreadconfig.StarterInventoryItems()))
	}
	for _, item := range dreadconfig.StarterInventoryItems() {
		owned, ok := ownedByItemID[item.Item.ItemID]
		if !ok {
			t.Fatalf("owned inventory missing shared item %d", item.Item.ItemID)
		}
		wantExternalID := extractedMarketItemExternalID(item.Item.ItemID, "")
		if owned.externalID != wantExternalID {
			t.Fatalf("owned item %d external_id = %q, want %q", item.Item.ItemID, owned.externalID, wantExternalID)
		}
		if strings.ContainsAny(owned.externalID, `/\."'`) {
			t.Fatalf("owned item %d external_id = %q, want client-safe non-asset identifier", item.Item.ItemID, owned.externalID)
		}
		if owned.itemType != item.Item.ItemType {
			t.Fatalf("owned item %d item_type = %q, want %q", item.Item.ItemID, owned.itemType, item.Item.ItemType)
		}
		if owned.shipID != item.ShipID {
			t.Fatalf("owned item %d ship_id = %d, want %d", item.Item.ItemID, owned.shipID, item.ShipID)
		}
		if owned.loadoutID != item.LoadoutID {
			t.Fatalf("owned item %d loadout_id = %d, want %d", item.Item.ItemID, owned.loadoutID, item.LoadoutID)
		}
	}
}

func TestGatewayBootstrapOwnedItemsWaitForPlayerData(t *testing.T) {
	claims := gatewayTestClaims()
	resetGatewayPlayerDataReady(t, testGatewayUserID)
	setGatewayBootstrapWaitTimeout(t, 20*time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/digital_items_rmt", nil)
	rec := httptest.NewRecorder()
	handleGWCatalog(rec, req, claims)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode gateway payload before YA_PlayerGet: %v", err)
	}

	ownedItems := gatewayJSONArray(t, payload[gatewayKeyOwnedItems], gatewayKeyOwnedItems)
	if len(ownedItems) != 0 {
		t.Fatalf("owned item count before YA_PlayerGet = %d, want 0", len(ownedItems))
	}
	if starterShips := gatewayJSONArray(t, payload["starter_ship_ids"], "starter_ship_ids"); len(starterShips) != len(dreadconfig.StarterInventoryShipIDs()) {
		t.Fatalf("starter_ship_ids count before YA_PlayerGet = %d, want %d", len(starterShips), len(dreadconfig.StarterInventoryShipIDs()))
	}
	if starterLoadouts := gatewayJSONArray(t, payload["starter_loadout_ids"], "starter_loadout_ids"); len(starterLoadouts) != len(dreadconfig.StarterInventoryLoadoutIDs()) {
		t.Fatalf("starter_loadout_ids count before YA_PlayerGet = %d, want %d", len(starterLoadouts), len(dreadconfig.StarterInventoryLoadoutIDs()))
	}

	state := &mmogConnState{playerPID: protocol.NormalizePlayerPID(testGatewayUserID)}
	if err := handlePlayerGetSatisfied(logrus.New(), &captureConn{}, "test-remote", nil, false, state, "client-request"); err != nil {
		t.Fatalf("handlePlayerGetSatisfied: %v", err)
	}

	// Answering YA_PlayerGet is not on its own enough to release the gateway:
	// the client must come back for more, proving it drained the player-data
	// frame. Simulate that next read.
	if err := processMmogAppFrames(logrus.New(), &captureConn{}, "test-remote", nil, nil, false, state); err != nil {
		t.Fatalf("processMmogAppFrames (client resume): %v", err)
	}

	rec = httptest.NewRecorder()
	handleGWCatalog(rec, req, claims)

	if rec.Code != http.StatusOK {
		t.Fatalf("status after YA_PlayerGet = %d, want %d", rec.Code, http.StatusOK)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode gateway payload after YA_PlayerGet: %v", err)
	}

	ownedItems = gatewayJSONArray(t, payload[gatewayKeyOwnedItems], gatewayKeyOwnedItems)
	if got := len(ownedItems); got != len(starterOwnedInventorySeeds()) {
		t.Fatalf("owned item count after YA_PlayerGet = %d, want %d", got, len(starterOwnedInventorySeeds()))
	}
	if starterShips := gatewayJSONArray(t, payload["starter_ship_ids"], "starter_ship_ids"); len(starterShips) != len(dreadconfig.StarterInventoryShipIDs()) {
		t.Fatalf("starter_ship_ids count after YA_PlayerGet = %d, want %d", len(starterShips), len(dreadconfig.StarterInventoryShipIDs()))
	}
	if starterLoadouts := gatewayJSONArray(t, payload["starter_loadout_ids"], "starter_loadout_ids"); len(starterLoadouts) != len(dreadconfig.StarterInventoryLoadoutIDs()) {
		t.Fatalf("starter_loadout_ids count after YA_PlayerGet = %d, want %d", len(starterLoadouts), len(dreadconfig.StarterInventoryLoadoutIDs()))
	}
}

func TestGatewayBootstrapHandlersWaitForPlayerDataReady(t *testing.T) {
	claims := gatewayTestClaims()
	tests := []struct {
		name    string
		path    string
		handler func(http.ResponseWriter, *http.Request, jwt.MapClaims)
	}{
		{name: testNameBundles, path: "/api/v1/bundles", handler: handleGWBundles},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetGatewayPlayerDataReady(t, testGatewayUserID)
			setGatewayBootstrapWaitTimeout(t, 200*time.Millisecond)

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				tc.handler(rec, req, claims)
				close(done)
			}()

			waitForGatewayPlayerDataWaiter(t, testGatewayUserID, 50*time.Millisecond)
			setGatewayPlayerDataReadyState(testGatewayUserID, true)

			select {
			case <-done:
			case <-time.After(100 * time.Millisecond):
				t.Fatal("gateway bootstrap handler did not resume after player data became ready")
			}

			payload := decodeGatewayPayload(t, rec)
			if got := len(gatewayJSONArray(t, payload[gatewayKeyOwnedItems], gatewayKeyOwnedItems)); got != len(starterOwnedInventorySeeds()) {
				t.Fatalf("owned item count after quick readiness = %d, want %d", got, len(starterOwnedInventorySeeds()))
			}
			if starterShips := gatewayJSONArray(t, payload["starter_ship_ids"], "starter_ship_ids"); len(starterShips) != len(dreadconfig.StarterInventoryShipIDs()) {
				t.Fatalf("starter_ship_ids count after quick readiness = %d, want %d", len(starterShips), len(dreadconfig.StarterInventoryShipIDs()))
			}
			if starterLoadouts := gatewayJSONArray(t, payload["starter_loadout_ids"], "starter_loadout_ids"); len(starterLoadouts) != len(dreadconfig.StarterInventoryLoadoutIDs()) {
				t.Fatalf("starter_loadout_ids count after quick readiness = %d, want %d", len(starterLoadouts), len(dreadconfig.StarterInventoryLoadoutIDs()))
			}
		})
	}
}

func TestGatewayBootstrapHandlersFallbackWhenPlayerDataReadyTimesOut(t *testing.T) {
	claims := gatewayTestClaims()
	tests := []struct {
		name    string
		path    string
		handler func(http.ResponseWriter, *http.Request, jwt.MapClaims)
	}{
		{name: testNameBundles, path: "/api/v1/bundles", handler: handleGWBundles},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetGatewayPlayerDataReady(t, testGatewayUserID)
			timeout := 30 * time.Millisecond
			setGatewayBootstrapWaitTimeout(t, timeout)

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			start := time.Now()
			tc.handler(rec, req, claims)
			elapsed := time.Since(start)

			if elapsed < timeout {
				t.Fatalf("gateway bootstrap handler returned after %v, want at least %v wait", elapsed, timeout)
			}
			if elapsed > 250*time.Millisecond {
				t.Fatalf("gateway bootstrap handler returned after %v, want brief timeout fallback", elapsed)
			}

			payload := decodeGatewayPayload(t, rec)
			if got := len(gatewayJSONArray(t, payload[gatewayKeyOwnedItems], gatewayKeyOwnedItems)); got != 0 {
				t.Fatalf("owned item count after timeout fallback = %d, want 0", got)
			}
			if starterShips := gatewayJSONArray(t, payload["starter_ship_ids"], "starter_ship_ids"); len(starterShips) != len(dreadconfig.StarterInventoryShipIDs()) {
				t.Fatalf("starter_ship_ids count after timeout fallback = %d, want %d", len(starterShips), len(dreadconfig.StarterInventoryShipIDs()))
			}
			if starterLoadouts := gatewayJSONArray(t, payload["starter_loadout_ids"], "starter_loadout_ids"); len(starterLoadouts) != len(dreadconfig.StarterInventoryLoadoutIDs()) {
				t.Fatalf("starter_loadout_ids count after timeout fallback = %d, want %d", len(starterLoadouts), len(dreadconfig.StarterInventoryLoadoutIDs()))
			}
			if tc.name == "catalog" {
				if len(gatewayCatalogEntities(t, payload, gatewayKeyItemCatalogReal)) == 0 {
					t.Fatal("item_catalog_real.entities should stay populated after timeout fallback")
				}
			} else if len(gatewayJSONArray(t, payload[gatewayFieldBundles], gatewayFieldBundles)) == 0 {
				t.Fatal("bundles should stay populated after timeout fallback")
			}
		})
	}
}

// The client processes HTTP callbacks well ahead of mmog frames (observed
// live: market catalog handled on UE4 frame 96, "Player Data Received" only on
// frame 105). If the gateway answers the market catalog as soon as we *send*
// player data, the client's market-complete handler runs OnUpdateInventory
// before it has player data and logs "Inventory of player data not yet
// initialized!". Readiness must therefore be signalled only once the client
// comes back for more.
func TestGatewayCatalogWaitsForClientToResumeAfterPlayerGet(t *testing.T) {
	resetGatewayPlayerDataReady(t, testGatewayUserID)
	pid := protocol.NormalizePlayerPID(testGatewayUserID)
	state := &mmogConnState{playerPID: pid}

	if err := handlePlayerGetSatisfied(logrus.New(), &captureConn{}, "test-remote", nil, false, state, "client-request"); err != nil {
		t.Fatalf("handlePlayerGetSatisfied: %v", err)
	}
	if gatewayPlayerDataReadyForUser(pid) {
		t.Fatal("gateway must not be released just because the YA_PlayerGet response was written")
	}

	if err := processMmogAppFrames(logrus.New(), &captureConn{}, "test-remote", nil, nil, false, state); err != nil {
		t.Fatalf("processMmogAppFrames (client resume): %v", err)
	}
	if !gatewayPlayerDataReadyForUser(pid) {
		t.Fatal("gateway should be released once the client reads again after player data")
	}
}

// TestGatewayWalletReflectsPersistedBalance guards the player's balances.
//
// The wallet is the only place the client learns them: the YA_PlayerGet parser
// reads no currency field at all. This returned a hardcoded 10000/0/0 for every
// player, so anything spent or earned was persisted but never displayed.
func TestGatewayWalletReflectsPersistedBalance(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const playerPID = "16161616161616161616161616161616"

	// Seed the player, then move the balance away from the old constant.
	_ = buildMmogPlayerGetPayload(playerPID)
	database := currentMmogPlayerStateDB()
	if _, err := database.Exec(
		`UPDATE player_state SET soft_currency=?, premium_currency=?, free_xp=? WHERE user_id=?`,
		1234, 56, 789, normalizedPlayerStatePID(playerPID),
	); err != nil {
		t.Fatalf("seed balances: %v", err)
	}

	wallet := gatewayWalletSnapshot(playerPID)
	for _, tc := range []struct {
		key  string
		want int32
	}{{"CR", 1234}, {"RMT", 56}, {"FreeXp", 789}} {
		got, ok := wallet[tc.key].(int32)
		if !ok {
			t.Fatalf("wallet[%s] = %v (%T), want an int32", tc.key, wallet[tc.key], wallet[tc.key])
		}
		if got != tc.want {
			t.Fatalf("wallet[%s] = %d, want %d — the wallet is not reading persisted state", tc.key, got, tc.want)
		}
	}
}

// TestGatewayMarketOfferCarriesTheFieldsTheClientReads pins the offer schema to
// what the client's own loaders parse, rather than to what looks plausible.
//
// FYItemOfferData::Load (0x142a6d760) reads exactly the fields listed below and
// nothing else, and FYItemData::Load (0x142a6d020) reads Name/Flags/
// GrantedCurrency/ImgUrl*. Before this, the payload carried 62 keys per offer
// and hit 4 of the offer loader's 18: every price the store showed came out as
// "hard: 0 soft: 0 real: 0.00" in the client log because Price/CurrencyAmount/
// prices[] -- which is where the prices were -- are read by nothing.
// TestGatewayPayloadsHaveNoCaseCollidingKeys covers the top level of every
// catalog response for the same FName hazard the per-offer check covers.
func TestGatewayPayloadsHaveNoCaseCollidingKeys(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	for _, key := range []string{
		gatewayKeyItemCatalogVC, gatewayKeyItemCatalogReal,
		gatewayKeyCurrencyVC, gatewayKeyCurrencyReal, gatewayKeyBundles,
	} {
		seen := map[string]string{}
		for field := range gatewayBootstrapPayload(defaultMmogPlayerPID, key, true) {
			lower := strings.ToLower(field)
			if other, clash := seen[lower]; clash {
				t.Errorf("%s payload sends both %q and %q; FName lookups are case-insensitive", key, other, field)
			}
			seen[lower] = field
		}
	}
}

func TestGatewayMarketOfferCarriesTheFieldsTheClientReads(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	seeds := gatewayItemCatalogSeedsForCurrency(defaultMmogPlayerPID, gatewayVirtualCurrencyIDs)
	if len(seeds) == 0 {
		t.Fatal("virtual-currency catalog is empty")
	}
	// Round-trip through JSON so this asserts against the bytes the client
	// actually receives, not the Go map.
	raw, err := json.Marshal(gatewayMarketEntities(seeds, true))
	if err != nil {
		t.Fatalf("marshal market entities: %v", err)
	}
	var entities []any
	if err := json.Unmarshal(raw, &entities); err != nil {
		t.Fatalf("decode market entities: %v", err)
	}
	first := gatewayJSONMap(t, entities[0], "offer")

	for _, field := range []string{
		"CRPrice", "CRCurrency", "SPPrice", "SPCurrency", "RCPrice", "RCCurrency", "RCSymbol",
		"OriginalPrice", "PriceID", "PromotionFlags", "ExpirationTime",
		"ProvidedCredits", "ProvidedPoints", "ItemIDs",
		"ItemID", "IsNew", "DoNotDisplayInStore", "full_image_url",
	} {
		if _, ok := first[field]; !ok {
			t.Errorf("offer is missing %q, which FYItemOfferData::Load reads", field)
		}
	}

	// UE resolves these through FNames and FName comparison is case-insensitive,
	// so two spellings of one field collide in the parsed object and one
	// silently wins. Never send both.
	for _, entity := range entities {
		entry := gatewayJSONMap(t, entity, "offer")
		seen := map[string]string{}
		for key := range entry {
			lower := strings.ToLower(key)
			if other, clash := seen[lower]; clash {
				t.Fatalf("offer sends both %q and %q; FName lookups are case-insensitive so one silently overwrites the other", other, key)
			}
			seen[lower] = key
		}
	}

	// A priced item must report that price where the client looks for it, and
	// OriginalPrice must match so no phantom discount is derived.
	priced := 0
	for _, entity := range entities {
		entry := gatewayJSONMap(t, entity, "offer")
		credits := gatewayJSONInt32(t, entry["CRPrice"], "CRPrice")
		if credits == 0 {
			continue
		}
		priced++
		if got := gatewayJSONInt32(t, entry["OriginalPrice"], "OriginalPrice"); got != credits {
			t.Fatalf("OriginalPrice = %d, want it equal to CRPrice %d", got, credits)
		}
		if entry["PriceID"] == "price_free" {
			t.Fatalf("item priced at %d credits still reports PriceID price_free", credits)
		}
	}
	if priced == 0 {
		t.Fatal("no offer carries a credit price; the store would show everything as free")
	}

	// Debug loadouts must not be listed in a storefront.
	for _, entity := range entities {
		entry := gatewayJSONMap(t, entity, "offer")
		name, _ := entry["display_name"].(string)
		if gatewayMarketItemIsDevelopmentAsset(name) && entry["DoNotDisplayInStore"] != true {
			t.Fatalf("development asset %q is displayed in the store", name)
		}
	}
}
