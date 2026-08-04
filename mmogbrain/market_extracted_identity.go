package main

import (
	"strconv"
	"strings"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

type extractedMarketItemMetadata struct {
	displayName       string
	itemType          string
	itemTableCategory string
	catalogBucket     string
	externalID        string
}

type extractedStarterLoadoutIdentity struct {
	loadoutID int32
	weapons   [2]int32
	abilities [4]int32
	perks     [4]int32
}

var (
	extractedShipIDAthos     = dreadconfig.MustItemByTypeAndDisplayName(dreadconfig.ItemTypeShip, "Athos").ItemID
	extractedShipIDZmey      = dreadconfig.MustItemByTypeAndDisplayName(dreadconfig.ItemTypeShip, "Zmey").ItemID
	extractedShipIDAion      = dreadconfig.MustItemByTypeAndDisplayName(dreadconfig.ItemTypeShip, "Aion").ItemID
	extractedShipIDValcour   = dreadconfig.MustItemByTypeAndDisplayName(dreadconfig.ItemTypeShip, "Valcour").ItemID
	extractedShipIDSvarog    = dreadconfig.MustItemByTypeAndDisplayName(dreadconfig.ItemTypeShip, "Svarog").ItemID
	extractedShipIDTrafalgar = dreadconfig.MustItemByTypeAndDisplayName(dreadconfig.ItemTypeShip, "Trafalgar").ItemID
	extractedShipIDNav       = dreadconfig.MustItemByTypeAndDisplayName(dreadconfig.ItemTypeShip, "Nav").ItemID
	extractedShipIDCeres     = dreadconfig.MustItemByTypeAndDisplayName(dreadconfig.ItemTypeShip, "Ceres").ItemID
)

func extractedMarketItemMetadataForID(itemID int32) (extractedMarketItemMetadata, bool) {
	item, ok := dreadconfig.ItemByID(itemID)
	if !ok {
		return extractedMarketItemMetadata{}, false
	}
	return extractedMarketItemMetadata{
		displayName:       item.DisplayName,
		itemType:          item.ItemType,
		itemTableCategory: item.TableCategory,
		catalogBucket:     item.CatalogBucket,
		externalID:        item.AssetPath,
	}, true
}

func extractedMarketItemDisplayName(itemID int32, fallback string) string {
	meta, ok := extractedMarketItemMetadataForID(itemID)
	if !ok || meta.displayName == "" {
		return fallback
	}
	return meta.displayName
}

func extractedMarketItemExternalID(itemID int32, fallback string) string {
	meta, ok := extractedMarketItemMetadataForID(itemID)
	if ok {
		return clientSafeMarketExternalID(meta.itemType, meta.displayName, itemID)
	}
	if fallback != "" {
		return sanitizeMarketExternalID(fallback)
	}
	return "item_" + strconv.FormatInt(int64(itemID), 10)
}

func clientSafeMarketExternalID(itemType string, displayName string, itemID int32) string {
	prefix := sanitizeMarketExternalID(itemType)
	name := sanitizeMarketExternalID(displayName)
	id := strconv.FormatInt(int64(itemID), 10)
	switch {
	case prefix != "" && name != "":
		return prefix + "_" + name + "_" + id
	case prefix != "":
		return prefix + "_" + id
	case name != "":
		return name + "_" + id
	default:
		return "item_" + id
	}
}

func sanitizeMarketExternalID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func extractedStarterLoadoutIdentityForShip(shipName string) (extractedStarterLoadoutIdentity, bool) {
	loadout, ok := dreadconfig.StarterInventoryLoadoutByShipName(shipName)
	if !ok {
		return extractedStarterLoadoutIdentity{}, false
	}

	identity := extractedStarterLoadoutIdentity{loadoutID: loadout.LoadoutID}
	for _, slot := range loadout.Slots {
		switch slot.SlotName {
		case dreadconfig.SlotWeaponPrimary:
			identity.weapons[0] = slot.ItemID
		case dreadconfig.SlotWeaponSecondary:
			identity.weapons[1] = slot.ItemID
		case dreadconfig.SlotAbilityPrimary:
			identity.abilities[0] = slot.ItemID
		case dreadconfig.SlotAbilitySecondary:
			identity.abilities[1] = slot.ItemID
		case dreadconfig.SlotAbilityPerimeter:
			identity.abilities[2] = slot.ItemID
		case dreadconfig.SlotAbilityInternal:
			identity.abilities[3] = slot.ItemID
		case dreadconfig.SlotPerkCom:
			identity.perks[0] = slot.ItemID
		case dreadconfig.SlotPerkWeapon:
			identity.perks[1] = slot.ItemID
		case dreadconfig.SlotPerkNavigation:
			identity.perks[2] = slot.ItemID
		case dreadconfig.SlotPerkEngineer:
			identity.perks[3] = slot.ItemID
		}
	}
	return identity, true
}
