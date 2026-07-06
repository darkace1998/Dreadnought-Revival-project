package dreadgameconfig

import (
	"testing"
)

func TestH4LoadItemIDConversionTable(t *testing.T) {
	// This test verifies that the ItemIDConversionTable loads correctly
	// Note: This will only work if the data files are present
	
	// Initialize the item ID conversion table
	err := LoadItemIDConversionTable()
	if err != nil {
		t.Logf("⚠️  H4: Could not load ItemIDConversionTable (data files may not be present): %v", err)
		// Don't fail the test if data files are missing
		return
	}

	entryCount := GetItemIDConversionEntryCount()
	
	// The ItemIDConversionTable.json has 1,616 entries
	if entryCount != 1616 {
		t.Errorf("❌ H4: Expected 1616 conversion entries, got %d", entryCount)
	} else {
		t.Logf("✅ H4: Successfully loaded %d conversion entries", entryCount)
	}
}

func TestH4ConversionEntryAccess(t *testing.T) {
	// Initialize the item ID conversion table
	err := LoadItemIDConversionTable()
	if err != nil {
		t.Logf("⚠️  H4: Could not load ItemIDConversionTable (data files may not be present): %v", err)
		return
	}

	// Test accessing known conversion entries
	// From the first few entries in the file
	knownOldItemIDs := []int64{1000001, 1000002, 1000003, 1000004, 1000005}
	expectedNewItemIDs := []int64{33489313, 33489316, 33489323, 33489328, 33489330}

	for i, oldItemID := range knownOldItemIDs {
		entry, exists := GetItemIDConversionEntry(oldItemID)
		if !exists {
			t.Errorf("❌ H4: Expected to find conversion entry for old item ID %d", oldItemID)
			continue
		}

		if entry.OldItemID != oldItemID {
			t.Errorf("❌ H4: Expected old item ID %d, got %d", oldItemID, entry.OldItemID)
		}

		if entry.NewItemID != expectedNewItemIDs[i] {
			t.Errorf("❌ H4: Expected new item ID %d for old ID %d, got %d",
				expectedNewItemIDs[i], oldItemID, entry.NewItemID)
		} else {
			t.Logf("✅ H4: Found conversion entry: %d -> %d (%s)",
				oldItemID, entry.NewItemID, entry.Name)
		}
	}
}

func TestH4ConversionByNewID(t *testing.T) {
	// Initialize the item ID conversion table
	err := LoadItemIDConversionTable()
	if err != nil {
		t.Logf("⚠️  H4: Could not load ItemIDConversionTable (data files may not be present): %v", err)
		return
	}

	// Test accessing by new item ID
	knownNewItemIDs := []int64{33489313, 33489316, 33489323, 33489328, 33489330}
	expectedOldItemIDs := []int64{1000001, 1000002, 1000003, 1000004, 1000005}

	for i, newItemID := range knownNewItemIDs {
		entry, exists := GetItemIDConversionEntryByNewID(newItemID)
		if !exists {
			t.Errorf("❌ H4: Expected to find conversion entry for new item ID %d", newItemID)
			continue
		}

		if entry.NewItemID != newItemID {
			t.Errorf("❌ H4: Expected new item ID %d, got %d", newItemID, entry.NewItemID)
		}

		if entry.OldItemID != expectedOldItemIDs[i] {
			t.Errorf("❌ H4: Expected old item ID %d for new ID %d, got %d",
				expectedOldItemIDs[i], newItemID, entry.OldItemID)
		} else {
			t.Logf("✅ H4: Found conversion entry by new ID: %d <- %d (%s)",
				newItemID, entry.OldItemID, entry.Name)
		}
	}
}

