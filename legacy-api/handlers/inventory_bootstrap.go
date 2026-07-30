package handlers

import (
	"fmt"
	"strconv"
	"strings"

	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
)

type InventoryItem struct {
	ID         string `json:"id"`
	ItemType   string `json:"item_type"`
	ItemID     string `json:"item_id"`
	AcquiredAt string `json:"acquired_at"`
	Name       string `json:"name,omitempty"`
	ShipID     int32  `json:"ship_id,omitempty"`
	LoadoutID  int32  `json:"loadout_id,omitempty"`
	SlotName   string `json:"slot_name,omitempty"`
	Owned      bool   `json:"owned"`
}

type inventoryBootstrapSeed struct {
	ItemType  string
	ItemID    string
	AssetPath string
	Name      string
	ShipID    int32
	LoadoutID int32
	SlotName  string
}

const (
	itemTypeLegacyUI string = "loadout_item"
)

func inventorySeedKey(itemType string, itemID string) string {
	return strings.ToLower(strings.TrimSpace(itemType)) + ":" + strings.ToLower(strings.TrimSpace(itemID))
}

func starterInventoryBootstrapSeeds() []inventoryBootstrapSeed {
	sharedItems := dreadconfig.StarterInventoryItems()
	seeds := make([]inventoryBootstrapSeed, 0, len(sharedItems))
	for _, item := range sharedItems {
		seeds = append(seeds, inventoryBootstrapSeed{
			ItemType:  item.Item.ItemType,
			ItemID:    strconv.Itoa(int(item.Item.ItemID)),
			AssetPath: item.Item.AssetPath,
			Name:      item.Item.DisplayName,
			ShipID:    item.ShipID,
			LoadoutID: item.LoadoutID,
			SlotName:  item.SlotName,
		})
	}
	return seeds
}

func legacyStarterInventorySeedAliases() map[string]inventoryBootstrapSeed {
	aliases := make(map[string]inventoryBootstrapSeed)
	seedByKey := make(map[string]inventoryBootstrapSeed)
	for _, seed := range starterInventoryBootstrapSeeds() {
		seedByKey[inventorySeedKey(seed.ItemType, seed.ItemID)] = seed
		if seed.AssetPath != "" {
			aliases[inventorySeedKey(seed.ItemType, seed.AssetPath)] = seed
		}
		switch seed.ItemType {
		case dreadconfig.ItemTypeWeapon, dreadconfig.ItemTypeAbility, dreadconfig.ItemTypePerk:
			aliases[inventorySeedKey(itemTypeLegacyUI, seed.ItemID)] = seed
			if seed.AssetPath != "" {
				aliases[inventorySeedKey(itemTypeLegacyUI, seed.AssetPath)] = seed
			}
		case dreadconfig.ItemTypeShip:
			for _, aliasID := range legacyStarterShipItemAliases(seed.ItemID) {
				aliases[inventorySeedKey(seed.ItemType, aliasID)] = seed
			}
		}
	}
	for _, loadout := range dreadconfig.StarterInventoryLoadouts() {
		for idx, slot := range loadout.Slots {
			meta, ok := dreadconfig.ItemByID(slot.ItemID)
			if !ok {
				panic(fmt.Sprintf("missing shared starter item metadata for %d", slot.ItemID))
			}
			seed, ok := seedByKey[inventorySeedKey(meta.ItemType, strconv.Itoa(int(slot.ItemID)))]
			if !ok {
				panic(fmt.Sprintf("missing starter inventory seed for %s:%d", meta.ItemType, slot.ItemID))
			}
			legacyItemID := strconv.Itoa(int(loadout.LoadoutID*10 + int32(idx) + 1))
			aliases[inventorySeedKey(itemTypeLegacyUI, legacyItemID)] = seed
			aliases[inventorySeedKey(seed.ItemType, legacyItemID)] = seed
		}
	}
	return aliases
}

func starterInventoryShipIDs() []int32 {
	return dreadconfig.StarterInventoryShipIDs()
}

func starterInventoryLoadoutIDs() []int32 {
	return dreadconfig.StarterInventoryLoadoutIDs()
}

func inventoryItemFromSeed(seed inventoryBootstrapSeed, id string, acquiredAt string) InventoryItem {
	return InventoryItem{
		ID:         id,
		ItemType:   seed.ItemType,
		ItemID:     seed.ItemID,
		AcquiredAt: acquiredAt,
		Name:       seed.Name,
		ShipID:     seed.ShipID,
		LoadoutID:  seed.LoadoutID,
		SlotName:   seed.SlotName,
		Owned:      true,
	}
}

// legacyStarterShipItemAliases returns the alternative identifiers a stored
// inventory row may use for a starter ship, so rows written before ids were
// normalised still resolve to the seed.
//
// This used to be a hand-written switch on the display name returning
// {"Athos_T1", "16777223"} and friends. None of it was real: the numeric ids
// (16777216+n, i.e. an ordinal stuffed into the YShipLoadoutPrecast category)
// appear nowhere in the client's tables, "Akula" is a paint
// (VAN_PN_Akula_DA), not a ship, and "Lorica" is the tier-4 Dreadnought Light
// rather than anything a new player owns. The mapping was invented.
//
// The client already ships the authoritative answer: ItemIDConversionTable
// exists to translate a build's old item id to the current one, and carries
// the item's real name alongside. So both aliases are derived from it —
// OldItemID is a genuine legacy identifier for exactly this item, and Name is
// the name the client itself displays. Items absent from the conversion table
// (the Dreadnought Medium T1 loadout is only in ItemIDRegister) legitimately
// have no legacy alias, and get none rather than a fabricated one.
func legacyStarterShipItemAliases(itemID string) []string {
	parsed, err := strconv.ParseInt(strings.TrimSpace(itemID), 10, 64)
	if err != nil {
		return nil
	}
	entry, ok := dreadconfig.GetItemIDConversionEntryByNewID(parsed)
	if !ok {
		return nil
	}
	aliases := make([]string, 0, 2)
	if entry.OldItemID != 0 && entry.OldItemID != parsed {
		aliases = append(aliases, strconv.FormatInt(entry.OldItemID, 10))
	}
	// Names in the table carry stray padding, including non-breaking spaces
	// ("Lorica "), which would never match a stored row as-is.
	if name := strings.TrimSpace(strings.ReplaceAll(entry.Name, " ", " ")); name != "" {
		aliases = append(aliases, name)
	}
	return aliases
}
