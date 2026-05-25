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
	switch shipClass {
	case 0:
		return "Destroyer"
	case 1:
		return "Corvette"
	case 2:
		return "Artillery"
	case 3:
		return "Tactical"
	case 4:
		return "Dreadnought"
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

func gatewayItemCatalogSeeds(priceCurrencyID string) []gatewayCatalogEntitySeed {
	seeds := make([]gatewayCatalogEntitySeed, 0, len(starterOwnedInventorySeeds()))
	for _, item := range starterOwnedInventorySeeds() {
		if item.itemType == "ship" {
			continue
		}
		seeds = append(seeds, gatewayCatalogEntitySeed{
			itemID:          item.itemID,
			externalID:      item.externalID,
			displayName:     item.name,
			description:     item.description,
			entityType:      "item",
			itemType:        item.itemType,
			manufacturer:    item.manufacturer,
			shipID:          item.shipID,
			loadoutID:       item.loadoutID,
			priceCurrencyID: priceCurrencyID,
			priceAmount:     0,
			owned:           true,
			hidden:          item.itemType != "ship" && item.itemType != "loadout",
			quantity:        item.quantity,
			gateIdentity:    true,
		})
	}
	return seeds
}

func gatewayCurrencyCatalogSeeds(priceCurrencyID string, grantedCurrency string) []gatewayCatalogEntitySeed {
	grantedAmount := int32(1000)
	if grantedCurrency == "CR" {
		grantedAmount = 10000
	}
	return []gatewayCatalogEntitySeed{{
		itemID:          9000001,
		externalID:      "currency_pack_" + strings.ToLower(grantedCurrency),
		displayName:     strings.ToUpper(grantedCurrency) + " Starter Pack",
		description:     "Bootstrap currency pack for hangar readiness",
		entityType:      "forex_offer",
		itemType:        "currency_pack",
		priceCurrencyID: priceCurrencyID,
		priceAmount:     0,
		grantedCurrency: grantedCurrency,
		grantedAmount:   grantedAmount,
		quantity:        1,
	}}
}

func gatewayBundleCatalogSeeds() []gatewayCatalogEntitySeed {
	return []gatewayCatalogEntitySeed{{
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
	}}
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
	priceValue := strconv.Itoa(int(seed.priceAmount))
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
		"ItemStatsArray":          []any{},
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


