package dreadgameconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// CatalogItemID can be either an int64 or a string to handle large IDs and special codes
type CatalogItemID struct {
	Value interface{} // Can be int64 or string
}

// CatalogBucket represents a catalog bucket with its name, item IDs, and type ID
type CatalogBucket struct {
	BucketName string          `json:"bucket_name"`
	ItemIDs    []CatalogItemID `json:"item_ids"`
	TypeID     int32           `json:"type_id"`
	ItemCount  int             `json:"item_count"`
}

// CatalogIDTable represents the complete catalog ID table structure
type CatalogIDTable struct {
	Buckets     map[string]CatalogBucket `json:"buckets"`
	TotalItems  int                      `json:"total_items"`
	BucketCount int                      `json:"bucket_count"`
}

var (
	catalogIDTable     CatalogIDTable
	catalogIDTableOnce sync.Once
	catalogIDTableMu   sync.RWMutex
)

// LoadCatalogIDTable loads the CatalogIDTable.json file and parses it into catalog buckets
func LoadCatalogIDTable() error {
	catalogIDTableOnce.Do(func() {
		filePath := AssetPath("CatalogIDTable.json")
		
		data, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Printf("Warning: Failed to load CatalogIDTable: %v\n", err)
			return
		}

		// Parse the raw JSON structure
		var rawData map[string]map[string]interface{}
		if err := json.Unmarshal(data, &rawData); err != nil {
			fmt.Printf("Warning: Failed to parse CatalogIDTable: %v\n", err)
			return
		}

		// Convert to our structured format
		buckets := make(map[string]CatalogBucket)
		totalItems := 0

		for bucketName, bucketData := range rawData {
			// Extract item IDs
			idsInterface, ok := bucketData["ids"]
			if !ok {
				fmt.Printf("Warning: Bucket '%s' has no 'ids' field\n", bucketName)
				continue
			}

			// Convert interface{} to []CatalogItemID
			var itemIDs []CatalogItemID
			if idsSlice, ok := idsInterface.([]interface{}); ok {
				itemIDs = make([]CatalogItemID, len(idsSlice))
				for i, idInterface := range idsSlice {
					// Handle different types: float64 (JSON numbers), int, int32, int64, string
					var itemID CatalogItemID
					switch v := idInterface.(type) {
					case float64:
						// Use int64 to preserve large numbers
						itemID.Value = int64(v)
					case int:
						itemID.Value = int64(v)
					case int32:
						itemID.Value = int64(v)
					case int64:
						itemID.Value = v
					case string:
						itemID.Value = v
					default:
						fmt.Printf("Warning: Unexpected type for item ID in bucket '%s': %T\n", bucketName, v)
						continue
					}
					itemIDs[i] = itemID
				}
			} else {
				fmt.Printf("Warning: Bucket '%s' has 'ids' field of unexpected type: %T\n", bucketName, idsInterface)
				continue
			}

			// Extract type_id
			var typeID int32 = 0
			if typeIDInterface, ok := bucketData["type_id"]; ok {
				switch v := typeIDInterface.(type) {
				case float64:
					typeID = int32(v)
				case int32:
					typeID = v
				case int:
					typeID = int32(v)
				case int64:
					typeID = int32(v)
				case string:
					// For un_typed, type_id is a string, we'll use 0
					if v != "un_typed" {
						fmt.Printf("Warning: Bucket '%s' has string type_id: %s\n", bucketName, v)
					}
					// Keep typeID as 0 for string type_id
				default:
					fmt.Printf("Warning: Bucket '%s' has type_id of unexpected type: %T\n", bucketName, typeIDInterface)
				}
			}

			buckets[bucketName] = CatalogBucket{
				BucketName: bucketName,
				ItemIDs:    itemIDs,
				TypeID:     typeID,
				ItemCount:  len(itemIDs),
			}
			totalItems += len(itemIDs)
		}

		catalogIDTable = CatalogIDTable{
			Buckets:     buckets,
			TotalItems:  totalItems,
			BucketCount: len(buckets),
		}

		fmt.Printf("Loaded %d catalog buckets with %d total items from CatalogIDTable.json\n",
			len(buckets), totalItems)
	})

	return nil
}

// GetCatalogBucket returns the catalog bucket by name
func GetCatalogBucket(bucketName string) (*CatalogBucket, bool) {
	catalogIDTableMu.RLock()
	defer catalogIDTableMu.RUnlock()

	bucket, exists := catalogIDTable.Buckets[bucketName]
	if exists {
		return &bucket, true
	}
	return nil, false
}

// GetCatalogBucketByItemID finds which bucket contains the given item ID (int64 version)
func GetCatalogBucketByItemID(itemID int64) (*CatalogBucket, bool) {
	catalogIDTableMu.RLock()
	defer catalogIDTableMu.RUnlock()

	for _, bucket := range catalogIDTable.Buckets {
		for _, id := range bucket.ItemIDs {
			// Compare based on the type of the stored ID
			switch v := id.Value.(type) {
			case int64:
				if v == itemID {
					return &bucket, true
				}
			case int32:
				if int64(v) == itemID {
					return &bucket, true
				}
			case int:
				if int64(v) == itemID {
					return &bucket, true
				}
			// String IDs cannot match numeric itemID
			}
		}
	}
	return nil, false
}

