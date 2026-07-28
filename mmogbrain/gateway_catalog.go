package main

import (
	"strconv"
	"strings"

	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
)

type gatewayCatalogEntitySeed struct {
	itemID          int32
	externalID      string
	displayName     string
	description     string
	entityType      string
	itemType        string
	manufacturer    string
	shipID          int32
	loadoutID       int32
	priceCurrencyID string
	priceAmount     int32
	grantedCurrency string
	grantedAmount   int32
	owned           bool
	hidden          bool
	quantity        int32
	isNew           bool
	gateIdentity    bool
	bundleItems     []gatewayCatalogEntitySeed
}

func gatewayBootstrapPayload(playerID string, requestedCatalog string, playerDataReady bool) map[string]any {
	ownedItems := []any{}
	if playerDataReady {
		ownedItems = gatewayOwnedInventorySnapshot()
	}
	payload := map[string]any{
		"Code":                0,
		"catalog_version":     "starter-hangar-bootstrap-v6",
		"requested_catalog":   requestedCatalog,
		"player_id":           playerID,
		"wallet":              gatewayWalletSnapshot(),
		"owned_items":         ownedItems,
		"starter_ship_ids":    starterShipIDsForBootstrap(),
		"starter_loadout_ids": starterLoadoutIDsForBootstrap(),
	}
	if catalog := gatewayRequestedCatalogCollection(requestedCatalog, playerDataReady); catalog != nil {
		payload["entities"] = catalog["entities"]
		payload["Items"] = catalog["Items"]
		payload["ItemOffers"] = catalog["ItemOffers"]
		payload["ForexOffers"] = catalog["ForexOffers"]
	}
	if requestedCatalog == "bundles" {
		bundles := gatewayMarketEntities(gatewayBundleCatalogSeeds(), playerDataReady)
		payload["bundles"] = bundles
		payload["Bundles"] = bundles
	}
	return payload
}

func gatewayRequestedCatalogCollection(requestedCatalog string, playerDataReady bool) map[string]any {
	switch requestedCatalog {
	case "item_catalog_real":
		return gatewayItemCatalogCollection(gatewayMarketEntities(gatewayItemCatalogSeeds("RMT"), playerDataReady))
	case "item_catalog_virtual":
		return gatewayItemCatalogCollection(gatewayMarketEntities(gatewayItemCatalogSeeds("CR"), playerDataReady))
	case "currency_catalog_real":
		return gatewayCurrencyCatalogCollection(gatewayMarketEntities(gatewayCurrencyCatalogSeeds("RMT", "RMT"), playerDataReady))
	case "currency_catalog_virtual":
		return gatewayCurrencyCatalogCollection(gatewayMarketEntities(gatewayCurrencyCatalogSeeds("CR", "CR"), playerDataReady))
	default:
		return nil
	}
}

func starterShipIDsForBootstrap() []int32 {
	return dreadconfig.StarterInventoryShipIDs()
}

func starterLoadoutIDsForBootstrap() []int32 {
	return starterLoadoutIDs()
}

func gatewayItemCatalogCollection(entities []any) map[string]any {
	return map[string]any{
		"entities":    entities,
		"Items":       entities,
		"ItemOffers":  []any{},
		"ForexOffers": []any{},
	}
}

func gatewayCurrencyCatalogCollection(entities []any) map[string]any {
	return map[string]any{
		"entities":    entities,
		"Items":       []any{},
		"ItemOffers":  []any{},
		"ForexOffers": entities,
	}
}

func gatewayShipClassDisplayName(shipClass int32) string {
	// Ordinals match the decompiled ship-baseclass enum (FUN_140303fb0):
	// 0=Dreadnought, 1=Corvette, 2=ArtilleryCruiser, 3=TacticalCruiser,
	// 4=Destroyer — see response_types.go's starterShipArchetypes comment.
	switch shipClass {
	case 0:
		return "Dreadnought"
	case 1:
		return "Corvette"
	case 2:
		return "Artillery"
	case 3:
		return "Tactical"
	case 4:
		return "Destroyer"
	default:
		return ""
	}
}

