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
			for _, aliasID := range legacyStarterShipItemAliases(seed.Name) {
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

func legacyStarterShipItemAliases(shipName string) []string {
	switch strings.ToLower(strings.TrimSpace(shipName)) {
	case "assault medium t1":
		return []string{"Athos_T1", "16777223"}
	case "dreadnought medium t1":
		return []string{"Akula_T1", "16777225"}
	case "support medium t1":
		return []string{"Lorica_T1", "16777231"}
	default:
		return nil
	}
}
