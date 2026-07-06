package dreadgameconfig

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// ItemRegistryEntry represents an entry from ItemIDRegister.json
type ItemRegistryEntry struct {
	ItemID int32  `json:"ItemID"`
	Path   string `json:"Path"`
}

// itemIDRegisterData holds the loaded item ID register data
var (
	itemRegistry     = make(map[int32]ItemRegistryEntry) // itemID -> ItemRegistryEntry
	pathToItemID     = make(map[string]int32)             // path -> itemID (reverse lookup)
	itemRegistryLock sync.RWMutex
	itemRegistryLoaded bool
)

// LoadItemIDRegister loads the ItemIDRegister.json file into itemID→assetPath map
// H2: Load `ItemIDRegister.json` (12,349 lines, 3,086 entries) into itemID→assetPath map
func LoadItemIDRegister() error {
	itemRegistryLock.Lock()
	defer itemRegistryLock.Unlock()

	if itemRegistryLoaded {
		return nil
	}

	// Path to the ItemIDRegister.json file
	registerPath := filepath.Join("..", "..", "data", "assets", "ItemIDRegister.json")
	
	data, err := os.ReadFile(registerPath)
	if err != nil {
		log.Printf("Warning: Failed to load ItemIDRegister: %v", err)
		return err
	}

	var dataTable struct {
		ItemIDRegister []ItemRegistryEntry `json:"ItemIDRegister"`
	}

	if err := json.Unmarshal(data, &dataTable); err != nil {
		log.Printf("Warning: Failed to parse ItemIDRegister: %v", err)
		return err
	}

	loadedCount := 0
	for _, entry := range dataTable.ItemIDRegister {
		// Store in itemID map
		itemRegistry[entry.ItemID] = entry
		
		// Store in reverse lookup map
		pathToItemID[entry.Path] = entry.ItemID
		
		loadedCount++
	}

	itemRegistryLoaded = true
	log.Printf("Loaded %d entries from %s", loadedCount, filepath.Base(registerPath))
	return nil
}

// GetItemRegistryEntry returns the registry entry for a given item ID
func GetItemRegistryEntry(itemID int32) (ItemRegistryEntry, bool) {
	itemRegistryLock.RLock()
	defer itemRegistryLock.RUnlock()

	entry, exists := itemRegistry[itemID]
	return entry, exists
}

// GetAssetPathForItemID returns the asset path for a given item ID
func GetAssetPathForItemID(itemID int32) (string, bool) {
	entry, exists := GetItemRegistryEntry(itemID)
	if !exists {
		return "", false
	}
	return entry.Path, true
}

// GetItemIDForAssetPath returns the item ID for a given asset path (reverse lookup)
func GetItemIDForAssetPath(path string) (int32, bool) {
	itemRegistryLock.RLock()
	defer itemRegistryLock.RUnlock()

	itemID, exists := pathToItemID[path]
	return itemID, exists
}

// GetAllRegistryEntries returns all loaded registry entries
func GetAllRegistryEntries() []ItemRegistryEntry {
	itemRegistryLock.RLock()
	defer itemRegistryLock.RUnlock()

	entries := make([]ItemRegistryEntry, 0, len(itemRegistry))
	for _, entry := range itemRegistry {
		entries = append(entries, entry)
	}
	return entries
}

// GetRegistryEntryCount returns the number of loaded registry entries
func GetRegistryEntryCount() int {
	itemRegistryLock.RLock()
	defer itemRegistryLock.RUnlock()
	return len(itemRegistry)
}

// GetAllItemIDs returns all item IDs from the registry
func GetAllItemIDs() []int32 {
	itemRegistryLock.RLock()
	defer itemRegistryLock.RUnlock()

	ids := make([]int32, 0, len(itemRegistry))
	for id := range itemRegistry {
		ids = append(ids, id)
	}
	return ids
}

// GetAllAssetPaths returns all asset paths from the registry
func GetAllAssetPaths() []string {
	itemRegistryLock.RLock()
	defer itemRegistryLock.RUnlock()

	paths := make([]string, 0, len(itemRegistry))
	for _, entry := range itemRegistry {
		paths = append(paths, entry.Path)
	}
	return paths
}

// FindItemsByPathPrefix returns all items whose asset path starts with the given prefix
func FindItemsByPathPrefix(prefix string) []ItemRegistryEntry {
	itemRegistryLock.RLock()
	defer itemRegistryLock.RUnlock()

	var results []ItemRegistryEntry
	for _, entry := range itemRegistry {
		if len(entry.Path) >= len(prefix) && entry.Path[:len(prefix)] == prefix {
			results = append(results, entry)
		}
	}
	return results
}

// FindItemsByPathContains returns all items whose asset path contains the given substring
func FindItemsByPathContains(substring string) []ItemRegistryEntry {
	itemRegistryLock.RLock()
	defer itemRegistryLock.RUnlock()

	var results []ItemRegistryEntry
	for _, entry := range itemRegistry {
		if len(entry.Path) >= len(substring) {
			// Simple contains check
			for i := 0; i <= len(entry.Path)-len(substring); i++ {
				if entry.Path[i:i+len(substring)] == substring {
					results = append(results, entry)
					break
				}
			}
		}
	}
	return results
}