func gatewayManufacturerDisplayName(manufacturer string) string {
	switch manufacturer {
	case "JupiterArms":
		return "Jupiter Arms"
	case "AkulaVektor":
		return "Akula Vektor"
	default:
		return manufacturer
	}
}

func gatewayMarketCategoryName(itemType string) string {
	switch itemType {
	case "ship":
		return "Ship"
	case "loadout":
		return "Loadout"
	case "weapon":
		return "Weapon"
	case "ability":
		return "Ability"
	case "perk":
		return "Perk"
	case "bundle":
		return "Bundle"
	case "currency_pack":
		return "Currency Pack"
	default:
		return itemType
	}
}

func gatewayShipByID(shipID int32) (mmogShipSeed, bool) {
	for _, ship := range allT1Ships() {
		if ship.id == shipID {
			return ship, true
		}
	}
	for _, ship := range starterBootstrapShips() {
		if ship.id == shipID {
			return ship, true
		}
	}
	return mmogShipSeed{}, false
}

func gatewayMarketCategoryMetadata(seed gatewayCatalogEntitySeed) (string, string, string, string) {
	categoryName := gatewayMarketCategoryName(seed.itemType)
	parentCategoryName := ""
	extractedMeta, hasExtractedMeta := extractedMarketItemMetadataForID(seed.itemID)
	if ship, ok := gatewayShipByID(seed.shipID); ok {
		if seed.itemType == "ship" {
			if shipClassName := gatewayShipClassDisplayName(ship.shipClass); shipClassName != "" {
				categoryName = shipClassName
			}
			parentCategoryName = gatewayManufacturerDisplayName(ship.manufacturer)
		} else {
			if hasExtractedMeta && extractedMeta.catalogBucket != "" {
				categoryName = extractedMeta.catalogBucket
			}
			parentCategoryName = ship.name
		}
	} else if seed.manufacturer != "" {
		parentCategoryName = gatewayManufacturerDisplayName(seed.manufacturer)
	} else if hasExtractedMeta && extractedMeta.catalogBucket != "" {
		categoryName = extractedMeta.catalogBucket
	}
	if categoryName == "" {
		categoryName = seed.displayName
	}
	categoryDescription := seed.description
	if categoryDescription == "" {
		categoryDescription = strings.TrimSpace(parentCategoryName + " " + categoryName)
		if categoryDescription == "" {
			categoryDescription = categoryName
		}
	}
	return "", categoryName, parentCategoryName, categoryDescription
}

func gatewayWalletSnapshot() map[string]any {
	return map[string]any{
		"CR":     10000,
		"RMT":    0,
		"FreeXp": 0,
	}
}

func gatewayOwnedInventorySnapshot() []any {
	items := starterOwnedInventorySeeds()
	result := make([]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"external_id": item.externalID,
			"item_id":     item.itemID,
			"item_type":   item.itemType,
			"ship_id":     item.shipID,
			"loadout_id":  item.loadoutID,
			"owned":       true,
		})
	}
	return result
}

