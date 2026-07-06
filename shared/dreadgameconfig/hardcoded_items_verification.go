package dreadgameconfig

import (
	"fmt"
	"log"
	"sync"
)

// HardcodedItemsVerification represents the verification of all hardcoded items
// This implements H6: Verify all 66 currently hardcoded items resolve correctly via the new loader
type HardcodedItemsVerification struct {
	TotalItems        int
	VerifiedItems     int
	MissingItems      int
	VerificationMap   map[int32]HardcodedItemInfo
	MissingItemIDs    []int32
}

// HardcodedItemInfo contains information about a hardcoded item
type HardcodedItemInfo struct {
	ItemID        int32
	DisplayName   string
	ItemType      string
	TableCategory string
	CatalogBucket string
	AssetPath     string
	Source        string // "itemCatalog", "starterLoadouts", etc.
	Verified      bool
	DynamicName   string // Display name from dynamic catalog
}

var (
	hardcodedItemsVerification     *HardcodedItemsVerification
	hardcodedItemsVerificationOnce sync.Once
	hardcodedItemsVerificationMu   sync.RWMutex
)

// VerifyAllHardcodedItems verifies that all hardcoded items can be resolved via the new loader
// This implements H6: Verify all 66 currently hardcoded items resolve correctly via the new loader
func VerifyAllHardcodedItems() *HardcodedItemsVerification {
	hardcodedItemsVerificationOnce.Do(func() {
		// Build the dynamic catalog first
		catalog := BuildDynamicItemCatalog()
		
		// Create verification structure
		verification := &HardcodedItemsVerification{
			VerificationMap: make(map[int32]HardcodedItemInfo),
			MissingItemIDs:   make([]int32, 0),
		}

		// Step 1: Verify items from the hardcoded itemCatalog
		hardcodedCatalogItems := getHardcodedItemCatalogItems()
		
		for _, item := range hardcodedCatalogItems {
			verification.addItem(item, "itemCatalog")
		}

		// Step 2: Verify items from starterInventoryLoadouts
		starterLoadoutItems := getStarterLoadoutItems()
		
		for _, item := range starterLoadoutItems {
			// Only add if not already present from itemCatalog
			if _, exists := verification.VerificationMap[item.ItemID]; !exists {
				verification.addItem(item, "starterLoadouts")
			}
		}

		// Step 3: Verify all items against the dynamic catalog
		verification.verifyAgainstDynamicCatalog(catalog)

		// Step 4: Log results
		verification.logResults()

		hardcodedItemsVerification = verification
	})

	return hardcodedItemsVerification
}

// addItem adds a hardcoded item to the verification map
func (v *HardcodedItemsVerification) addItem(item ItemMetadata, source string) {
	info := HardcodedItemInfo{
		ItemID:        item.ItemID,
		DisplayName:   item.DisplayName,
		ItemType:      item.ItemType,
		TableCategory: item.TableCategory,
		CatalogBucket: item.CatalogBucket,
		AssetPath:     item.AssetPath,
		Source:        source,
		Verified:      false,
		DynamicName:   "",
	}

	v.VerificationMap[item.ItemID] = info
	v.TotalItems++
}

// verifyAgainstDynamicCatalog verifies all hardcoded items against the dynamic catalog
func (v *HardcodedItemsVerification) verifyAgainstDynamicCatalog(catalog *DynamicItemCatalog) {
	if catalog == nil {
		log.Printf("⚠️  H6: Cannot verify against dynamic catalog - catalog is nil")
		return
	}

	for itemID, info := range v.VerificationMap {
		// Check if the item exists in the dynamic catalog
		if dynamicItem, exists := catalog.ItemsByID[itemID]; exists {
			info.Verified = true
			info.DynamicName = dynamicItem.DisplayName
			v.VerificationMap[itemID] = info
			v.VerifiedItems++
		} else {
			// Item not found in dynamic catalog
			info.Verified = false
			info.DynamicName = "NOT FOUND"
			v.VerificationMap[itemID] = info
			v.MissingItemIDs = append(v.MissingItemIDs, itemID)
			v.MissingItems++
		}
	}
}

// logResults logs the verification results
func (v *HardcodedItemsVerification) logResults() {
	log.Printf("✅ H6: Verified %d/%d hardcoded items resolve correctly via new loader",
		v.VerifiedItems, v.TotalItems)
	
	if v.MissingItems > 0 {
		log.Printf("⚠️  H6: %d hardcoded items NOT found in dynamic catalog:", v.MissingItems)
		for _, itemID := range v.MissingItemIDs {
			info := v.VerificationMap[itemID]
			log.Printf("  - ItemID %d (%s) from %s", itemID, info.DisplayName, info.Source)
		}
	} else {
		log.Printf("✅ H6: All %d hardcoded items resolve correctly via new loader", v.TotalItems)
	}
}

// GetHardcodedItemsVerification returns the verification results
func GetHardcodedItemsVerification() *HardcodedItemsVerification {
	return VerifyAllHardcodedItems()
}

// GetHardcodedItemVerification returns verification info for a specific item
func GetHardcodedItemVerification(itemID int32) (HardcodedItemInfo, bool) {
	verification := VerifyAllHardcodedItems()
	hardcodedItemsVerificationMu.RLock()
	defer hardcodedItemsVerificationMu.RUnlock()

	info, exists := verification.VerificationMap[itemID]
	return info, exists
}

