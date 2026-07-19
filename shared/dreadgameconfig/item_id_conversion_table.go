package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// ItemIDConversionEntry represents a single conversion mapping from old to new item ID
type ItemIDConversionEntry struct {
	Name      string `json:"name"`
	Asset     string `json:"asset"`
	OldItemID int64  `json:"old_item_id"`
	NewItemID int64  `json:"new_item_id"`
}

// ItemIDConversionTable represents the complete item ID conversion table
type ItemIDConversionTable struct {
	Entries      []ItemIDConversionEntry `json:"entries"`
	EntryCount  int                      `json:"entry_count"`
	OldToNewMap map[int64]int64           `json:"-"`  // oldItemID -> newItemID
	NewToOldMap map[int64]int64           `json:"-"`  // newItemID -> oldItemID
}

var (
	itemIDConversionTable     ItemIDConversionTable
	itemIDConversionTableOnce sync.Once
	itemIDConversionTableMu   sync.RWMutex
	itemIDConversionTableLoadErr error
)

// LoadItemIDConversionTable loads the ItemIDConversionTable.json file and creates bidirectional mappings
func LoadItemIDConversionTable() error {
	itemIDConversionTableOnce.Do(func() {
		filePath := AssetPath("ItemIDConversionTable.json")

		data, err := os.ReadFile(filePath)
		if err != nil {
			itemIDConversionTableLoadErr = fmt.Errorf("failed to load ItemIDConversionTable: %w", err)
			return
		}

		// Parse the raw JSON structure
		var rawData struct {
			ItemIDLookUpTable []struct {
				Name      string `json:"Name"`
				Asset     string `json:"Asset"`
				OldItemID int64  `json:"OldItemID"`
				NewItemID int64  `json:"NewItemID"`
			} `json:"ItemIDLookUpTable"`
		}

		if err := json.Unmarshal(data, &rawData); err != nil {
			itemIDConversionTableLoadErr = fmt.Errorf("failed to parse ItemIDConversionTable: %w", err)
			return
		}

		// Convert to our structured format
		entries := make([]ItemIDConversionEntry, len(rawData.ItemIDLookUpTable))
		oldToNewMap := make(map[int64]int64)
		newToOldMap := make(map[int64]int64)

		for i, rawEntry := range rawData.ItemIDLookUpTable {
			entry := ItemIDConversionEntry{
				Name:      rawEntry.Name,
				Asset:     rawEntry.Asset,
				OldItemID: rawEntry.OldItemID,
				NewItemID: rawEntry.NewItemID,
			}
			entries[i] = entry
			
			// Create bidirectional mappings
			oldToNewMap[rawEntry.OldItemID] = rawEntry.NewItemID
			newToOldMap[rawEntry.NewItemID] = rawEntry.OldItemID
		}

		itemIDConversionTable = ItemIDConversionTable{
			Entries:      entries,
			EntryCount:  len(entries),
			OldToNewMap: oldToNewMap,
			NewToOldMap: newToOldMap,
		}

		fmt.Printf("Loaded %d item ID conversion entries from ItemIDConversionTable.json\n",
			len(entries))
	})

	return itemIDConversionTableLoadErr
}

// GetItemIDConversionEntry returns the conversion entry by old item ID
func GetItemIDConversionEntry(oldItemID int64) (*ItemIDConversionEntry, bool) {
	itemIDConversionTableMu.RLock()
	defer itemIDConversionTableMu.RUnlock()

	// Check if the old item ID exists in the map
	_, exists := itemIDConversionTable.OldToNewMap[oldItemID]
	if !exists {
		return nil, false
	}

	// Find the entry in the entries slice
	for _, entry := range itemIDConversionTable.Entries {
		if entry.OldItemID == oldItemID {
			return &entry, true
		}
	}
	
	// Should not happen if maps are consistent
	return nil, false
}

// GetItemIDConversionEntryByNewID returns the conversion entry by new item ID
func GetItemIDConversionEntryByNewID(newItemID int64) (*ItemIDConversionEntry, bool) {
	itemIDConversionTableMu.RLock()
	defer itemIDConversionTableMu.RUnlock()

	// Check if the new item ID exists in the map
	_, exists := itemIDConversionTable.NewToOldMap[newItemID]
	if !exists {
		return nil, false
	}

	// Find the entry in the entries slice
	for _, entry := range itemIDConversionTable.Entries {
		if entry.NewItemID == newItemID {
			return &entry, true
		}
	}
	
	// Should not happen if maps are consistent
	return nil, false
}

// ConvertOldToNewItemID converts an old item ID to its new equivalent
func ConvertOldToNewItemID(oldItemID int64) (int64, bool) {
	itemIDConversionTableMu.RLock()
	defer itemIDConversionTableMu.RUnlock()

	newItemID, exists := itemIDConversionTable.OldToNewMap[oldItemID]
	return newItemID, exists
}

