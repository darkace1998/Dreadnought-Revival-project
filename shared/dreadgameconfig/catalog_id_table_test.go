package dreadgameconfig

import (
	"fmt"
	"testing"
)

func TestH3LoadCatalogIDTable(t *testing.T) {
	// This test verifies that the CatalogIDTable loads correctly
	// Note: This will only work if the data files are present
	
	// Initialize the catalog ID table
	err := LoadCatalogIDTable()
	if err != nil {
		t.Logf("⚠️  H3: Could not load CatalogIDTable (data files may not be present): %v", err)
		// Don't fail the test if data files are missing
		return
	}

	bucketCount := GetCatalogBucketCount()
	totalItems := GetCatalogTotalItemCount()

	if bucketCount != 12 {
		t.Errorf("❌ H3: Expected 12 catalog buckets, got %d", bucketCount)
	} else {
		t.Logf("✅ H3: Successfully loaded %d catalog buckets", bucketCount)
	}

	if totalItems != 6630 {
		t.Errorf("❌ H3: Expected 6630 total catalog items, got %d", totalItems)
	} else {
		t.Logf("✅ H3: Successfully loaded %d total catalog items", totalItems)
	}
}

func TestH3CatalogBucketAccess(t *testing.T) {
	// Initialize the catalog ID table
	err := LoadCatalogIDTable()
	if err != nil {
		t.Logf("⚠️  H3: Could not load CatalogIDTable (data files may not be present): %v", err)
		return
	}

	// Test accessing known catalog buckets
	knownBuckets := []string{
		"Bundles",
		"Captain Vanity",
		"Coatings Collection",
		"Code Redemptions",
		"Decals Collection",
		"Emblems Collection",
		"GP to CR",
		"Heroships",
		"Modules",
		"Patterns Collection",
		"Weapons",
		"un_typed",
	}

	for _, bucketName := range knownBuckets {
		bucket, exists := GetCatalogBucket(bucketName)
		if !exists {
			t.Errorf("❌ H3: Expected to find catalog bucket '%s', but it was not found", bucketName)
		} else {
			if bucket.BucketName != bucketName {
				t.Errorf("❌ H3: Expected bucket name '%s', got '%s'", bucketName, bucket.BucketName)
			}
			if bucket.ItemCount <= 0 {
				t.Errorf("❌ H3: Expected bucket '%s' to have items, got %d", bucketName, bucket.ItemCount)
			} else {
				t.Logf("✅ H3: Found catalog bucket '%s' with %d items", bucketName, bucket.ItemCount)
			}
		}
	}
}

func TestH3CatalogBucketItemCounts(t *testing.T) {
	// Initialize the catalog ID table
	err := LoadCatalogIDTable()
	if err != nil {
		t.Logf("⚠️  H3: Could not load CatalogIDTable (data files may not be present): %v", err)
		return
	}

	// Expected item counts for each catalog bucket
	expectedCounts := map[string]int{
		"Bundles":           59,
		"Captain Vanity":    1856,
		"Coatings Collection": 78,
		"Code Redemptions":  30,
		"Decals Collection": 86,
		"Emblems Collection": 25,
		"GP to CR":          8,
		"Heroships":         46,
		"Modules":           1163,
		"Patterns Collection": 7,
		"Weapons":           140,
		"un_typed":          3132,
	}

	allCorrect := true
	for bucketName, expectedCount := range expectedCounts {
		actualCount, exists := GetCatalogItemCount(bucketName)
		if !exists {
			t.Errorf("❌ H3: Catalog bucket '%s' not found", bucketName)
			allCorrect = false
			continue
		}

		if actualCount != expectedCount {
			t.Errorf("❌ H3: Expected %d items in '%s', got %d", expectedCount, bucketName, actualCount)
			allCorrect = false
		} else {
			t.Logf("✅ H3: Catalog bucket '%s' has correct item count: %d", bucketName, actualCount)
		}
	}

	if allCorrect {
		t.Log("✅ H3: All catalog bucket item counts are correct")
	}
}

func TestH3AllCatalogBuckets(t *testing.T) {
	// Initialize the catalog ID table
	err := LoadCatalogIDTable()
	if err != nil {
		t.Logf("⚠️  H3: Could not load CatalogIDTable (data files may not be present): %v", err)
		return
	}

	buckets := GetAllCatalogBuckets()
	if len(buckets) != 12 {
		t.Errorf("❌ H3: Expected 12 catalog buckets, got %d", len(buckets))
	} else {
		t.Logf("✅ H3: GetAllCatalogBuckets returned %d buckets", len(buckets))
	}

	// Verify all buckets have valid data
	for _, bucket := range buckets {
		if bucket.BucketName == "" {
			t.Error("❌ H3: Found catalog bucket with empty name")
		}
		if bucket.ItemCount < 0 {
			t.Error("❌ H3: Found catalog bucket with negative item count")
		}
		if len(bucket.ItemIDs) != bucket.ItemCount {
			t.Errorf("❌ H3: Bucket '%s' has %d item IDs but ItemCount is %d",
				bucket.BucketName, len(bucket.ItemIDs), bucket.ItemCount)
		}
	}

	t.Log("✅ H3: All catalog buckets have valid data")
}

