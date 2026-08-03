package main

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

type gatewayCatalogEntitySeed struct {
	itemID      int32
	externalID  string
	displayName string
	// localizationKey is what goes in the entity's lowercase "name" field. The
	// client resolves that against its own string tables and renders
	// "<DNT>[[NotFound]]" for anything that is not a real key, so a human
	// display name must never be put there. See marketItemLocalizationKeys.
	localizationKey string
	description     string
	entityType      string
	itemType        string
	manufacturer    string
	// shipClass is the BASE ship class (0=Dreadnought, 1=Corvette,
	// 2=ArtilleryCruiser, 3=TacticalCruiser, 4=Destroyer), not EYShipClass.
	// See gatewayShipClassDisplayName.
	shipClass       int32
	shipID          int32
	loadoutID       int32
	priceCurrencyID string
	priceAmount     int32
	grantedCurrency string
	grantedAmount   int32
	// providedCredits/providedPoints are what a currency pack GRANTS on
	// purchase, as opposed to grantedCurrency/grantedAmount which is the
	// display-side pairing. Both are 0 for ordinary items.
	providedCredits int32
	providedPoints  int32
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
		"wallet":              gatewayWalletSnapshot(playerID),
		"owned_items":         ownedItems,
		"starter_ship_ids":    starterShipIDsForBootstrap(),
		"starter_loadout_ids": starterLoadoutIDsForBootstrap(),
	}
	// Gp2CreditsConversion, Campaigns, CustomSettings and FoundersPackUrl are
	// read by the catalog parser (0x142a34700) and were never sent. The
	// conversion rate is the one with a visible effect: it is what the market's
	// GP-to-credits exchange divides by, and it read 0 without this.
	payload["Gp2CreditsConversion"] = gatewayGpToCreditsRate
	payload["Campaigns"] = []any{}
	payload["CustomSettings"] = map[string]any{}
	payload["FoundersPackUrl"] = ""
	if catalog := gatewayRequestedCatalogCollection(playerID, requestedCatalog, playerDataReady); catalog != nil {
		// Top-level "entities" is REQUIRED, on every catalog, even when empty.
		//
		// It is not read by the catalog parser (0x142a34700), which is what an
		// earlier version of this checked before dropping the field as dead
		// weight. But MarketManager's completion check (FUN_1403dac50) looks
		// "entities" up by name on each of the four catalog responses and fails
		// the whole fetch if the key is missing, with "Market data retrieval was
		// not successful! One or more returned catalogs were missing data." --
		// which is exactly what dropping it produced.
		//
		// Only presence is tested, not contents: the two currency catalogs have
		// always sent an empty entities array and the fetch succeeded, so the
		// empty real-money catalog is fine. Bundles is the one that is
		// length-checked (0 < count) and it is checked on "bundles", not here.
		payload["entities"] = catalog["entities"]
		payload["Items"] = catalog["Items"]
		payload["ItemOffers"] = catalog["ItemOffers"]
		payload["ForexOffers"] = catalog["ForexOffers"]
	}
	if requestedCatalog == "bundles" {
		// BOTH spellings, deliberately. The catalog parser (0x142a34700) reads
		// L"Bundles" while MarketManager's completion check (FUN_1403dac50)
		// reads L"bundles" and requires a non-empty array, so the two halves of
		// the client disagree about the capitalisation.
		//
		// This is the one case-differing pair that is safe to send. A collision
		// only does damage when the two spellings carry DIFFERENT values, as
		// Name/name did -- one silently wins and which one is unpredictable.
		// Here both names are bound to the same slice, so either winner is the
		// right answer. Dropping the lowercase alias as "just a collision" is
		// what failed the market fetch.
		bundles := gatewayMarketEntities(gatewayBundleCatalogSeeds(), playerDataReady)
		payload["Bundles"] = bundles
		payload["bundles"] = bundles
	}
	return payload
}