// ConvertNewToOldItemID converts a new item ID back to its old equivalent
func ConvertNewToOldItemID(newItemID int64) (int64, bool) {
	itemIDConversionTableMu.RLock()
	defer itemIDConversionTableMu.RUnlock()

	oldItemID, exists := itemIDConversionTable.NewToOldMap[newItemID]
	return oldItemID, exists
}

// GetAllItemIDConversionEntries returns all conversion entries
func GetAllItemIDConversionEntries() []ItemIDConversionEntry {
	itemIDConversionTableMu.RLock()
	defer itemIDConversionTableMu.RUnlock()

	// Return a copy to avoid race conditions
	entries := make([]ItemIDConversionEntry, len(itemIDConversionTable.Entries))
	copy(entries, itemIDConversionTable.Entries)
	return entries
}

// GetItemIDConversionEntryCount returns the number of conversion entries
func GetItemIDConversionEntryCount() int {
	itemIDConversionTableMu.RLock()
	defer itemIDConversionTableMu.RUnlock()
	return itemIDConversionTable.EntryCount
}

// GetAllOldItemIDs returns all old item IDs from the conversion table
func GetAllOldItemIDs() []int64 {
	itemIDConversionTableMu.RLock()
	defer itemIDConversionTableMu.RUnlock()

	oldIDs := make([]int64, 0, len(itemIDConversionTable.OldToNewMap))
	for oldID := range itemIDConversionTable.OldToNewMap {
		oldIDs = append(oldIDs, oldID)
	}
	return oldIDs
}

// GetAllNewItemIDs returns all new item IDs from the conversion table
func GetAllNewItemIDs() []int64 {
	itemIDConversionTableMu.RLock()
	defer itemIDConversionTableMu.RUnlock()

	newIDs := make([]int64, 0, len(itemIDConversionTable.NewToOldMap))
	for newID := range itemIDConversionTable.NewToOldMap {
		newIDs = append(newIDs, newID)
	}
	return newIDs
}

// FindConversionEntriesByName searches for conversion entries by name (case-insensitive substring match)
func FindConversionEntriesByName(searchTerm string) []ItemIDConversionEntry {
	itemIDConversionTableMu.RLock()
	defer itemIDConversionTableMu.RUnlock()

	var result []ItemIDConversionEntry
	lowerSearch := toLower(searchTerm)

	for _, entry := range itemIDConversionTable.Entries {
		if containsIgnoreCase(entry.Name, lowerSearch) {
			result = append(result, entry)
		}
	}

	return result
}

// FindConversionEntriesByAsset searches for conversion entries by asset path (case-insensitive substring match)
func FindConversionEntriesByAsset(searchTerm string) []ItemIDConversionEntry {
	itemIDConversionTableMu.RLock()
	defer itemIDConversionTableMu.RUnlock()

	var result []ItemIDConversionEntry
	lowerSearch := toLower(searchTerm)

	for _, entry := range itemIDConversionTable.Entries {
		if containsIgnoreCase(entry.Asset, lowerSearch) {
			result = append(result, entry)
		}
	}

	return result
}

// BatchConvertOldToNewItemIDs converts multiple old item IDs to new item IDs
func BatchConvertOldToNewItemIDs(oldItemIDs []int64) map[int64]int64 {
	itemIDConversionTableMu.RLock()
	defer itemIDConversionTableMu.RUnlock()

	result := make(map[int64]int64)
	for _, oldID := range oldItemIDs {
		if newID, exists := itemIDConversionTable.OldToNewMap[oldID]; exists {
			result[oldID] = newID
		}
	}
	return result
}

// BatchConvertNewToOldItemIDs converts multiple new item IDs back to old item IDs
func BatchConvertNewToOldItemIDs(newItemIDs []int64) map[int64]int64 {
	itemIDConversionTableMu.RLock()
	defer itemIDConversionTableMu.RUnlock()

	result := make(map[int64]int64)
	for _, newID := range newItemIDs {
		if oldID, exists := itemIDConversionTable.NewToOldMap[newID]; exists {
			result[newID] = oldID
		}
	}
	return result
}

// IsOldItemIDInConversionTable checks if an old item ID exists in the conversion table
func IsOldItemIDInConversionTable(oldItemID int64) bool {
	_, exists := ConvertOldToNewItemID(oldItemID)
	return exists
}

// IsNewItemIDInConversionTable checks if a new item ID exists in the conversion table
func IsNewItemIDInConversionTable(newItemID int64) bool {
	_, exists := ConvertNewToOldItemID(newItemID)
	return exists
}