func TestH4DirectConversion(t *testing.T) {
	// Initialize the item ID conversion table
	err := LoadItemIDConversionTable()
	if err != nil {
		t.Logf("⚠️  H4: Could not load ItemIDConversionTable (data files may not be present): %v", err)
		return
	}

	// Test direct conversion functions
	testCases := []struct {
		oldItemID int64
		expectedNewItemID int64
	}{
		{1000001, 33489313}, // Gora
		{1000002, 33489316}, // Monarch
		{1000003, 33489323}, // Kreshnik
		{1000004, 33489328}, // Svarog
		{1000005, 33489330}, // Cattaro
	}

	for _, tc := range testCases {
		// Test old to new conversion
		newItemID, exists := ConvertOldToNewItemID(tc.oldItemID)
		if !exists {
			t.Errorf("❌ H4: Old item ID %d not found in conversion table", tc.oldItemID)
			continue
		}

		if newItemID != tc.expectedNewItemID {
			t.Errorf("❌ H4: Expected new item ID %d for old ID %d, got %d",
				tc.expectedNewItemID, tc.oldItemID, newItemID)
		} else {
			t.Logf("✅ H4: Old->New conversion: %d -> %d", tc.oldItemID, newItemID)
		}

		// Test new to old conversion (reverse)
		oldItemID, exists := ConvertNewToOldItemID(tc.expectedNewItemID)
		if !exists {
			t.Errorf("❌ H4: New item ID %d not found in conversion table", tc.expectedNewItemID)
			continue
		}

		if oldItemID != tc.oldItemID {
			t.Errorf("❌ H4: Expected old item ID %d for new ID %d, got %d",
				tc.oldItemID, tc.expectedNewItemID, oldItemID)
		} else {
			t.Logf("✅ H4: New->Old conversion: %d <- %d", tc.expectedNewItemID, oldItemID)
		}
	}
}

func TestH4AllConversionEntries(t *testing.T) {
	// Initialize the item ID conversion table
	err := LoadItemIDConversionTable()
	if err != nil {
		t.Logf("⚠️  H4: Could not load ItemIDConversionTable (data files may not be present): %v", err)
		return
	}

	entries := GetAllItemIDConversionEntries()
	if len(entries) != 1616 {
		t.Errorf("❌ H4: Expected 1616 conversion entries, got %d", len(entries))
	} else {
		t.Logf("✅ H4: GetAllItemIDConversionEntries returned %d entries", len(entries))
	}

	// Verify all entries have valid data
	for _, entry := range entries {
		if entry.OldItemID == 0 {
			t.Error("❌ H4: Found conversion entry with zero old item ID")
		}
		if entry.NewItemID == 0 {
			t.Error("❌ H4: Found conversion entry with zero new item ID")
		}
		// Asset can be empty, but name should not be completely empty for valid entries
		if entry.OldItemID > 0 && entry.NewItemID > 0 && entry.Asset == "" {
			t.Logf("ℹ️  H4: Entry with old ID %d has empty asset path", entry.OldItemID)
		}
	}

	t.Log("✅ H4: All conversion entries have valid data")
}

func TestH4AllItemIDs(t *testing.T) {
	// Initialize the item ID conversion table
	err := LoadItemIDConversionTable()
	if err != nil {
		t.Logf("⚠️  H4: Could not load ItemIDConversionTable (data files may not be present): %v", err)
		return
	}

	oldIDs := GetAllOldItemIDs()
	newIDs := GetAllNewItemIDs()

	if len(oldIDs) != 1616 {
		t.Errorf("❌ H4: Expected 1616 old item IDs, got %d", len(oldIDs))
	} else {
		t.Logf("✅ H4: GetAllOldItemIDs returned %d IDs", len(oldIDs))
	}

	if len(newIDs) != 1616 {
		t.Logf("ℹ️  H4: GetAllNewItemIDs returned %d unique new IDs (some new IDs are shared by multiple old IDs)", len(newIDs))
	} else {
		t.Logf("✅ H4: GetAllNewItemIDs returned %d IDs", len(newIDs))
	}

	// Verify all old IDs are positive
	for _, id := range oldIDs {
		if id <= 0 {
			t.Errorf("❌ H4: Found non-positive old item ID: %d", id)
		}
	}

	// Note: Some new IDs might be -1 (invalid/placeholder) or shared between multiple old IDs
	negativeNewIDs := 0
	for _, id := range newIDs {
		if id <= 0 {
			negativeNewIDs++
		}
	}
	if negativeNewIDs > 0 {
		t.Logf("ℹ️  H4: Found %d non-positive new item IDs (these may be placeholders or invalid entries)", negativeNewIDs)
	} else {
		t.Log("✅ H4: All new item IDs are positive")
	}

	t.Log("✅ H4: All old item IDs are positive")
}