// gatewayRequestedCatalogCollection answers one of the five catalog endpoints.
//
// The two item catalogs are split by the currency an item is priced in, and are
// NOT interchangeable. MarketManager waits for all five responses and then
// concatenates them into a single store list (FUN_1403dac50, "Received all
// market data. Concatenating item catalog, currency catalog and bundles"), so
// returning the same items for both digital_items_rmt and digital_items_vc
// listed every item in the store twice. Everything this server sells is priced
// in credits, so the real-money catalog is legitimately empty -- which the gate
// tolerates, since it only requires each of the five responses to arrive, and
// the currency catalogs have always been empty without stalling it.
func gatewayRequestedCatalogCollection(playerID string, requestedCatalog string, playerDataReady bool) map[string]any {
	switch requestedCatalog {
	case "item_catalog_real":
		return gatewayItemCatalogCollection(gatewayMarketEntities(gatewayItemCatalogSeedsForCurrency(playerID, gatewayRealMoneyCurrencyIDs), playerDataReady))
	case "item_catalog_virtual":
		return gatewayItemCatalogCollection(gatewayMarketEntities(gatewayItemCatalogSeedsForCurrency(playerID, gatewayVirtualCurrencyIDs), playerDataReady))
	case "currency_catalog_real":
		return gatewayCurrencyCatalogCollection(gatewayMarketEntities(gatewayCurrencyCatalogSeeds("RMT", "RMT"), playerDataReady))
	case "currency_catalog_virtual":
		return gatewayCurrencyCatalogCollection(gatewayMarketEntities(gatewayCurrencyCatalogSeeds("CR", "CR"), playerDataReady))
	default:
		return nil
	}
}

// gatewayVirtualCurrencyIDs / gatewayRealMoneyCurrencyIDs partition the catalog.
// CR is credits (soft), GP is the hard currency, RMT is real money.
var (
	gatewayVirtualCurrencyIDs   = map[string]bool{"CR": true, "GP": true}
	gatewayRealMoneyCurrencyIDs = map[string]bool{"RMT": true}
)

func gatewayItemCatalogSeedsForCurrency(playerID string, currencies map[string]bool) []gatewayCatalogEntitySeed {
	// gatewayItemCatalogSeeds takes a playerID -- the two call sites used to
	// pass the strings "RMT" and "CR" here, so every catalog was built for a
	// player named after a currency and no purchase the player had actually
	// made was ever reflected in it.
	all := gatewayItemCatalogSeeds(playerID)
	seeds := make([]gatewayCatalogEntitySeed, 0, len(all))
	for _, seed := range all {
		if currencies[seed.priceCurrencyID] {
			seeds = append(seeds, seed)
		}
	}
	return seeds
}

func starterShipIDsForBootstrap() []int32 {
	return dreadconfig.StarterInventoryShipIDs()
}

func starterLoadoutIDsForBootstrap() []int32 {
	return starterLoadoutIDs()
}