// realCatalogBucketSeeds sources purchasable catalog entries from the real
// extracted client catalog (data/assets/CatalogIDTable.json) instead of a
// small hand-authored set (issue #37 — the real store catalog has 6630 SKUs
// across 12 buckets; the previous implementation covered 0 of them).
//
// The real SKU numbers (9-12 digits, e.g. 99984017220) don't fit in
// gatewayCatalogEntitySeed.itemID (int32) — but itemID is only used as an
// internal identity value (gatewayMarketIdentity derives "id"/"entity_id"
// from it as a string), while the client actually keys per-item attribution
// on the separate "Sku"/"external_id" field (confirmed in the companion
// bundle-attribution issue, #58). So each entry gets a small synthetic
// sequential itemID, while externalID carries the real SKU number/code
// faithfully as a string.
//
// No real price data exists anywhere in the extracted assets for these SKUs
// (confirmed in the issue) — defaultPrice is a placeholder per bucket type,
// not real pricing.
func realCatalogBucketSeeds(bucketName, itemType, entityType, priceCurrencyID string, defaultPrice int32, idBase int32) []gatewayCatalogEntitySeed {
	_ = dreadconfig.LoadCatalogIDTable()
	bucket, ok := dreadconfig.GetCatalogBucket(bucketName)
	if !ok {
		return nil
	}
	seeds := make([]gatewayCatalogEntitySeed, 0, len(bucket.ItemIDs))
	for i, id := range bucket.ItemIDs {
		var sku string
		switch v := id.Value.(type) {
		case int64:
			sku = strconv.FormatInt(v, 10)
		case string:
			sku = v
		default:
			continue
		}
		if sku == "" {
			continue
		}
		seeds = append(seeds, gatewayCatalogEntitySeed{
			itemID:          idBase + int32(i),
			externalID:      sku,
			displayName:     bucketName + " " + sku,
			description:     bucketName + " catalog item",
			entityType:      entityType,
			itemType:        itemType,
			priceCurrencyID: priceCurrencyID,
			priceAmount:     defaultPrice,
			owned:           false,
			quantity:        1,
		})
	}
	return seeds
}

// realCatalogBucketIDBase gives each real-catalog bucket a distinct,
// non-overlapping range of synthetic itemIDs (see realCatalogBucketSeeds).
var realCatalogBucketIDBase = map[string]int32{
	"Bundles":             19000000,
	"Weapons":             20000000,
	"Modules":             21000000,
	"Captain Vanity":      22000000,
	"Coatings Collection": 24000000,
	"Decals Collection":   25000000,
	"Emblems Collection":  26000000,
	"Patterns Collection": 27000000,
	"Code Redemptions":    28000000,
	"Heroships":           29000000,
	"GP to CR":            30000000,
	"un_typed":            31000000,
}

// gatewayItemCatalogSeeds returns the market (store) catalog contents.
//
// It is EMPTY on purpose. The client resolves each catalog entry's lowercase
// "name" as a LOCALIZATION KEY: FUN_142a80350 reads "name", looks it up via
// FUN_142a60670 against the client's own string tables, and writes the result
// to "Name", substituting the literal placeholder "<DNT>[[NotFound]]" when the
// lookup fails. The real backend sent 32-hex localization keys; we only have
// display names ("Agosta", "Repeater Turrets"), and there is no id -> key
// mapping anywhere in our data (ItemIDTable.json / ItemIDRegister.json contain
// zero such keys), so EVERY entry we list renders as "<DNT>[[NotFound]]" in the
// player's inventory — loadouts included. Filtering by item type does not help:
// a name that merely appears in DreadGame.locres appears there as a VALUE, not
// as a key, so it still fails the lookup.
//
// Nothing needs this list: the player's owned gear reaches the client through
// owned_items and YA_PlayerGet (loadouts, weapons, abilities, ships), and this
// private server has no purchasable store. So we advertise nothing rather than
// advertise items the client cannot name.
func gatewayItemCatalogSeeds(_ string) []gatewayCatalogEntitySeed {
	return nil
}

// gatewayCurrencyCatalogSeeds returns the currency (forex) store contents.
//
// Empty for the same reason as gatewayItemCatalogSeeds: the single synthetic
// "CR/RMT Starter Pack" entry (itemID 9000001) is not a real SKU, its name is
// not a localization key, and the client rendered it as "<DNT>[[NotFound]]".
// The player's balance is delivered by the separate "wallet" field, so nothing
// depends on listing a purchasable currency pack.
func gatewayCurrencyCatalogSeeds(_ string, _ string) []gatewayCatalogEntitySeed {
	return nil
}