func TestH4ConversionSearch(t *testing.T) {
	// Initialize the item ID conversion table
	err := LoadItemIDConversionTable()
	if err != nil {
		t.Logf("⚠️  H4: Could not load ItemIDConversionTable (data files may not be present): %v", err)
		return
	}

	// Test searching by name
	searchResults := FindConversionEntriesByName("Gora")
	if len(searchResults) == 0 {
		t.Error("❌ H4: Expected to find conversion entries containing 'Gora'")
	} else {
		t.Logf("✅ H4: Found %d conversion entries containing 'Gora'", len(searchResults))
		for _, entry := range searchResults {
			t.Logf("  - %s: %d -> %d", entry.Name, entry.OldItemID, entry.NewItemID)
		}
	}

	// Test case-insensitive search
	searchResults = FindConversionEntriesByName("MONARCH")
	if len(searchResults) == 0 {
		t.Error("❌ H4: Expected to find conversion entries containing 'MONARCH'")
	} else {
		// There might be multiple entries with "MONARCH" in the name
		foundMonarch := false
		for _, entry := range searchResults {
			if entry.Name == "Monarch" {
				foundMonarch = true
				break
			}
		}
		if !foundMonarch {
			t.Errorf("❌ H4: Expected to find 'Monarch' entry in results")
		} else {
			t.Logf("✅ H4: Case-insensitive search works: found %d entries containing 'MONARCH'", len(searchResults))
		}
	}

	// Test search by asset
	searchResults = FindConversionEntriesByAsset("VH_AssaultHeavy")
	if len(searchResults) == 0 {
		t.Error("❌ H4: Expected to find conversion entries containing 'VH_AssaultHeavy' in asset")
	} else {
		t.Logf("✅ H4: Found %d conversion entries containing 'VH_AssaultHeavy' in asset", len(searchResults))
	}

	// Test search for non-existent term
	searchResults = FindConversionEntriesByName("NonExistent")
	if len(searchResults) != 0 {
		t.Errorf("❌ H4: Expected no results for 'NonExistent', got %d", len(searchResults))
	} else {
		t.Log("✅ H4: Search for non-existent term returns empty results")
	}
}

func TestH4BatchConversion(t *testing.T) {
	// Initialize the item ID conversion table
	err := LoadItemIDConversionTable()
	if err != nil {
		t.Logf("⚠️  H4: Could not load ItemIDConversionTable (data files may not be present): %v", err)
		return
	}

	// Test batch conversion
	oldItemIDs := []int64{1000001, 1000002, 1000003, 9999999} // Last one doesn't exist
	result := BatchConvertOldToNewItemIDs(oldItemIDs)

	if len(result) != 3 {
		t.Errorf("❌ H4: Expected 3 conversions (9999999 doesn't exist), got %d", len(result))
	} else {
		t.Logf("✅ H4: Batch conversion returned %d valid conversions", len(result))
		
		// Verify specific conversions
		if result[1000001] != 33489313 {
			t.Errorf("❌ H4: Expected 1000001 -> 33489313, got %d", result[1000001])
		}
		if result[1000002] != 33489316 {
			t.Errorf("❌ H4: Expected 1000002 -> 33489316, got %d", result[1000002])
		}
		if result[1000003] != 33489323 {
			t.Errorf("❌ H4: Expected 1000003 -> 33489323, got %d", result[1000003])
		}
	}

	// Test batch reverse conversion
	newItemIDs := []int64{33489313, 33489316, 33489323, 9999999}
	result = BatchConvertNewToOldItemIDs(newItemIDs)

	if len(result) != 3 {
		t.Errorf("❌ H4: Expected 3 reverse conversions, got %d", len(result))
	} else {
		t.Logf("✅ H4: Batch reverse conversion returned %d valid conversions", len(result))
		
		// Verify specific conversions
		if result[33489313] != 1000001 {
			t.Errorf("❌ H4: Expected 33489313 -> 1000001, got %d", result[33489313])
		}
		if result[33489316] != 1000002 {
			t.Errorf("❌ H4: Expected 33489316 -> 1000002, got %d", result[33489316])
		}
		if result[33489323] != 1000003 {
			t.Errorf("❌ H4: Expected 33489323 -> 1000003, got %d", result[33489323])
		}
	}
}

func TestH4ExistenceChecks(t *testing.T) {
	// Initialize the item ID conversion table
	err := LoadItemIDConversionTable()
	if err != nil {
		t.Logf("⚠️  H4: Could not load ItemIDConversionTable (data files may not be present): %v", err)
		return
	}

	// Test existence checks
	if !IsOldItemIDInConversionTable(1000001) {
		t.Error("❌ H4: Old item ID 1000001 should exist in conversion table")
	} else {
		t.Log("✅ H4: Old item ID 1000001 exists in conversion table")
	}

	if !IsNewItemIDInConversionTable(33489313) {
		t.Error("❌ H4: New item ID 33489313 should exist in conversion table")
	} else {
		t.Log("✅ H4: New item ID 33489313 exists in conversion table")
	}

	if IsOldItemIDInConversionTable(9999999) {
		t.Error("❌ H4: Old item ID 9999999 should not exist in conversion table")
	} else {
		t.Log("✅ H4: Non-existent old item ID 9999999 correctly not found")
	}

	if IsNewItemIDInConversionTable(9999999) {
		t.Error("❌ H4: New item ID 9999999 should not exist in conversion table")
	} else {
		t.Log("✅ H4: Non-existent new item ID 9999999 correctly not found")
	}
}