func gatewayItemCatalogCollection(entities []any) map[string]any {
	// Items is the definition list; ItemOffers is what the store actually
	// presents. The client builds its market grid and per-ship purchase data
	// from the offers, so leaving them empty produced "MarketGridItems of
	// length 0" and "GetShipPurchaseData Offer not found for ship ..." even
	// with Items fully populated. Every item is offered, at the price 0 that
	// gatewayMarketEntity already reports.
	return map[string]any{
		"entities":    entities,
		"Items":       entities,
		"ItemOffers":  entities,
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

// shipManufacturerID maps a manufacturer to the numeric id the client asks for.
//
// UYTechTreeManager stores the tech tree as an array of manufacturer entries
// (id at offset 0, stride 0x28), each holding the ship array that
// ComposeShipManufacturerDataForLoadout searches, and YUIExternalFunctions::
// GetManufacturerData looks them up by that id -- it logged "Could not find a
// manufacturer with id 0/1/2" while our tech tree carried no manufacturer at
// all.
//
// The client requests exactly 0, 1 and 2 and we have exactly three makers. The
// order below is the assumed one; if ships appear under the wrong maker's page,
// only these three numbers need reordering.
func shipManufacturerID(manufacturer string) int32 {
	switch manufacturer {
	case "JupiterArms":
		return 0
	case "AkulaVektor":
		return 1
	case "Oberon":
		return 2
	}
	return -1
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
			parentCategoryName = gatewayManufacturerDisplayName(shipManufacturer(ship))
		} else {
			if hasExtractedMeta && extractedMeta.catalogBucket != "" {
				categoryName = extractedMeta.catalogBucket
			}
			parentCategoryName = shipDisplayName(ship)
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

// gatewayWalletSnapshot reports the player's balances.
//
// CAUTION: the client does not read this field. The string "wallet" does not
// occur anywhere in the shipping binary, so whatever is sent here is ignored.
// It previously returned a hardcoded 10000/0/0; reporting the player's real
// balance is at least honest, but it does not drive anything on screen.
//
// The HUD's three numbers are FPlayerCurrencyAmountsData{m_freeXP,
// m_softCurrency, m_hardCurrency}. Only m_freeXP is currently fed, by the
// "FreeXp" field of YA_PlayerGet -- and it is correct, matching the persisted
// value. The other two read zero because nothing this server sends supplies
// them: a complete enumeration of the YA_PlayerGet parser's 47 FName lookups
// contains no currency field, and the gateway fields that might have carried
// one ("wallet") are unknown to the client.
//
// Other gateway fields in this payload are likewise absent from the binary and
// therefore dead: owned_items, player_id, catalog_version, starter_ship_ids and
// requested_catalog. Only entities/Items/ItemOffers/ForexOffers are read.
// Finding the real source of m_softCurrency/m_hardCurrency is still open.
func gatewayWalletSnapshot(playerID string) map[string]any {
	state := mmogPlayerStateForPID(playerID)
	return map[string]any{
		"CR":     state.softCurrency,
		"RMT":    state.premiumCurrency,
		"FreeXp": state.freeXP,
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
// catalogSKUNumber matches the store's own SKU shape: "999" followed by an item
// id. Confirmed from the shipped CatalogIDTable itself, where e.g. 99933489268
// is the Furia's precast loadout id 33489268 behind that prefix.
var catalogSKUNumber = regexp.MustCompile(`^999(\d{6,10})$`)

// catalogSKUDisplay gives a SKU the item's real name and description where the
// SKU encodes an item id we can resolve.
//
// Every one of the 6630 catalog entries used to be labelled "<bucket> <sku>" and
// described as "<bucket> catalog item" -- invented placeholders, and the client
// shows them. It also renders a missing entry as
// "<sku><DNT> Invalid Description Field in Json", which is what the Rurik's
// description was: the client asks this catalog for a ship's description by SKU,
// and a hull with no store entry has none to give.
//
// Names come from the same authority as everywhere else, and descriptions from
// the hull's own precast blueprint. Anything unresolvable keeps the old
// placeholder rather than getting a made-up one.
func catalogSKUDisplay(bucketName, sku string) (string, string) {
	displayName := bucketName + " " + sku
	description := bucketName + " catalog item"

	match := catalogSKUNumber.FindStringSubmatch(sku)
	if match == nil {
		return displayName, description
	}
	id, err := strconv.ParseInt(match[1], 10, 32)
	if err != nil {
		return displayName, description
	}
	itemID := int32(id)
	if name, ok := dreadconfig.AuthoritativeItemName(itemID); ok && name != "" {
		displayName = name
	}
	if text, ok := dreadconfig.HullDescriptionForItemID(itemID); ok {
		description = text
	}
	return displayName, description
}

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
		displayName, description := catalogSKUDisplay(bucketName, sku)
		seeds = append(seeds, gatewayCatalogEntitySeed{
			itemID:          idBase + int32(i),
			externalID:      sku,
			displayName:     displayName,
			description:     description,
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
//
// Every base is deliberately below 16777216, i.e. TOP BYTE 0.
//
// The top byte of an item id is its ItemIDTable CategoryID -- verified across
// every id in the extracted tables, 3437 agree and 0 disagree -- so a synthetic
// id does not merely identify an entry, it CLAIMS a category. The previous bases
// (19000000 through 31000000) all had top byte 1, which is YShipLoadoutPrecast,
// so every synthetic bundle and every synthetic Heroships entry announced itself
// to the client as a precast ship loadout.
//
// No category has ID 0, so top byte 0 claims nothing, which is the honest answer
// for these entries: their real identity travels in Sku/external_id as the
// original SKU string, and itemID is only an internal handle. Bases are spaced a
// million apart and the largest bucket holds ~3100 entries, so they cannot
// collide with each other, and starting at 5000000 keeps them clear of the
// retired OldItemIDs in ItemIDConversionTable (which begin at 1000001).
var realCatalogBucketIDBase = map[string]int32{
	"Bundles":             5000000,
	"Weapons":             6000000,
	"Modules":             7000000,
	"Captain Vanity":      8000000,
	"Coatings Collection": 9000000,
	"Decals Collection":   10000000,
	"Emblems Collection":  11000000,
	"Patterns Collection": 12000000,
	"Code Redemptions":    13000000,
	"Heroships":           14000000,
	"GP to CR":            15000000,
	"un_typed":            16000000,
}

// gatewayItemCatalogSeeds returns the market (store) catalog contents.
//
// This was served EMPTY for a long time, on the grounds that each entry's
// lowercase "name" is a localization key we did not have, so every item
// rendered as "<DNT>[[NotFound]]". That is no longer true: the keys were
// recovered from the game's own shipped .locres data -- see
// marketItemLocalizationKeys.
//
// Serving nothing was not cosmetic. The client builds the tech tree, the market
// grid and the loadout/vanity pickers from catalog items, and with none it logs
//
//	UTechTreeInterpreter::ComposeShipManufacturerDataForLoadout Could not find item for ship id 33489198
//	UTechTreeInterpreter::GetHeroShipsFromManufacturerData Could not find a manufacturer with id 0
//	Script Msg: Attempted to access index 0 from array MarketGridItems of length 0
//
// leaving an empty tech tree, an empty market, and no items to choose from when
// editing a loadout or a ship's vanity.
//
// Everything is listed as owned and free. This server has no real store, and
// gatewayMarketEntity deliberately reports price 0 for every entry so the client
// never computes a campaign discount against its own local prices.
// hullCatalogDescription returns a ship's own description text, or "" for items
// the game has no description for.
func hullCatalogDescription(itemID int32) string {
	description, _ := dreadconfig.HullDescriptionForItemID(itemID)
	return description
}

func gatewayItemCatalogSeeds(playerID string) []gatewayCatalogEntitySeed {
	purchased := persistedMmogPlayerPurchasedItemIDSet(playerID)

	// Starter gear already carries the ship/loadout it belongs to. Catalog
	// entries must report the same association, otherwise an entry and the
	// owned_items record for the same item disagree about which ship it is on.
	starter := map[int32]mmogInventoryItemSeed{}
	for _, item := range starterOwnedInventorySeeds() {
		starter[item.itemID] = item
	}

	seeds := make([]gatewayCatalogEntitySeed, 0, len(marketItemLocalizationKeys))
	for _, itemID := range sortedMarketCatalogItemIDs() {
		meta, ok := extractedMarketItemMetadataForID(itemID)
		if !ok {
			continue
		}
		seed := gatewayCatalogEntitySeed{
			itemID:      itemID,
			externalID:  extractedMarketItemExternalID(itemID, meta.displayName),
			displayName: meta.displayName,
			// Every entry here went out with an EMPTY description, and the
			// client renders a ship whose description it cannot read as
			// "99933489263<DNT> Invalid Description Field in Json" -- its own
			// SKU form of the id, then the error. Reported live for the Rurik
			// (AGENT-CHAT C12) while the Furia, which reaches the client through
			// the store-bucket path instead, rendered fine.
			//
			// The hulls' real prose is in their precast loadout blueprints, the
			// same assets the names come from. Eight hulls have none there and
			// stay empty, and non-ship items stay empty too: there is no
			// description for them anywhere in the extracted data, and inventing
			// one is worse than the gap.
			description:     hullCatalogDescription(itemID),
			localizationKey: marketItemLocalizationKeys[itemID],
			entityType:      "item",
			itemType:        meta.itemType,
			priceCurrencyID: "CR",
			priceAmount:     gatewayMarketCreditPrice(meta.itemType, gatewayMarketItemTier(itemID)),
			quantity:        1,
			hidden:          gatewayMarketItemIsDevelopmentAsset(meta.displayName),
		}
		if owned, isStarter := starter[itemID]; isStarter {
			seed.externalID = owned.externalID
			seed.shipID = owned.shipID
			seed.loadoutID = owned.loadoutID
			seed.manufacturer = owned.manufacturer
			seed.owned = true
			// Gear inherits the class of the ship it is fitted to, so its card
			// shows the same class icon as that ship.
			seed.shipClass = techTreeShipClass(owned.shipID)
		}
		if _, bought := purchased[itemID]; bought {
			seed.owned = true
		}
		if meta.itemType == "ship" || fleetShipCatalogIDs()[itemID] {
			// Fleet entries are precast-loadout ids that the client treats as
			// ship ids (ComposeShipManufacturerDataForLoadout looks them up by
			// that id and wants manufacturer data), so they need the same
			// treatment as a real ship even though their item type says
			// "loadout".
			//
			// Only when nothing better is known: for starter gear the block
			// above already set shipID to the PAWN id of the ship the item is
			// fitted to, which is what "ship_id" means and what the gateway's
			// owned_items reports. Overwriting that with the item's own id made
			// the catalog and owned_items disagree about the same item.
			if seed.shipID == 0 {
				seed.shipID = itemID
			}
			if ship, found := gatewayShipByID(itemID); found {
				seed.manufacturer = shipManufacturer(ship)
			}
			if seed.manufacturer == "" {
				// Fleet-alias ids live only in the tech tree, not in the ship
				// lists gatewayShipByID searches. Manufacturer is what the
				// client groups the tech tree by, so it cannot be left blank.
				seed.manufacturer = techTreeShipManufacturer(itemID)
			}
			seed.shipClass = techTreeShipClass(itemID)
		}
		if meta.itemType == "loadout" {
			seed.loadoutID = itemID
		}
		seeds = append(seeds, seed)
	}
	return seeds
}

// sortedMarketCatalogItemIDs returns the catalog's item ids in a stable order.
// Map iteration order is random in Go, and an unstable catalog would make the
// client's grid reshuffle between fetches.
func sortedMarketCatalogItemIDs() []int32 {
	ids := make([]int32, 0, len(marketItemLocalizationKeys))
	for itemID := range marketItemLocalizationKeys {
		ids = append(ids, itemID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
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
	// Prices go out in the CR/SP/RC fields below, NOT in "Price"/"CurrencyAmount".
	//
	// This used to send everything at 0 on the belief that the client holds its
	// own price data and that a server price would trigger a flood of
	// "UpdateOfferCampaignData | Original Price is lower than the offer price".
	// The first half is wrong: FYItemOfferData::Load (0x142a6d760) reads prices
	// only from CRPrice/SPPrice/RCPrice, which were never sent, so every offer
	// in the client logged "hard: 0 soft: 0 real: 0.00" and the store had no
	// prices at all. The second half is still respected -- OriginalPrice is
	// always emitted equal to the offer price, so no campaign discount is ever
	// computed and that comparison can never fire.
	creditsPrice, hardPrice := 0, 0
	switch seed.priceCurrencyID {
	case "GP":
		hardPrice = int(seed.priceAmount)
	default:
		creditsPrice = int(seed.priceAmount)
	}
	// OriginalPrice must equal the offer price. The client treats a higher
	// original as a discount and warns "Original Price is lower than the offer
	// price" when the two disagree; keeping them identical means no campaign
	// discount is ever derived.
	originalPrice := creditsPrice
	if hardPrice != 0 {
		originalPrice = hardPrice
	}
	priceID := gatewayMarketPriceID(seed)
	priceValue := strconv.Itoa(int(seed.priceAmount))
	owned := playerDataReady && seed.owned
	itemID, shipID, loadoutID, entityID := gatewayMarketIdentity(seed, playerDataReady)
	price := map[string]any{
		"id":            priceID,
		"PriceID":       priceID,
		"price_id":      priceID,
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
	itemTier := gatewayMarketItemTier(seed.itemID)
	bundleItemIDs := make([]any, 0, len(seed.bundleItems))
	for _, item := range seed.bundleItems {
		bundleItemIDs = append(bundleItemIDs, item.itemID)
	}
	entity := map[string]any{
		"ID":      itemID,
		"Sku":     seed.externalID,
		"ImgUrlS": "",
		"ImgUrlM": "",
		"ImgUrlL": "",
		"Flags":   0,
		// "Name" and "name" are BOTH sent, and are different values.
		//
		// An earlier pass here dropped "Name" on the grounds that UE resolves
		// fields through FNames, whose comparison is case-insensitive, so the
		// two spellings would collide. That is true of the BINARY mmog protocol
		// -- FUN_140320910 lowercases both sides -- but not of this JSON
		// catalog, where lookups are case-sensitive: the client's own item
		// loader reads "ImgUrlL" and "full_image_url" from the same object and
		// treats them as different fields.
		//
		// Dropping it was what emptied every name in the store. FYItemData::Load
		// (FUN_142a6d020) looks up "Name"; when the field is absent the reader
		// (FUN_142a60670) returns its fallback, and the client logged
		// "Item Id: <id> name: <DNT>[[NotFound]]" for all 62 items. That marker
		// means the FIELD was missing, not that a localization key failed to
		// resolve.
		//
		// So "Name" carries the display text and "name" the localization key the
		// client resolves separately.
		"Name":                seed.displayName,
		"name":                gatewayMarketLocalizationName(seed),
		"display_name":        seed.displayName,
		"entity_id":           entityID,
		"external_id":         seed.externalID,
		"item_id":             itemID,
		"entity_type":         seed.entityType,
		"item_type":           seed.itemType,
		"Description":         seed.description,
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
		// Tier drives the UI's tier badge, whose texture path is built as
		// /Game/Generic/UI/tiers/UI_tier_<n>. With no tier field at all the
		// client read the value uninitialised and asked for nonsense like
		// UI_tier_1107296256 (0x42000000, the float 32.0) and UI_tier_148,
		// against assets that only exist for 1..16 -- so every item icon
		// failed to load. Sent under each spelling the payload uses elsewhere.
		"Tier":                   itemTier,
		"ItemTier":               itemTier,
		"item_tier":              itemTier,
		"HasVeteranStatus":       false,
		"HeroShipStatsArray":     []any{},
		"PreviousItemStatsArray": []any{},
		"Manufacturer":           seed.manufacturer,
		"ship_id":                shipID,
		"ShipID":                 shipID,
		"loadout_id":             loadoutID,
		"LoadoutID":              loadoutID,
		"PriceID":                priceID,
		"campaign_id":            "",
		"PromotionFlagSet":       []any{},
		// The 12 fields below are the ones FYItemOfferData::Load (0x142a6d760)
		// actually reads. Everything above is either read by FYItemData::Load
		// (Name/Flags/GrantedCurrency/ImgUrl*) or inert display scaffolding.
		//
		// Currency mapping is confirmed from the loader's stores and the log
		// format "hard: %d soft: %d real: %0.2f": SPPrice lands at +0x14 and is
		// printed as hard, CRPrice at +0x18 as soft, RCPrice at +0x20 as real.
		// So CR = credits, SP = GP, RC = real money.
		"CRPrice":         creditsPrice,
		"CRCurrency":      "CR",
		"SPPrice":         hardPrice,
		"SPCurrency":      "GP",
		"RCPrice":         0,
		"RCCurrency":      "USD",
		"RCSymbol":        "$",
		"OriginalPrice":   originalPrice,
		"ExpirationTime":  0,
		"PromotionFlags":  0,
		"ProvidedCredits": seed.providedCredits,
		"ProvidedPoints":  seed.providedPoints,
		// ItemIDs is how the offer loader reads a bundle's contents. Unlike
		// items[], which carries whole entities and caused duplicate
		// FYItemData loads for ids the item catalog already sent (issue #58),
		// this is ids only, so it can be populated safely.
		"ItemIDs":      bundleItemIDs,
		"prices":       []any{price},
		"items":        bundleItems,
		"entities":     []any{},
		"entitlements": []any{},
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

// gatewayMarketLocalizationName returns the value for a catalog entity's
// lowercase "name" field.
//
// That field is a localization KEY, not a label: the client looks it up in its
// own string tables and substitutes the literal "<DNT>[[NotFound]]" when the
// lookup fails. Keys come from marketItemLocalizationKeys, recovered from the
// game's shipped .locres data.
//
// When an item has no known key we send an empty string rather than its display
// name. A wrong key renders as the "[[NotFound]]" placeholder either way, and an
// empty value at least does not look like a failed lookup of a real name.
func gatewayMarketLocalizationName(seed gatewayCatalogEntitySeed) string {
	if seed.localizationKey != "" {
		return seed.localizationKey
	}
	if key, ok := marketItemLocalizationKeys[seed.itemID]; ok {
		return key
	}
	return ""
}

// fleetShipCatalogIDs is the set of ids the fleet reports as its ships. They are
// precast-loadout ids rather than ship pawn ids, but the client resolves
// manufacturer and category data for them as if they were ships.
//
// These were the DEVELOPMENT loadout ids until the class names the client
// instantiates were corrected; see nativeStarterLoadoutClassName.
func fleetShipCatalogIDs() map[int32]bool {
	starters := dreadconfig.StarterInventoryLoadoutIDs()
	ids := make(map[int32]bool, len(starters))
	for _, loadoutID := range starters {
		ids[fleetStarterShipIDForPrecast(loadoutID)] = true
	}
	return ids
}

// techTreeShipManufacturer returns the maker recorded for a tech-tree node,
// including the synthetic fleet- and loadout-alias rows that do not appear in
// the ship lists gatewayShipByID searches.
func techTreeShipManufacturer(shipID int32) string {
	for _, ship := range techTreeShips() {
		if ship.id == shipID {
			return shipManufacturer(ship)
		}
	}
	return ""
}

// gatewayMarketItemTier returns the 1-based tier the UI shows on an item badge.
//
// Tier is encoded in the item's asset path as a /T<n>/ directory. The families
// are inconsistent about where they start (some at T0, some at T1), so a T0 and
// a T1 variant both mean "first tier" here; anything higher maps straight
// across. Items with no tier in their path are first tier.
//
// The result is clamped to 1..5. The UI only ships tier badges for a small
// range, and an out-of-range value produces a texture path that cannot resolve.
func gatewayMarketItemTier(itemID int32) int32 {
	item, ok := dreadconfig.ItemByID(itemID)
	if !ok {
		return 1
	}
	match := assetPathTierPattern.FindStringSubmatch(item.AssetPath)
	if match == nil {
		return 1
	}
	tier, err := strconv.Atoi(match[1])
	if err != nil || tier <= 1 {
		return 1
	}
	if tier > 5 {
		return 5
	}
	return int32(tier)
}

var assetPathTierPattern = regexp.MustCompile(`/T(\d+)/`)

// techTreeShipClass returns a ship's BASE class ordinal (0=Dreadnought,
// 1=Corvette, 2=ArtilleryCruiser, 3=TacticalCruiser, 4=Destroyer), covering the
// synthetic fleet- and loadout-alias rows as well as real ships.
func techTreeShipClass(shipID int32) int32 {
	if shipID == 0 {
		return 0
	}
	for _, ship := range techTreeShips() {
		if ship.id == shipID {
			return ship.shipClass
		}
	}
	if ship, ok := gatewayShipByID(shipID); ok {
		return ship.shipClass
	}
	return 0
}

// gatewayGpToCreditsRate is the divisor the market's GP-to-credits exchange
// uses. The client reads it as "Gp2CreditsConversion" and had nothing to read,
// leaving the exchange at 0. No authentic rate survives in the extracted game
// data, so this is a chosen value: 1 GP buys 250 credits.
const gatewayGpToCreditsRate = 250

// gatewayMarketPriceID names the price a purchase is made against. The client
// reads it as "PriceID" and echoes it back when buying, so it has to be stable
// for a given item and price, not the "price_free" constant every entry used to
// carry regardless of cost.
func gatewayMarketPriceID(seed gatewayCatalogEntitySeed) string {
	if seed.priceAmount <= 0 {
		return "price_free"
	}
	return "price_" + strings.ToLower(seed.priceCurrencyID) + "_" + strconv.Itoa(int(seed.priceAmount))
}

// gatewayMarketCreditPrice is the credit cost of a catalog item.
//
// ASSUMPTION, clearly flagged: no authentic price table survives. The extracted
// content has no price/cost datatable, the SDK has no offer struct, and the
// client holds no local prices -- it reads them from the CRPrice field only.
// So these are derived from the two signals the catalog does carry, item type
// and tier, on a doubling-per-tier curve. They are deliberately confined to
// this one function so a real table can replace it without touching anything
// else.
func gatewayMarketCreditPrice(itemType string, tier int32) int32 {
	var base int32
	switch itemType {
	case "ship", "loadout":
		base = 25000
	case "weapon", "ability", "module":
		base = 5000
	case "bundle", "currency":
		return 0
	default:
		base = 5000
	}
	if tier < 1 {
		tier = 1
	}
	if tier > 5 {
		tier = 5
	}
	return base << uint(tier-1)
}

// gatewayMarketItemIsDevelopmentAsset reports whether a catalog id is one of the
// engine-side "Precast Development ..." loadouts. They are debug assets that a
// live storefront would never list, so they are marked DoNotDisplayInStore
// rather than dropped -- the player's fleet still references these ids, and
// removing the entries entirely would leave those ships with no catalog entry
// at all.
func gatewayMarketItemIsDevelopmentAsset(displayName string) bool {
	return strings.Contains(displayName, "Precast Development") || strings.Contains(displayName, "DevLoadout")
}