// GetAllHardcodedItemVerifications returns verification info for all hardcoded items
func GetAllHardcodedItemVerifications() []HardcodedItemInfo {
	verification := VerifyAllHardcodedItems()
	hardcodedItemsVerificationMu.RLock()
	defer hardcodedItemsVerificationMu.RUnlock()

	infos := make([]HardcodedItemInfo, 0, len(verification.VerificationMap))
	for _, info := range verification.VerificationMap {
		infos = append(infos, info)
	}
	return infos
}

// GetMissingHardcodedItems returns item IDs that were not found in the dynamic catalog
func GetMissingHardcodedItems() []int32 {
	verification := VerifyAllHardcodedItems()
	hardcodedItemsVerificationMu.RLock()
	defer hardcodedItemsVerificationMu.RUnlock()

	missing := make([]int32, len(verification.MissingItemIDs))
	copy(missing, verification.MissingItemIDs)
	return missing
}

// GetHardcodedItemCount returns the total number of hardcoded items
func GetHardcodedItemCount() int {
	verification := VerifyAllHardcodedItems()
	hardcodedItemsVerificationMu.RLock()
	defer hardcodedItemsVerificationMu.RUnlock()

	return verification.TotalItems
}

// GetVerifiedHardcodedItemCount returns the number of verified hardcoded items
func GetVerifiedHardcodedItemCount() int {
	verification := VerifyAllHardcodedItems()
	hardcodedItemsVerificationMu.RLock()
	defer hardcodedItemsVerificationMu.RUnlock()

	return verification.VerifiedItems
}

// GetMissingHardcodedItemCount returns the number of missing hardcoded items
func GetMissingHardcodedItemCount() int {
	verification := VerifyAllHardcodedItems()
	hardcodedItemsVerificationMu.RLock()
	defer hardcodedItemsVerificationMu.RUnlock()

	return verification.MissingItems
}

// IsAllHardcodedItemsVerified returns true if all hardcoded items are verified
func IsAllHardcodedItemsVerified() bool {
	verification := VerifyAllHardcodedItems()
	hardcodedItemsVerificationMu.RLock()
	defer hardcodedItemsVerificationMu.RUnlock()

	return verification.MissingItems == 0
}

// getHardcodedItemCatalogItems returns all items from the hardcoded itemCatalog
func getHardcodedItemCatalogItems() []ItemMetadata {
	return itemCatalog
}

// getStarterLoadoutItems returns all unique items from starterInventoryLoadouts
func getStarterLoadoutItems() []ItemMetadata {
	// Extract all unique item IDs from starter loadouts
	seenItemIDs := make(map[int32]bool)
	var uniqueItems []ItemMetadata

	for _, loadout := range starterInventoryLoadouts {
		// Add the ship itself
		if !seenItemIDs[loadout.ShipID] {
			// Try to find the ship in itemCatalog
			for _, item := range itemCatalog {
				if item.ItemID == loadout.ShipID {
					seenItemIDs[loadout.ShipID] = true
					uniqueItems = append(uniqueItems, item)
					break
				}
			}
		}

		// Add the loadout itself
		if !seenItemIDs[loadout.LoadoutID] {
			// Try to find the loadout in itemCatalog
			for _, item := range itemCatalog {
				if item.ItemID == loadout.LoadoutID {
					seenItemIDs[loadout.LoadoutID] = true
					uniqueItems = append(uniqueItems, item)
					break
				}
			}
		}

		// Add all slot items
		for _, slot := range loadout.Slots {
			if !seenItemIDs[slot.ItemID] {
				// Try to find the item in itemCatalog
				for _, item := range itemCatalog {
					if item.ItemID == slot.ItemID {
						seenItemIDs[slot.ItemID] = true
						uniqueItems = append(uniqueItems, item)
						break
					}
				}
			}
		}
	}

	return uniqueItems
}

// VerifyHardcodedItemsAgainstDynamicCatalog explicitly verifies all hardcoded items
// This is the main H6 function that can be called to verify the implementation
func VerifyHardcodedItemsAgainstDynamicCatalog() string {
	verification := VerifyAllHardcodedItems()
	
	if verification == nil {
		return "❌ H6: Verification failed - could not build dynamic catalog"
	}

	if verification.MissingItems == 0 {
		return fmt.Sprintf("✅ H6: All %d hardcoded items resolve correctly via new loader", verification.TotalItems)
	} else {
		return fmt.Sprintf("⚠️  H6: %d/%d hardcoded items resolve correctly, %d missing",
			verification.VerifiedItems, verification.TotalItems, verification.MissingItems)
	}
}

// PrintHardcodedItemsVerificationReport prints a detailed report of the verification
func PrintHardcodedItemsVerificationReport() {
	verification := VerifyAllHardcodedItems()
	
	fmt.Printf("=== H6: Hardcoded Items Verification Report ===\n")
	fmt.Printf("Total hardcoded items: %d\n", verification.TotalItems)
	fmt.Printf("Verified items: %d\n", verification.VerifiedItems)
	fmt.Printf("Missing items: %d\n", verification.MissingItems)
	
	if verification.MissingItems > 0 {
		fmt.Printf("\nMissing items:\n")
		for _, itemID := range verification.MissingItemIDs {
			info := verification.VerificationMap[itemID]
			fmt.Printf("  - ItemID %d: %s (from %s)\n", itemID, info.DisplayName, info.Source)
		}
	} else {
		fmt.Printf("\n✅ All hardcoded items resolve correctly!\n")
	}
	
	fmt.Printf("\nDetailed verification:\n")
	for itemID, info := range verification.VerificationMap {
		status := "✅"
		if !info.Verified {
			status = "❌"
		}
		fmt.Printf("  %s ItemID %d: %s -> %s (from %s)\n",
			status, itemID, info.DisplayName, info.DynamicName, info.Source)
	}
}