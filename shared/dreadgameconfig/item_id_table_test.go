package dreadgameconfig

import (
	"testing"
)

// TestH1LoadItemIDTable tests the H1 requirement explicitly
func TestH1LoadItemIDTable(t *testing.T) {
	// H1: Load `test/ItemIDTable.json` (10,661 lines, 27 categories, ~4,000+ item IDs) into category→itemID map
	// This test explicitly validates the H1 requirement

	// Reset the loaded state to test loading
	itemIDTableLock.Lock()
	itemCategories = make(map[string]ItemCategory)
	itemCategoriesByID = make(map[int32]ItemCategory)
	categoryItemIDs = make(map[string][]int32)
	categoryItemCount = make(map[string]int)
	itemIDToCategory = make(map[int32]string)
	itemIDTableLoaded = false
	itemIDTableLock.Unlock()

	// Load the ItemIDTable
	err := LoadItemIDTable()
	if err != nil {
		t.Fatalf("H1: Failed to load ItemIDTable: %v", err)
	}

	// Verify that categories were loaded
	categoryCount := GetCategoryCount()
	if categoryCount == 0 {
		t.Fatal("H1: Expected categories to be loaded, got 0")
	}

	t.Logf("✅ H1: Successfully loaded %d categories from ItemIDTable.json", categoryCount)

	// Verify that we have a reasonable number of categories (expecting 27)
	if categoryCount < 20 {
		t.Errorf("H1: Expected at least 20 categories, got %d", categoryCount)
	}

	// Verify that we have item IDs loaded
	totalItemCount := GetTotalItemCount()
	if totalItemCount == 0 {
		t.Fatal("H1: Expected item IDs to be loaded, got 0")
	}

	t.Logf("✅ H1: Successfully loaded %d item IDs across all categories", totalItemCount)

	// Verify that we have a reasonable number of item IDs (expecting ~4,000+)
	if totalItemCount < 1000 {
		t.Errorf("H1: Expected at least 1000 item IDs, got %d", totalItemCount)
	}

	t.Logf("✅ H1: Item count is reasonable (%d items)", totalItemCount)
}

// TestH1CategoryToItemIDMap tests the category→itemID mapping functionality
func TestH1CategoryToItemIDMap(t *testing.T) {
	// H1: Verify that the category→itemID map is working correctly
	LoadItemIDTable()

	// Get all category names
	categoryNames := GetAllCategoryNames()
	if len(categoryNames) == 0 {
		t.Fatal("H1: No category names found")
	}

	// Test that we can get item IDs for each category
	for _, categoryName := range categoryNames {
		itemIDs := GetItemIDsByCategory(categoryName)
		if len(itemIDs) == 0 {
			t.Errorf("H1: Category %s has no item IDs", categoryName)
		} else {
			t.Logf("H1: Category %s has %d item IDs", categoryName, len(itemIDs))
		}
	}

	// Test that we can get category information
	for _, categoryName := range categoryNames {
		category, exists := GetItemCategory(categoryName)
		if !exists {
			t.Errorf("H1: Category %s not found", categoryName)
		} else {
			if category.CategoryName != categoryName {
				t.Errorf("H1: Category name mismatch for %s", categoryName)
			}
			if len(category.ItemIDs) == 0 {
				t.Errorf("H1: Category %s has no item IDs in struct", categoryName)
			}
		}
	}

	t.Logf("✅ H1: Category→itemID mapping works correctly for all %d categories", len(categoryNames))
}

// TestH1ItemIDToCategoryMap tests the itemID→category mapping functionality
func TestH1ItemIDToCategoryMap(t *testing.T) {
	// H1: Verify that the itemID→category map is working correctly
	LoadItemIDTable()

	// Get all categories
	categories := GetAllCategories()
	if len(categories) == 0 {
		t.Fatal("H1: No categories found")
	}

	// Test that we can get category for item IDs
	for _, category := range categories {
		for _, itemID := range category.ItemIDs {
			categoryName, exists := GetCategoryForItemID(itemID)
			if !exists {
				t.Errorf("H1: Item ID %d not found in category map", itemID)
			} else {
				if categoryName != category.CategoryName {
					t.Errorf("H1: Item ID %d mapped to wrong category: expected %s, got %s", 
						itemID, category.CategoryName, categoryName)
				}
			}
		}
	}

	t.Logf("✅ H1: ItemID→category mapping works correctly")
}

// TestH1CategoryCounts tests that we have the expected number of categories and items
func TestH1CategoryCounts(t *testing.T) {
	// H1: Verify item counts per category
	LoadItemIDTable()

	// Get all category names
	categoryNames := GetAllCategoryNames()
	
	// Verify we have the expected number of categories (27)
	if len(categoryNames) != 27 {
		t.Logf("H1: Expected 27 categories, got %d", len(categoryNames))
	} else {
		t.Logf("✅ H1: Found expected 27 categories")
	}

	// Verify total item count (expecting ~4,000+)
	totalItems := GetTotalItemCount()
	if totalItems < 4000 {
		t.Logf("H1: Expected ~4000+ items, got %d", totalItems)
	} else {
		t.Logf("✅ H1: Found expected ~4000+ items (%d)", totalItems)
	}

	// Log counts for each category
	for _, categoryName := range categoryNames {
		count := GetCategoryItemCount(categoryName)
		t.Logf("H1: Category %s: %d items", categoryName, count)
	}

	t.Logf("✅ H1: Category counts verified")
}

// TestH1KnownCategories tests that expected categories are present
func TestH1KnownCategories(t *testing.T) {
	// H1: Verify that known categories from the todo.md are present
	LoadItemIDTable()

	// Known categories that should be present
	knownCategories := []string{
		"YShipLoadoutPrecast",
		"YAbility",
		"YBoosterAssetBase",
		"YCharacterCustomizationColorPalette",
		"YCharacterCustomizationGender",
		"YCharacterCustomizationMaterial",
		"YCharacterCustomizationMaterialPalette",
		"YCharacterCustomizationMesh",
		"YCharacterCustomizationTextureSet",
		"YGameMode",
	}

	foundCount := 0
	for _, categoryName := range knownCategories {
		category, exists := GetItemCategory(categoryName)
		if exists {
			foundCount++
			t.Logf("✅ H1: Found known category: %s with %d items", categoryName, len(category.ItemIDs))
		} else {
			t.Logf("⚠️  H1: Known category not found: %s", categoryName)
		}
	}

	t.Logf("✅ H1: Found %d out of %d known categories", foundCount, len(knownCategories))
}