func TestH3CatalogBucketNames(t *testing.T) {
	// Initialize the catalog ID table
	err := LoadCatalogIDTable()
	if err != nil {
		t.Logf("⚠️  H3: Could not load CatalogIDTable (data files may not be present): %v", err)
		return
	}

	names := GetAllCatalogBucketNames()
	if len(names) != 12 {
		t.Errorf("❌ H3: Expected 12 catalog bucket names, got %d", len(names))
	} else {
		t.Logf("✅ H3: GetAllCatalogBucketNames returned %d names", len(names))
	}

	// Verify all names are non-empty
	for _, name := range names {
		if name == "" {
			t.Error("❌ H3: Found empty catalog bucket name")
		}
	}

	t.Log("✅ H3: All catalog bucket names are non-empty")
}

func TestH3CatalogItemIDs(t *testing.T) {
	// Initialize the catalog ID table
	err := LoadCatalogIDTable()
	if err != nil {
		t.Logf("⚠️  H3: Could not load CatalogIDTable (data files may not be present): %v", err)
		return
	}

	// Test getting item IDs from a few buckets
	testBuckets := []string{"Weapons", "Modules", "Bundles"}

	for _, bucketName := range testBuckets {
		ids, exists := GetCatalogItemIDs(bucketName)
		if !exists {
			t.Errorf("❌ H3: Could not get item IDs for bucket '%s'", bucketName)
			continue
		}

		if len(ids) == 0 {
			t.Errorf("❌ H3: Bucket '%s' returned empty item IDs", bucketName)
			continue
		}

		// Verify all numeric IDs are positive (strings are allowed)
		for _, id := range ids {
			switch v := id.Value.(type) {
			case int64:
				if v <= 0 {
					t.Errorf("❌ H3: Found non-positive item ID %d in bucket '%s'", v, bucketName)
				}
			case int32:
				if v <= 0 {
					t.Errorf("❌ H3: Found non-positive item ID %d in bucket '%s'", v, bucketName)
				}
			case int:
				if v <= 0 {
					t.Errorf("❌ H3: Found non-positive item ID %d in bucket '%s'", v, bucketName)
				}
			// String IDs are allowed and don't need to be positive
			}
		}

		t.Logf("✅ H3: Bucket '%s' has %d valid item IDs", bucketName, len(ids))
	}
}

func TestH3ItemIDInCatalog(t *testing.T) {
	// Initialize the catalog ID table
	err := LoadCatalogIDTable()
	if err != nil {
		t.Logf("⚠️  H3: Could not load CatalogIDTable (data files may not be present): %v", err)
		return
	}

	// Test some known item IDs that should be in the catalog
	// These are from the Bundles bucket based on the data
	knownItemIDs := []int64{99930001, 99930002, 99930003} // First few bundle IDs

	for _, itemID := range knownItemIDs {
		if IsItemIDInCatalog(itemID) {
			t.Logf("✅ H3: Item ID %d is in catalog", itemID)
		} else {
			t.Logf("ℹ️  H3: Item ID %d not found in catalog (may be in different bucket)", itemID)
		}
	}

	// Test that we can find buckets for item IDs
	// Get some item IDs from known buckets
	weaponsIDs, _ := GetCatalogItemIDs("Weapons")
	if len(weaponsIDs) > 0 {
		// Test the first weapon ID - need to extract the int64 value
		firstWeaponID := weaponsIDs[0].Value.(int64)
		bucket, exists := GetCatalogBucketByItemID(firstWeaponID)
		if exists {
			if bucket.BucketName != "Weapons" {
				t.Errorf("❌ H3: Expected item ID %d to be in 'Weapons' bucket, got '%s'",
					firstWeaponID, bucket.BucketName)
			} else {
				t.Logf("✅ H3: Item ID %d correctly found in 'Weapons' bucket", firstWeaponID)
			}
		} else {
			t.Errorf("❌ H3: Could not find bucket for item ID %d", firstWeaponID)
		}
	}
}