func gatewayBundleCatalogSeeds() []gatewayCatalogEntitySeed {
	seeds := []gatewayCatalogEntitySeed{{
		itemID:          9100001,
		externalID:      "starter_bundle",
		displayName:     "Starter Bundle",
		description:     "Starter ships, loadouts, and equipped items",
		entityType:      "bundle",
		itemType:        "bundle",
		priceCurrencyID: "CR",
		priceAmount:     0,
		owned:           true,
		quantity:        1,
		gateIdentity:    true,
		// NOTE (issue #58): deliberately left empty. The client's bundle
		// loader (FUN_142a59b30/FUN_142a61790) cross-references each items[]
		// entry by external_id, but gatewayMarketEntity() builds a FULL
		// entity per bundle item — populating this with the same items
		// already sent via gatewayItemCatalogSeeds (the item/currency
		// catalog) causes duplicate FYItemData loads for the same IDs, per
		// TestGatewayBootstrapPayloadsStayStructurallyComplete/bundles and
		// TestGatewayBootstrapOwnedInventoryAlignsWithMarketEntities/bundles,
		// which assert this stays empty for exactly that reason.
	}}
	// issue #37: real bundle SKUs from the extracted catalog, in addition to
	// the synthetic Starter Bundle above. Real bundle SKUs also get an empty
	// items[] array for the same duplicate-FYItemData-load reason as the
	// Starter Bundle (see NOTE above) — we don't have real bundle contents
	// data to populate them with anyway.
	seeds = append(seeds, realCatalogBucketSeeds("Bundles", "bundle", "bundle", "CR", 1000, realCatalogBucketIDBase["Bundles"])...)
	return seeds
}

func gatewayMarketIdentity(seed gatewayCatalogEntitySeed, _ bool) (int32, int32, int32, string) {
	itemID := seed.itemID
	shipID := seed.shipID
	loadoutID := seed.loadoutID
	entityID := strconv.Itoa(int(seed.itemID))
	return itemID, shipID, loadoutID, entityID
}

func gatewayMarketEntities(seeds []gatewayCatalogEntitySeed, playerDataReady bool) []any {
	entities := make([]any, 0, len(seeds))
	for _, seed := range seeds {
		entities = append(entities, gatewayMarketEntity(seed, playerDataReady))
	}
	return entities
}

