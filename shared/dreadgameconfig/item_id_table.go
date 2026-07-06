package dreadgameconfig

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// ItemCategory represents a category of items from ItemIDTable.json
type ItemCategory struct {
	CategoryName string   `json:"CategoryName"`
	CategoryID   int32    `json:"CategoryID"`
	ItemIDs      []int32  `json:"-"` // Extracted from ItemIDs array
}

// itemIDTableData holds the loaded item ID table data
var (
	itemCategories     = make(map[string]ItemCategory) // categoryName -> ItemCategory
	itemCategoriesByID = make(map[int32]ItemCategory)  // categoryID -> ItemCategory
	categoryItemIDs    = make(map[string][]int32)       // categoryName -> []itemID
	categoryItemCount  = make(map[string]int)           // categoryName -> count
	itemIDToCategory   = make(map[int32]string)         // itemID -> categoryName
	itemIDTableLock    sync.RWMutex
	itemIDTableLoaded  bool
)

// LoadItemIDTable loads the ItemIDTable.json file into category→itemID maps
// H1: Load `test/ItemIDTable.json` (10,661 lines, 27 categories, ~4,000+ item IDs) into category→itemID map
func LoadItemIDTable() error {
	itemIDTableLock.Lock()
	defer itemIDTableLock.Unlock()

	if itemIDTableLoaded {
		return nil
	}

	// Path to the ItemIDTable.json file
	tablePath := filepath.Join("..", "..", "data", "assets", "ItemIDTable.json")
	
	data, err := os.ReadFile(tablePath)
	if err != nil {
		log.Printf("Warning: Failed to load ItemIDTable: %v", err)
		return err
	}

	var dataTable struct {
		ItemIDTable []struct {
			CategoryName string `json:"CategoryName"`
			CategoryID   int32  `json:"CategoryID"`
			ItemIDs      []struct {
				ItemID int32 `json:"ItemID"`
			} `json:"ItemIDs"`
		} `json:"ItemIDTable"`
	}

	if err := json.Unmarshal(data, &dataTable); err != nil {
		log.Printf("Warning: Failed to parse ItemIDTable: %v", err)
		return err
	}

	loadedCount := 0
	categoryCount := 0
	itemCount := 0

	for _, entry := range dataTable.ItemIDTable {
		// Skip empty categories
		if entry.CategoryName == "" {
			continue
		}

		// Extract item IDs from the array
		itemIDs := make([]int32, len(entry.ItemIDs))
		for i, itemIDObj := range entry.ItemIDs {
			itemIDs[i] = itemIDObj.ItemID
			
			// Map itemID to category
			itemIDToCategory[itemIDObj.ItemID] = entry.CategoryName
			itemCount++
		}

		// Create category
		category := ItemCategory{
			CategoryName: entry.CategoryName,
			CategoryID:   entry.CategoryID,
			ItemIDs:      itemIDs,
		}

		// Store in maps
		itemCategories[entry.CategoryName] = category
		itemCategoriesByID[entry.CategoryID] = category
		categoryItemIDs[entry.CategoryName] = itemIDs
		categoryItemCount[entry.CategoryName] = len(itemIDs)

		categoryCount++
		loadedCount += len(itemIDs)
	}

	itemIDTableLoaded = true
	log.Printf("Loaded %d item IDs across %d categories from %s", loadedCount, categoryCount, filepath.Base(tablePath))
	return nil
}

// GetItemCategory returns the category for a given category name
func GetItemCategory(categoryName string) (ItemCategory, bool) {
	itemIDTableLock.RLock()
	defer itemIDTableLock.RUnlock()

	category, exists := itemCategories[categoryName]
	return category, exists
}

// GetItemCategoryByID returns the category for a given category ID
func GetItemCategoryByID(categoryID int32) (ItemCategory, bool) {
	itemIDTableLock.RLock()
	defer itemIDTableLock.RUnlock()

	category, exists := itemCategoriesByID[categoryID]
	return category, exists
}

// GetItemIDsByCategory returns all item IDs for a given category
func GetItemIDsByCategory(categoryName string) []int32 {
	itemIDTableLock.RLock()
	defer itemIDTableLock.RUnlock()

	itemIDs, exists := categoryItemIDs[categoryName]
	if !exists {
		return []int32{}
	}
	return itemIDs
}

// GetCategoryForItemID returns the category name for a given item ID
func GetCategoryForItemID(itemID int32) (string, bool) {
	itemIDTableLock.RLock()
	defer itemIDTableLock.RUnlock()

	category, exists := itemIDToCategory[itemID]
	return category, exists
}

// GetAllCategories returns all loaded categories
func GetAllCategories() []ItemCategory {
	itemIDTableLock.RLock()
	defer itemIDTableLock.RUnlock()

	categories := make([]ItemCategory, 0, len(itemCategories))
	for _, category := range itemCategories {
		categories = append(categories, category)
	}
	return categories
}

// GetCategoryCount returns the number of loaded categories
func GetCategoryCount() int {
	itemIDTableLock.RLock()
	defer itemIDTableLock.RUnlock()
	return len(itemCategories)
}

// GetTotalItemCount returns the total number of item IDs across all categories
func GetTotalItemCount() int {
	itemIDTableLock.RLock()
	defer itemIDTableLock.RUnlock()

	total := 0
	for _, count := range categoryItemCount {
		total += count
	}
	return total
}

// GetCategoryItemCount returns the number of items in a specific category
func GetCategoryItemCount(categoryName string) int {
	itemIDTableLock.RLock()
	defer itemIDTableLock.RUnlock()

	count, exists := categoryItemCount[categoryName]
	if !exists {
		return 0
	}
	return count
}

// GetAllCategoryNames returns all category names
func GetAllCategoryNames() []string {
	itemIDTableLock.RLock()
	defer itemIDTableLock.RUnlock()

	names := make([]string, 0, len(itemCategories))
	for name := range itemCategories {
		names = append(names, name)
	}
	return names
}