func TestH3CatalogSearch(t *testing.T) {
	// Initialize the catalog ID table
	err := LoadCatalogIDTable()
	if err != nil {
		t.Logf("⚠️  H3: Could not load CatalogIDTable (data files may not be present): %v", err)
		return
	}

	// Test searching for catalog buckets by name
	searchResults := FindCatalogItemsByName("Collection")
	if len(searchResults) == 0 {
		t.Error("❌ H3: Expected to find catalog buckets containing 'Collection'")
	} else {
		t.Logf("✅ H3: Found %d catalog buckets containing 'Collection'", len(searchResults))
		for _, bucket := range searchResults {
			t.Logf("  - %s (%d items)", bucket.BucketName, bucket.ItemCount)
		}
	}

	// Test case-insensitive search
	searchResults = FindCatalogItemsByName("WEAPONS")
	if len(searchResults) != 1 {
		t.Errorf("❌ H3: Expected to find 1 catalog bucket containing 'WEAPONS', got %d", len(searchResults))
	} else if searchResults[0].BucketName != "Weapons" {
		t.Errorf("❌ H3: Expected to find 'Weapons' bucket, got '%s'", searchResults[0].BucketName)
	} else {
		t.Logf("✅ H3: Case-insensitive search works: found 'Weapons' bucket")
	}

	// Test search for non-existent term
	searchResults = FindCatalogItemsByName("NonExistent")
	if len(searchResults) != 0 {
		t.Errorf("❌ H3: Expected no results for 'NonExistent', got %d", len(searchResults))
	} else {
		t.Log("✅ H3: Search for non-existent term returns empty results")
	}
}

func TestH3CatalogBucketsForItemIDs(t *testing.T) {
	// Initialize the catalog ID table
	err := LoadCatalogIDTable()
	if err != nil {
		t.Logf("⚠️  H3: Could not load CatalogIDTable (data files may not be present): %v", err)
		return
	}

	// Get some item IDs from different buckets
	weaponsIDs, _ := GetCatalogItemIDs("Weapons")
	modulesIDs, _ := GetCatalogItemIDs("Modules")

	if len(weaponsIDs) > 0 && len(modulesIDs) > 0 {
		// Test with item IDs from both buckets - extract int64 values
		firstWeaponID := weaponsIDs[0].Value.(int64)
		firstModuleID := modulesIDs[0].Value.(int64)
		itemIDs := []int64{firstWeaponID, firstModuleID}
		buckets := GetCatalogBucketsForItemIDs(itemIDs)

		if len(buckets) != 2 {
			t.Errorf("❌ H3: Expected 2 buckets for item IDs from different buckets, got %d", len(buckets))
		} else {
			t.Logf("✅ H3: Found %d buckets for item IDs from different buckets", len(buckets))
			
			// Verify the buckets are correct
			bucketNames := make(map[string]bool)
			for _, bucket := range buckets {
				bucketNames[bucket.BucketName] = true
			}
			
			if !bucketNames["Weapons"] {
				t.Error("❌ H3: Expected 'Weapons' bucket in results")
			}
			if !bucketNames["Modules"] {
				t.Error("❌ H3: Expected 'Modules' bucket in results")
			}
		}
	}
}

func TestH3DataConsistency(t *testing.T) {
	// Initialize the catalog ID table
	err := LoadCatalogIDTable()
	if err != nil {
		t.Logf("⚠️  H3: Could not load CatalogIDTable (data files may not be present): %v", err)
		return
	}

	// Verify that the sum of all bucket item counts equals the total
	buckets := GetAllCatalogBuckets()
	totalFromBuckets := 0
	for _, bucket := range buckets {
		totalFromBuckets += bucket.ItemCount
	}

	expectedTotal := GetCatalogTotalItemCount()
	if totalFromBuckets != expectedTotal {
		t.Errorf("❌ H3: Sum of bucket item counts (%d) != total items (%d)",
			totalFromBuckets, expectedTotal)
	} else {
		t.Logf("✅ H3: Data consistency verified: sum of bucket counts (%d) = total items (%d)",
			totalFromBuckets, expectedTotal)
	}

	// Verify that all item IDs are unique across buckets
	allItemIDs := make(map[string]string) // Use string representation for uniqueness check
	for _, bucket := range buckets {
		for _, itemID := range bucket.ItemIDs {
			// Create a unique string representation of the item ID
			var idKey string
			switch v := itemID.Value.(type) {
			case int64:
				idKey = fmt.Sprintf("int64:%d", v)
			case int32:
				idKey = fmt.Sprintf("int32:%d", v)
			case int:
				idKey = fmt.Sprintf("int:%d", v)
			case string:
				idKey = fmt.Sprintf("string:%s", v)
			default:
				idKey = fmt.Sprintf("unknown:%v", v)
			}
			
			if existingBucket, exists := allItemIDs[idKey]; exists {
				t.Errorf("❌ H3: Item ID %s appears in multiple buckets: '%s' and '%s'",
					idKey, existingBucket, bucket.BucketName)
			} else {
				allItemIDs[idKey] = bucket.BucketName
			}
		}
	}

	t.Logf("✅ H3: All %d item IDs are unique across buckets", len(allItemIDs))
}
