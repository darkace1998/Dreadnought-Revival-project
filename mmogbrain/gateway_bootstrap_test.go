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

	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
	"github.com/dreadnought-ps/mmogbrain/protocol"
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
	gatewayKeyBundles         = "bundles"
	gatewayKeyOwnedItems      = "owned_items"
	gatewayKeyWallet          = "wallet"
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
		entities, ok := payload["entities"].([]any)
		if !ok {
			t.Fatalf("entities has unexpected type %T", payload["entities"])
		}
		if len(entities) == 0 {
			t.Fatalf("entities should not be empty for %s", key)
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
		if alias == "Items" {
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

func gatewayJSONBool(t *testing.T, value any, field string) bool {
	t.Helper()

	typed, ok := value.(bool)
	if !ok {
		t.Fatalf("%s has unexpected type %T", field, value)
	}
	return typed
}

func gatewayCatalogEntityByItemID(t *testing.T, entities []any, itemID int32, field string) map[string]any {
	t.Helper()

	for _, item := range entities {
		entry := gatewayJSONMap(t, item, field)
		if gatewayJSONInt32(t, entry["item_id"], field+".item_id") == itemID {
			return entry
		}
	}
	t.Fatalf("%s missing item_id %d", field, itemID)
	return nil
}

func gatewayCatalogEntityByExternalID(t *testing.T, entities []any, externalID string, field string) map[string]any {
	t.Helper()

	for _, item := range entities {
		entry := gatewayJSONMap(t, item, field)
		if gatewayJSONString(t, entry, "external_id") == externalID {
			return entry
		}
	}
	t.Fatalf("%s missing external_id %q", field, externalID)
	return nil
}

func gatewayCatalogSeedByItemID(t *testing.T, seeds []gatewayCatalogEntitySeed, itemID int32) gatewayCatalogEntitySeed {
	t.Helper()

	for _, seed := range seeds {
		if seed.itemID == itemID {
			return seed
		}
	}
	t.Fatalf("gateway catalog seeds missing item_id %d", itemID)
	return gatewayCatalogEntitySeed{}
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

func assertGatewayMarketUICompatibilityFields(t *testing.T, entry map[string]any, fieldPrefix string, wantOwned bool, wantPrice string, wantCategory string, wantParentCategory string) {
	t.Helper()

	description := gatewayJSONString(t, entry, "Description")
	if description == "" {
		t.Fatalf("%s Description should not be empty", fieldPrefix)
	}
	if got := gatewayJSONString(t, entry, "CategoryIcon"); got != "" {
		t.Fatalf("%s CategoryIcon = %q, want empty string fallback", fieldPrefix, got)
	}
	if got := gatewayJSONString(t, entry, "CategoryName"); got != wantCategory {
		t.Fatalf("%s CategoryName = %q, want %q", fieldPrefix, got, wantCategory)
	}
	if got := gatewayJSONString(t, entry, "ParentCategoryName"); got != wantParentCategory {
		t.Fatalf("%s ParentCategoryName = %q, want %q", fieldPrefix, got, wantParentCategory)
	}
	if got := gatewayJSONString(t, entry, "CategoryDescription"); got != description {
		t.Fatalf("%s CategoryDescription = %q, want Description %q", fieldPrefix, got, description)
	}
	if got := gatewayJSONString(t, entry, "Price"); got != wantPrice {
		t.Fatalf("%s Price = %q, want %q", fieldPrefix, got, wantPrice)
	}
	if got := gatewayJSONBool(t, entry["IsOwned"], fieldPrefix+".IsOwned"); got != wantOwned {
		t.Fatalf("%s IsOwned = %t, want %t", fieldPrefix, got, wantOwned)
	}
	if got := gatewayJSONBool(t, entry["Owned"], fieldPrefix+".Owned"); got != wantOwned {
		t.Fatalf("%s Owned = %t, want %t", fieldPrefix, got, wantOwned)
	}
	if got := gatewayJSONBool(t, entry["bIsOwned"], fieldPrefix+".bIsOwned"); got != wantOwned {
		t.Fatalf("%s bIsOwned = %t, want %t", fieldPrefix, got, wantOwned)
	}
	if got := gatewayJSONInt32(t, entry["ActionAvailabilityIndex"], fieldPrefix+".ActionAvailabilityIndex"); got != 0 {
		t.Fatalf("%s ActionAvailabilityIndex = %d, want 0", fieldPrefix, got)
	}
	for _, field := range []string{"HasVideoPreview", "OnSale", "IsHeroShip", "HasVeteranStatus"} {
		if got := gatewayJSONBool(t, entry[field], fieldPrefix+"."+field); got {
			t.Fatalf("%s %s = true, want false", fieldPrefix, field)
		}
	}
	for _, field := range []string{"ItemStatsArray", "AdditionalTextArray", "HeroShipStatsArray", "PreviousItemStatsArray"} {
		if got := len(gatewayJSONArray(t, entry[field], fieldPrefix+"."+field)); got != 0 {
			t.Fatalf("%s %s length = %d, want 0", fieldPrefix, field, got)
		}
	}
	prices := gatewayJSONArray(t, entry["prices"], fieldPrefix+".prices")
	if len(prices) == 0 {
		t.Fatalf("%s prices should not be empty", fieldPrefix)
	}
	if got := gatewayJSONString(t, gatewayJSONMap(t, prices[0], fieldPrefix+".prices[0]"), "amount"); got != wantPrice {
		t.Fatalf("%s prices[0].amount = %q, want %q", fieldPrefix, got, wantPrice)
	}
}

func assertGatewayOwnershipFields(t *testing.T, entry map[string]any, fieldPrefix string, wantOwned bool) {
	t.Helper()

	if got := gatewayJSONBool(t, entry["IsOwned"], fieldPrefix+".IsOwned"); got != wantOwned {
		t.Fatalf("%s IsOwned = %t, want %t", fieldPrefix, got, wantOwned)
	}
	if got := gatewayJSONBool(t, entry["Owned"], fieldPrefix+".Owned"); got != wantOwned {
		t.Fatalf("%s Owned = %t, want %t", fieldPrefix, got, wantOwned)
	}
	if got := gatewayJSONBool(t, entry["bIsOwned"], fieldPrefix+".bIsOwned"); got != wantOwned {
		t.Fatalf("%s bIsOwned = %t, want %t", fieldPrefix, got, wantOwned)
	}
}

func assertGatewayInventoryIdentityFields(t *testing.T, entry map[string]any, fieldPrefix string, wantItemID int32, wantShipID int32, wantLoadoutID int32) {
	t.Helper()

	if got := gatewayJSONInt32(t, entry["item_id"], fieldPrefix+".item_id"); got != wantItemID {
		t.Fatalf("%s item_id = %d, want %d", fieldPrefix, got, wantItemID)
	}
	if got := gatewayJSONInt32(t, entry["ItemID"], fieldPrefix+".ItemID"); got != wantItemID {
		t.Fatalf("%s ItemID = %d, want %d", fieldPrefix, got, wantItemID)
	}
	if got := gatewayJSONInt32(t, entry["ship_id"], fieldPrefix+".ship_id"); got != wantShipID {
		t.Fatalf("%s ship_id = %d, want %d", fieldPrefix, got, wantShipID)
	}
	if got := gatewayJSONInt32(t, entry["ShipID"], fieldPrefix+".ShipID"); got != wantShipID {
		t.Fatalf("%s ShipID = %d, want %d", fieldPrefix, got, wantShipID)
	}
	if got := gatewayJSONInt32(t, entry["loadout_id"], fieldPrefix+".loadout_id"); got != wantLoadoutID {
		t.Fatalf("%s loadout_id = %d, want %d", fieldPrefix, got, wantLoadoutID)
	}
	if got := gatewayJSONInt32(t, entry["LoadoutID"], fieldPrefix+".LoadoutID"); got != wantLoadoutID {
		t.Fatalf("%s LoadoutID = %d, want %d", fieldPrefix, got, wantLoadoutID)
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

	if bytes.Contains(playerGet, appendFieldMarker("Items", 0x0d)) {
		t.Fatal("YA_PlayerGet should not include the legacy Items inventory array after payload trim")
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

func TestGatewayItemCatalogSeedsUseExtractedIdentityMappings(t *testing.T) {
	seeds := gatewayItemCatalogSeeds("CR")

	for _, seed := range seeds {
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
				bundles := gatewayJSONArray(t, payload[gatewayKeyBundles], gatewayKeyBundles)
				if len(bundles) == 0 {
					t.Fatal("bundles should not be empty")
				}
				firstBundle := gatewayJSONMap(t, bundles[0], "first bundle")
				items := gatewayJSONArray(t, firstBundle["items"], "bundle items")
				if got := len(items); got != 0 {
					t.Fatalf("bundle item count = %d, want 0 to avoid duplicate FYItemData loads", got)
				}
			} else {
				topLevelEntities := gatewayJSONArray(t, payload["entities"], "entities")
				nestedEntities := gatewayCatalogEntities(t, payload, tc.requestedKey)
				if len(topLevelEntities) != len(nestedEntities) {
					t.Fatalf("top-level entities count = %d, want %d for %s", len(topLevelEntities), len(nestedEntities), tc.name)
				}
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
				expectedOverlap := 0
				for _, item := range starterOwnedInventorySeeds() {
					if item.itemType != "ship" {
						expectedOverlap++
					}
				}
				if overlapCount != expectedOverlap {
					t.Fatalf("catalog overlap = %d, want %d", overlapCount, expectedOverlap)
				}
				return
			}

			for _, bundle := range gatewayJSONArray(t, payload[gatewayKeyBundles], gatewayKeyBundles) {
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

func TestGatewayCatalogEntitiesExposeMarketUICompatibilityFields(t *testing.T) {
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

	entities := gatewayCatalogEntities(t, payload, gatewayKeyItemCatalogReal)
	firstStarter := starterShipLoadouts()[0]
	sharedFirstStarter := sharedStarterLoadoutByID(t, firstStarter.loadoutID())
	starterLoadout := gatewayCatalogEntityByItemID(t, entities, firstStarter.loadoutID(), "item_catalog_real entity")
	assertGatewayMarketUICompatibilityFields(t, starterLoadout, "starter loadout catalog item", true, "0", "Loadouts", sharedFirstStarter.ShipName)

	for _, entity := range entities {
		entityMap, ok := entity.(map[string]any)
		if !ok {
			t.Fatalf("item_catalog_real entity type = %T, want object", entity)
		}
		if got := gatewayJSONString(t, entityMap, "item_type"); got == "ship" {
			t.Fatalf("item_catalog_real should not expose ship entitlement rows that the client treats as loadout vanity: %#v", entity)
		}
	}
}

func TestGatewayCatalogEntitiesUseExtractedCategoryBuckets(t *testing.T) {
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

	entities := gatewayCatalogEntities(t, payload, gatewayKeyItemCatalogReal)

	firstStarter := starterShipLoadouts()[0]
	starterWeapon := gatewayCatalogEntityByItemID(t, entities, firstStarter.weaponPrimaryItemID(), "starter weapon catalog item")
	sharedFirstStarter := sharedStarterLoadoutByID(t, firstStarter.loadoutID())
	if got := gatewayJSONString(t, starterWeapon, "CategoryName"); got != "Weapons" {
		t.Fatalf("starter weapon CategoryName = %q, want Weapons", got)
	}
	if got := gatewayJSONString(t, starterWeapon, "ParentCategoryName"); got != sharedFirstStarter.ShipName {
		t.Fatalf("starter weapon ParentCategoryName = %q, want %q", got, sharedFirstStarter.ShipName)
	}

	starterAbility := gatewayCatalogEntityByItemID(t, entities, firstStarter.abilityItemID(0), "starter ability catalog item")
	if got := gatewayJSONString(t, starterAbility, "CategoryName"); got != "Modules" {
		t.Fatalf("starter ability CategoryName = %q, want Modules", got)
	}
	if got := gatewayJSONString(t, starterAbility, "ParentCategoryName"); got != sharedFirstStarter.ShipName {
		t.Fatalf("starter ability ParentCategoryName = %q, want %q", got, sharedFirstStarter.ShipName)
	}
}

func TestGatewayBootstrapOwnedItemsWaitForPlayerData(t *testing.T) {
	claims := gatewayTestClaims()
	resetGatewayPlayerDataReady(t, testGatewayUserID)
	setGatewayBootstrapWaitTimeout(t, 20*time.Millisecond)
	firstStarter := starterShipLoadouts()[0]
	starterLoadoutSeed := gatewayCatalogSeedByItemID(t, gatewayItemCatalogSeeds("RMT"), firstStarter.loadoutID())

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
	if len(gatewayCatalogEntities(t, payload, gatewayKeyItemCatalogReal)) == 0 {
		t.Fatal("item_catalog_real.entities should still be populated before YA_PlayerGet")
	}
	entities := gatewayCatalogEntities(t, payload, gatewayKeyItemCatalogReal)
	starterLoadout := gatewayCatalogEntityByExternalID(t, entities, starterLoadoutSeed.externalID, "item_catalog_real entity")
	assertGatewayOwnershipFields(t, starterLoadout, "starter loadout before YA_PlayerGet", false)
	assertGatewayInventoryIdentityFields(t, starterLoadout, "starter loadout before YA_PlayerGet", firstStarter.loadoutID(), starterLoadoutSeed.shipID, firstStarter.loadoutID())

	state := &mmogConnState{playerPID: protocol.NormalizePlayerPID(testGatewayUserID)}
	if err := handlePlayerGetSatisfied(logrus.New(), &captureConn{}, "test-remote", nil, false, state, "client-request"); err != nil {
		t.Fatalf("handlePlayerGetSatisfied: %v", err)
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
	entities = gatewayCatalogEntities(t, payload, gatewayKeyItemCatalogReal)
	starterLoadout = gatewayCatalogEntityByItemID(t, entities, firstStarter.loadoutID(), "item_catalog_real entity")
	assertGatewayOwnershipFields(t, starterLoadout, "starter loadout after YA_PlayerGet", true)
	assertGatewayInventoryIdentityFields(t, starterLoadout, "starter loadout after YA_PlayerGet", firstStarter.loadoutID(), starterLoadoutSeed.shipID, firstStarter.loadoutID())
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
			} else if len(gatewayJSONArray(t, payload[gatewayKeyBundles], gatewayKeyBundles)) == 0 {
				t.Fatal("bundles should stay populated after timeout fallback")
			}
		})
	}
}