func gatewayMarketEntity(seed gatewayCatalogEntitySeed, playerDataReady bool) map[string]any {
	categoryIcon, categoryName, parentCategoryName, categoryDescription := gatewayMarketCategoryMetadata(seed)
	// Do NOT send server-side prices. The client already holds every item's
	// price/campaign definition in its own Content, and when our synthetic
	// price (e.g. the 500 placeholder on purchasable buckets) differs from the
	// client's local "original price" it logs a flood of
	// "UpdateOfferCampaignData | Original Price is lower than the offer price"
	// warnings and destabilises the shop (client crash observed while caching
	// the Heroships bucket, itemIDs 29000xxx). We only convey identity +
	// ownership; the client fills price/campaign from its own data. Price is
	// reported as free (0) uniformly so no campaign discount is ever computed.
	priceValue := "0"
	owned := playerDataReady && seed.owned
	itemID, shipID, loadoutID, entityID := gatewayMarketIdentity(seed, playerDataReady)
	price := map[string]any{
		"id":            "price_free",
		"PriceID":       "price_free",
		"PriceId":       "price_free",
		"price_id":      "price_free",
		"region_id":     "US",
		"amount":        priceValue,
		"currency_id":   seed.priceCurrencyID,
		"currency":      seed.priceCurrencyID,
		"currency_code": seed.priceCurrencyID,
	}
	bundleItems := make([]any, 0, len(seed.bundleItems))
	for _, item := range seed.bundleItems {
		bundleItems = append(bundleItems, gatewayMarketEntity(item, playerDataReady))
	}
	itemStatsArray := gatewayWeaponStatsArray(seed.itemID)
	entity := map[string]any{
		"ID":                  itemID,
		"Name":                seed.displayName,
		"Sku":                 seed.externalID,
		"ImgUrlS":             "",
		"ImgUrlM":             "",
		"ImgUrlL":             "",
		"Flags":               0,
		"id":                  entityID,
		"name":                seed.displayName,
		"display_name":        seed.displayName,
		"entity_id":           entityID,
		"external_id":         seed.externalID,
		"item_id":             itemID,
		"entity_ID":           itemID,
		"entity_type":         seed.entityType,
		"item_type":           seed.itemType,
		"Description":         seed.description,
		"description":         seed.description,
		"full_image_url":      "",
		"ImageURL":            "",
		"currency_id":         seed.priceCurrencyID,
		"quantity":            seed.quantity,
		"CategoryIcon":        categoryIcon,
		"CategoryName":        categoryName,
		"ParentCategoryName":  parentCategoryName,
		"CategoryDescription": categoryDescription,
		"GrantedCurrency": map[string]any{
			"Currency": seed.grantedCurrency,
			"Amount":   seed.grantedAmount,
		},
		"ItemID":                  itemID,
		"CurrencyCode":            seed.priceCurrencyID,
		"CurrencySymbol":          seed.priceCurrencyID,
		"CurrencyAmount":          priceValue,
		"Price":                   priceValue,
		"IsNew":                   seed.isNew,
		"DoNotDisplayInStore":     seed.hidden,
		"IsOwned":                 owned,
		"Owned":                   owned,
		"bIsOwned":                owned,
		"ActionAvailabilityIndex": 0,
		"HasVideoPreview":         false,
		"OnSale":                  false,
		"ItemStatsArray":          itemStatsArray,
		"AdditionalTextArray":     []any{},
		"IsHeroShip":              false,
		"HasVeteranStatus":        false,
		"HeroShipStatsArray":      []any{},
		"PreviousItemStatsArray":  []any{},
		"Manufacturer":            seed.manufacturer,
		"ship_id":                 shipID,
		"ShipID":                  shipID,
		"loadout_id":              loadoutID,
		"LoadoutID":               loadoutID,
		"PriceId":                 "price_free",
		"campaign_id":             "",
		"PromotionFlagSet":        []any{},
		"prices":                  []any{price},
		"items":                   bundleItems,
		"entities":                []any{},
		"entitlements":            []any{},
	}
	if seed.grantedCurrency != "" {
		entity["granted_currency_id"] = seed.grantedCurrency
		entity["granted_currency_amount"] = seed.grantedAmount
	}
	return entity
}

func gatewayWeaponStatsArray(itemID int32) []any {
	weapon, ok := dreadconfig.WeaponByID(itemID)
	if !ok {
		return []any{}
	}
	return []any{
		map[string]any{"stat_name": "DamageHigh", "stat_value": weapon.DamageHigh},
		map[string]any{"stat_name": "DamageMedium", "stat_value": weapon.DamageMedium},
		map[string]any{"stat_name": "DamageLow", "stat_value": weapon.DamageLow},
		map[string]any{"stat_name": "WeaponCooldownTime", "stat_value": weapon.WeaponCooldownTime},
		map[string]any{"stat_name": "AmmoMagazinSize", "stat_value": weapon.AmmoMagazinSize},
		map[string]any{"stat_name": "SpreadBaseValue", "stat_value": weapon.SpreadBaseValue},
		map[string]any{"stat_name": "SpreadMaxValue", "stat_value": weapon.SpreadMaxValue},
		map[string]any{"stat_name": "MaxRange", "stat_value": weapon.MaxRange},
		map[string]any{"stat_name": "SlotType", "stat_value": weapon.SlotType},
		map[string]any{"stat_name": "Class", "stat_value": weapon.Class},
	}
}
