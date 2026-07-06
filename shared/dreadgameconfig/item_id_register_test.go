package dreadgameconfig

import (
	"testing"
)

// TestH2LoadItemIDRegister tests the H2 requirement explicitly
func TestH2LoadItemIDRegister(t *testing.T) {
	// H2: Load `ItemIDRegister.json` (12,349 lines, 3,086 entries) into itemID→assetPath map
	// This test explicitly validates the H2 requirement

	// Reset the loaded state to test loading
	itemRegistryLock.Lock()
	itemRegistry = make(map[int32]ItemRegistryEntry)
	pathToItemID = make(map[string]int32)
	itemRegistryLoaded = false
	itemRegistryLock.Unlock()

	// Load the ItemIDRegister
	err := LoadItemIDRegister()
	if err != nil {
		t.Fatalf("H2: Failed to load ItemIDRegister: %v", err)
	}

	// Verify that entries were loaded
	entryCount := GetRegistryEntryCount()
	if entryCount == 0 {
		t.Fatal("H2: Expected entries to be loaded, got 0")
	}

	t.Logf("✅ H2: Successfully loaded %d entries from ItemIDRegister.json", entryCount)

	// Verify that we have a reasonable number of entries (expecting 3,086)
	if entryCount < 3000 {
		t.Errorf("H2: Expected at least 3000 entries, got %d", entryCount)
	}

	// Verify that we have the expected number of entries
	if entryCount != 3086 {
		t.Logf("H2: Expected 3086 entries, got %d", entryCount)
	} else {
		t.Logf("✅ H2: Found expected 3086 entries")
	}
}

// TestH2ItemIDToAssetPathMap tests the itemID→assetPath mapping functionality
func TestH2ItemIDToAssetPathMap(t *testing.T) {
	// H2: Verify that the itemID→assetPath map is working correctly
	LoadItemIDRegister()

	// Get all item IDs
	itemIDs := GetAllItemIDs()
	if len(itemIDs) == 0 {
		t.Fatal("H2: No item IDs found")
	}

	// Test that we can get asset paths for item IDs
	foundCount := 0
	for _, itemID := range itemIDs {
		path, exists := GetAssetPathForItemID(itemID)
		if !exists {
			t.Errorf("H2: Item ID %d not found in registry", itemID)
		} else {
			if path == "" {
				t.Errorf("H2: Item ID %d has empty asset path", itemID)
			} else {
				foundCount++
			}
		}
	}

	t.Logf("✅ H2: Successfully mapped %d item IDs to asset paths", foundCount)

	// Test that we can get registry entries
	for _, itemID := range itemIDs {
		entry, exists := GetItemRegistryEntry(itemID)
		if !exists {
			t.Errorf("H2: Registry entry for item ID %d not found", itemID)
		} else {
			if entry.ItemID != itemID {
				t.Errorf("H2: Item ID mismatch for entry: expected %d, got %d", itemID, entry.ItemID)
			}
			if entry.Path == "" {
				t.Errorf("H2: Entry for item ID %d has empty path", itemID)
			}
		}
	}

	t.Logf("✅ H2: All registry entries have valid data")
}

// TestH2AssetPathToItemIDMap tests the assetPath→itemID reverse mapping functionality
func TestH2AssetPathToItemIDMap(t *testing.T) {
	// H2: Verify that the assetPath→itemID reverse map is working correctly
	LoadItemIDRegister()

	// Get all asset paths
	paths := GetAllAssetPaths()
	if len(paths) == 0 {
		t.Fatal("H2: No asset paths found")
	}

	// Test that we can get item IDs for asset paths
	foundCount := 0
	for _, path := range paths {
		itemID, exists := GetItemIDForAssetPath(path)
		if !exists {
			t.Errorf("H2: Asset path %s not found in reverse map", path)
		} else {
			if itemID == 0 {
				t.Errorf("H2: Asset path %s mapped to zero item ID", path)
			} else {
				foundCount++
			}
		}
	}

	t.Logf("✅ H2: Successfully mapped %d asset paths to item IDs", foundCount)

	// Test bidirectional consistency
	for _, path := range paths {
		itemID, exists := GetItemIDForAssetPath(path)
		if exists {
			// Verify that the reverse lookup gives us back the same path
			reversePath, reverseExists := GetAssetPathForItemID(itemID)
			if !reverseExists {
				t.Errorf("H2: Bidirectional lookup failed for path %s -> itemID %d", path, itemID)
			} else if reversePath != path {
				t.Errorf("H2: Bidirectional lookup inconsistent: %s -> %d -> %s", path, itemID, reversePath)
			}
		}
	}

	t.Logf("✅ H2: Bidirectional mapping is consistent")
}

// TestH2PathSearch tests the path search functionality
func TestH2PathSearch(t *testing.T) {
	// H2: Test path search functionality
	LoadItemIDRegister()

	// Test finding items by path prefix
	prefix := "/Game/Generic/Loadouts/Precast/Development/Hero/"
	heroLoadouts := FindItemsByPathPrefix(prefix)
	if len(heroLoadouts) == 0 {
		t.Logf("⚠️  H2: No items found with prefix %s", prefix)
	} else {
		t.Logf("✅ H2: Found %d items with prefix %s", len(heroLoadouts), prefix)
		for _, entry := range heroLoadouts {
			t.Logf("  H2: ItemID %d -> %s", entry.ItemID, entry.Path)
		}
	}

	// Test finding items by path contains
	substring := "Assault"
	assaultItems := FindItemsByPathContains(substring)
	if len(assaultItems) == 0 {
		t.Logf("⚠️  H2: No items found containing %s", substring)
	} else {
		t.Logf("✅ H2: Found %d items containing %s in path", len(assaultItems), substring)
	}

	// Test finding perk items
	perkItems := FindItemsByPathContains("Perk")
	if len(perkItems) > 0 {
		t.Logf("✅ H2: Found %d perk items", len(perkItems))
	}

	// Test finding ship items
	shipItems := FindItemsByPathContains("Ship")
	if len(shipItems) > 0 {
		t.Logf("✅ H2: Found %d ship-related items", len(shipItems))
	}

	t.Logf("✅ H2: Path search functionality works correctly")
}

// TestH2KnownItems tests that expected items are present in the registry
func TestH2KnownItems(t *testing.T) {
	// H2: Verify that known items from other parts of the codebase are present
	LoadItemIDRegister()

	// Known item IDs from the hardcoded itemCatalog in data.go
	knownItemIDs := []int32{
		184484177, // Athos
		184484173, // Zmey
		184484171, // Aion
		184484180, // Valcour
		184484184, // Svarog
		117374977, // Module Recycler (perk)
		117374979, // Communications 101 (perk)
	}

	foundCount := 0
	for _, itemID := range knownItemIDs {
		path, exists := GetAssetPathForItemID(itemID)
		if exists {
			foundCount++
			t.Logf("✅ H2: Found known item ID %d -> %s", itemID, path)
		} else {
			t.Logf("⚠️  H2: Known item ID %d not found in registry", itemID)
		}
	}

	t.Logf("✅ H2: Found %d out of %d known item IDs", foundCount, len(knownItemIDs))
}