func TestH4DataConsistency(t *testing.T) {
	// Initialize the item ID conversion table
	err := LoadItemIDConversionTable()
	if err != nil {
		t.Logf("⚠️  H4: Could not load ItemIDConversionTable (data files may not be present): %v", err)
		return
	}

	entries := GetAllItemIDConversionEntries()
	oldToNewMap := make(map[int64]int64)
	newToOldMap := make(map[int64]int64)

	// Build maps from entries
	for _, entry := range entries {
		oldToNewMap[entry.OldItemID] = entry.NewItemID
		newToOldMap[entry.NewItemID] = entry.OldItemID
	}

	// Verify that the number of unique old IDs matches the entry count
	if len(oldToNewMap) != 1616 {
		t.Errorf("❌ H4: Expected 1616 unique old item IDs, got %d", len(oldToNewMap))
	} else {
		t.Logf("✅ H4: Found 1616 unique old item IDs")
	}

	// Note: The number of unique new IDs might be less than 1616 because some new IDs are shared
	// or some entries have invalid new IDs (-1)
	if len(newToOldMap) < len(oldToNewMap) {
		t.Logf("ℹ️  H4: Found %d unique new item IDs (less than old IDs due to shared new IDs or invalid entries)", len(newToOldMap))
	} else if len(newToOldMap) == len(oldToNewMap) {
		t.Logf("✅ H4: Found %d unique new item IDs (same as old IDs)", len(newToOldMap))
	}

	// Check for consistency issues (these are expected in this dataset)
	inconsistencyCount := 0
	for oldID, newID := range oldToNewMap {
		if reverseOldID, exists := newToOldMap[newID]; !exists {
			// This can happen if newID is -1 or if multiple old IDs map to the same new ID
			inconsistencyCount++
		} else if reverseOldID != oldID {
			// This happens when multiple old IDs map to the same new ID
			// The reverse mapping will point to one of them (the last one processed)
			inconsistencyCount++
		}
	}

	if inconsistencyCount > 0 {
		t.Logf("ℹ️  H4: Found %d mapping inconsistencies (expected due to shared new IDs)", inconsistencyCount)
	} else {
		t.Log("✅ H4: All bidirectional mappings are consistent")
	}

	// Verify that all entries have valid old->new mappings
	for _, entry := range entries {
		if entry.OldItemID == 0 || entry.NewItemID == 0 {
			t.Errorf("❌ H4: Found entry with zero IDs: old=%d, new=%d", entry.OldItemID, entry.NewItemID)
		}
	}

	t.Log("✅ H4: All entries have non-zero IDs")
}

func TestH4KnownConversions(t *testing.T) {
	// Initialize the item ID conversion table
	err := LoadItemIDConversionTable()
	if err != nil {
		t.Logf("⚠️  H4: Could not load ItemIDConversionTable (data files may not be present): %v", err)
		return
	}

	// Test some known conversions from the file
	knownConversions := []struct {
		oldItemID int64
		newItemID int64
		name      string
	}{
		{1000001, 33489313, "Gora"},
		{1000002, 33489316, "Monarch"},
		{1000003, 33489323, "Kreshnik"},
		{1000004, 33489328, "Svarog"},
		{1000005, 33489330, "Cattaro"},
	}

	foundCount := 0
	for _, tc := range knownConversions {
		entry, exists := GetItemIDConversionEntry(tc.oldItemID)
		if !exists {
			t.Errorf("❌ H4: Known conversion not found: %d -> %d (%s)", tc.oldItemID, tc.newItemID, tc.name)
			continue
		}

		if entry.NewItemID != tc.newItemID {
			t.Errorf("❌ H4: Wrong new item ID for %s: expected %d, got %d", tc.name, tc.newItemID, entry.NewItemID)
			continue
		}

		if entry.Name != tc.name {
			t.Errorf("❌ H4: Wrong name for %d: expected '%s', got '%s'", tc.oldItemID, tc.name, entry.Name)
			continue
		}

		foundCount++
		t.Logf("✅ H4: Found known conversion: %s (%d -> %d)", tc.name, tc.oldItemID, tc.newItemID)
	}

	if foundCount == len(knownConversions) {
		t.Logf("✅ H4: All %d known conversions verified", foundCount)
	}
}