// GetAllCatalogBuckets returns all catalog buckets
func GetAllCatalogBuckets() []CatalogBucket {
	catalogIDTableMu.RLock()
	defer catalogIDTableMu.RUnlock()

	buckets := make([]CatalogBucket, 0, len(catalogIDTable.Buckets))
	for _, bucket := range catalogIDTable.Buckets {
		buckets = append(buckets, bucket)
	}
	return buckets
}

// GetAllCatalogBucketNames returns all catalog bucket names
func GetAllCatalogBucketNames() []string {
	catalogIDTableMu.RLock()
	defer catalogIDTableMu.RUnlock()

	names := make([]string, 0, len(catalogIDTable.Buckets))
	for name := range catalogIDTable.Buckets {
		names = append(names, name)
	}
	return names
}

// GetCatalogBucketCount returns the number of catalog buckets
func GetCatalogBucketCount() int {
	catalogIDTableMu.RLock()
	defer catalogIDTableMu.RUnlock()
	return catalogIDTable.BucketCount
}

// GetCatalogTotalItemCount returns the total number of items across all catalog buckets
func GetCatalogTotalItemCount() int {
	catalogIDTableMu.RLock()
	defer catalogIDTableMu.RUnlock()
	return catalogIDTable.TotalItems
}

// GetCatalogItemCount returns the number of items in a specific catalog bucket
func GetCatalogItemCount(bucketName string) (int, bool) {
	catalogIDTableMu.RLock()
	defer catalogIDTableMu.RUnlock()

	bucket, exists := catalogIDTable.Buckets[bucketName]
	if exists {
		return bucket.ItemCount, true
	}
	return 0, false
}

// GetCatalogItemIDs returns all item IDs in a specific catalog bucket
func GetCatalogItemIDs(bucketName string) ([]CatalogItemID, bool) {
	catalogIDTableMu.RLock()
	defer catalogIDTableMu.RUnlock()

	bucket, exists := catalogIDTable.Buckets[bucketName]
	if exists {
		// Return a copy to avoid race conditions
		ids := make([]CatalogItemID, len(bucket.ItemIDs))
		copy(ids, bucket.ItemIDs)
		return ids, true
	}
	return nil, false
}

// IsItemIDInCatalog checks if an item ID exists in any catalog bucket (int64 version)
func IsItemIDInCatalog(itemID int64) bool {
	_, exists := GetCatalogBucketByItemID(itemID)
	return exists
}

// IsStringItemIDInCatalog checks if a string item ID exists in any catalog bucket
func IsStringItemIDInCatalog(itemID string) bool {
	catalogIDTableMu.RLock()
	defer catalogIDTableMu.RUnlock()

	for _, bucket := range catalogIDTable.Buckets {
		for _, id := range bucket.ItemIDs {
			// Compare based on the type of the stored ID
			switch v := id.Value.(type) {
			case string:
				if v == itemID {
					return true
				}
			// Numeric IDs cannot match string itemID
			}
		}
	}
	return false
}

// GetCatalogBucketsForItemIDs returns all buckets that contain any of the given item IDs (int64 version)
func GetCatalogBucketsForItemIDs(itemIDs []int64) []CatalogBucket {
	catalogIDTableMu.RLock()
	defer catalogIDTableMu.RUnlock()

	var result []CatalogBucket
	itemIDSet := make(map[int64]bool)
	for _, id := range itemIDs {
		itemIDSet[id] = true
	}

	for _, bucket := range catalogIDTable.Buckets {
		for _, id := range bucket.ItemIDs {
			// Check if this ID matches any in the set
			switch v := id.Value.(type) {
			case int64:
				if itemIDSet[v] {
					result = append(result, bucket)
					break
				}
			case int32:
				if itemIDSet[int64(v)] {
					result = append(result, bucket)
					break
				}
			case int:
				if itemIDSet[int64(v)] {
					result = append(result, bucket)
					break
				}
			// String IDs cannot match numeric itemIDs in the set
			}
		}
	}

	return result
}

// FindCatalogItemsByName searches for catalog buckets by name (case-insensitive substring match)
func FindCatalogItemsByName(searchTerm string) []CatalogBucket {
	catalogIDTableMu.RLock()
	defer catalogIDTableMu.RUnlock()

	var result []CatalogBucket
	lowerSearch := toLower(searchTerm)

	for _, bucket := range catalogIDTable.Buckets {
		if containsIgnoreCase(bucket.BucketName, lowerSearch) {
			result = append(result, bucket)
		}
	}

	return result
}

// toLower converts a string to lowercase
func toLower(s string) string {
	result := ""
	for _, c := range s {
		if c >= 'A' && c <= 'Z' {
			result += string(c + 32)
		} else {
			result += string(c)
		}
	}
	return result
}

// containsIgnoreCase checks if a string contains a substring (case-insensitive)
func containsIgnoreCase(s, substr string) bool {
	lowerS := toLower(s)
	lowerSubstr := toLower(substr)
	return containsSubstring(lowerS, lowerSubstr)
}

// containsSubstring checks if s contains substr
func containsSubstring(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
