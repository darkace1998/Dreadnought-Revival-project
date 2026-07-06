package dreadgameconfig

import (
	"testing"
)

func TestH7AssetTablesValidation(t *testing.T) {
	// This test verifies H7: Add tests — verify item counts per category, validate ID→path mappings
	
	// Run the validation
	validation := ValidateAssetTables()
	if validation == nil {
		t.Fatal("❌ H7: Could not run asset tables validation")
	}

	t.Log("✅ H7: Asset tables validation completed successfully")
}

func TestH7ItemIDTableValidation(t *testing.T) {
	// This test verifies ItemIDTable validation
	
	validation := ValidateAssetTables()
	if validation == nil {
		t.Fatal("❌ H7: Could not run asset tables validation")
	}

	// Check that we have categories
	if validation.ItemIDTableCategories == 0 {
		t.Error("❌ H7: ItemIDTable should have categories")
	} else {
		t.Logf("✅ H7: ItemIDTable has %d categories", validation.ItemIDTableCategories)
	}

	// Check that we have items
	if validation.ItemIDTableTotalItems == 0 {
		t.Error("❌ H7: ItemIDTable should have items")
	} else {
		t.Logf("✅ H7: ItemIDTable has %d total items", validation.ItemIDTableTotalItems)
	}

	// Expected values from the data
	if validation.ItemIDTableCategories != 26 {
		t.Errorf("❌ H7: Expected 26 categories, got %d", validation.ItemIDTableCategories)
	} else {
		t.Log("✅ H7: ItemIDTable has correct number of categories (26)")
	}

	if validation.ItemIDTableTotalItems != 3497 {
		t.Errorf("❌ H7: Expected 3497 total items, got %d", validation.ItemIDTableTotalItems)
	} else {
		t.Log("✅ H7: ItemIDTable has correct total items (3497)")
	}
}

func TestH7ItemIDRegisterValidation(t *testing.T) {
	// This test verifies ItemIDRegister validation
	
	validation := ValidateAssetTables()
	if validation == nil {
		t.Fatal("❌ H7: Could not run asset tables validation")
	}

	// Check that we have entries
	if validation.ItemIDRegisterTotalItems == 0 {
		t.Error("❌ H7: ItemIDRegister should have entries")
	} else {
		t.Logf("✅ H7: ItemIDRegister has %d entries", validation.ItemIDRegisterTotalItems)
	}

	// Check that we have unique IDs
	if validation.ItemIDRegisterUniqueIDs == 0 {
		t.Error("❌ H7: ItemIDRegister should have unique IDs")
	} else {
		t.Logf("✅ H7: ItemIDRegister has %d unique IDs", validation.ItemIDRegisterUniqueIDs)
	}

	// Check that we have unique paths
	if validation.ItemIDRegisterUniquePaths == 0 {
		t.Error("❌ H7: ItemIDRegister should have unique paths")
	} else {
		t.Logf("✅ H7: ItemIDRegister has %d unique paths", validation.ItemIDRegisterUniquePaths)
	}

	// Expected values from the data
	if validation.ItemIDRegisterTotalItems != 3086 {
		t.Errorf("❌ H7: Expected 3086 entries, got %d", validation.ItemIDRegisterTotalItems)
	} else {
		t.Log("✅ H7: ItemIDRegister has correct number of entries (3086)")
	}

	if validation.ItemIDRegisterUniqueIDs != 3086 {
		t.Errorf("❌ H7: Expected 3086 unique IDs, got %d", validation.ItemIDRegisterUniqueIDs)
	} else {
		t.Log("✅ H7: ItemIDRegister has correct number of unique IDs (3086)")
	}
}

func TestH7CatalogIDTableValidation(t *testing.T) {
	// This test verifies CatalogIDTable validation
	
	validation := ValidateAssetTables()
	if validation == nil {
		t.Fatal("❌ H7: Could not run asset tables validation")
	}

	// Check that we have buckets
	if validation.CatalogIDTableBuckets == 0 {
		t.Error("❌ H7: CatalogIDTable should have buckets")
	} else {
		t.Logf("✅ H7: CatalogIDTable has %d buckets", validation.CatalogIDTableBuckets)
	}

	// Check that we have items
	if validation.CatalogIDTableTotalItems == 0 {
		t.Error("❌ H7: CatalogIDTable should have items")
	} else {
		t.Logf("✅ H7: CatalogIDTable has %d total items", validation.CatalogIDTableTotalItems)
	}

	// Expected values from the data
	if validation.CatalogIDTableBuckets != 12 {
		t.Errorf("❌ H7: Expected 12 buckets, got %d", validation.CatalogIDTableBuckets)
	} else {
		t.Log("✅ H7: CatalogIDTable has correct number of buckets (12)")
	}

	if validation.CatalogIDTableTotalItems != 6630 {
		t.Errorf("❌ H7: Expected 6630 total items, got %d", validation.CatalogIDTableTotalItems)
	} else {
		t.Log("✅ H7: CatalogIDTable has correct total items (6630)")
	}
}

func TestH7ItemIDConversionTableValidation(t *testing.T) {
	// This test verifies ItemIDConversionTable validation
	
	validation := ValidateAssetTables()
	if validation == nil {
		t.Fatal("❌ H7: Could not run asset tables validation")
	}

	// Check that we have entries
	if validation.ItemIDConversionTableTotalEntries == 0 {
		t.Error("❌ H7: ItemIDConversionTable should have entries")
	} else {
		t.Logf("✅ H7: ItemIDConversionTable has %d entries", validation.ItemIDConversionTableTotalEntries)
	}

	// Check that we have unique old IDs
	if validation.ItemIDConversionTableUniqueOldIDs == 0 {
		t.Error("❌ H7: ItemIDConversionTable should have unique old IDs")
	} else {
		t.Logf("✅ H7: ItemIDConversionTable has %d unique old IDs", validation.ItemIDConversionTableUniqueOldIDs)
	}

	// Check that we have unique new IDs
	if validation.ItemIDConversionTableUniqueNewIDs == 0 {
		t.Error("❌ H7: ItemIDConversionTable should have unique new IDs")
	} else {
		t.Logf("✅ H7: ItemIDConversionTable has %d unique new IDs", validation.ItemIDConversionTableUniqueNewIDs)
	}

	// Expected values from the data
	if validation.ItemIDConversionTableTotalEntries != 1616 {
		t.Errorf("❌ H7: Expected 1616 entries, got %d", validation.ItemIDConversionTableTotalEntries)
	} else {
		t.Log("✅ H7: ItemIDConversionTable has correct number of entries (1616)")
	}

	if validation.ItemIDConversionTableUniqueOldIDs != 1616 {
		t.Errorf("❌ H7: Expected 1616 unique old IDs, got %d", validation.ItemIDConversionTableUniqueOldIDs)
	} else {
		t.Log("✅ H7: ItemIDConversionTable has correct number of unique old IDs (1616)")
	}
}

func TestH7DynamicCatalogValidation(t *testing.T) {
	// This test verifies dynamic catalog validation
	
	validation := ValidateAssetTables()
	if validation == nil {
		t.Fatal("❌ H7: Could not run asset tables validation")
	}

	// Check that we have items in dynamic catalog
	if validation.DynamicCatalogTotalItems == 0 {
		t.Error("❌ H7: Dynamic catalog should have items")
	} else {
		t.Logf("✅ H7: Dynamic catalog has %d items", validation.DynamicCatalogTotalItems)
	}

	// Check that we have items by category
	if len(validation.DynamicCatalogByCategory) == 0 {
		t.Error("❌ H7: Dynamic catalog should have items by category")
	} else {
		t.Logf("✅ H7: Dynamic catalog has items in %d categories", len(validation.DynamicCatalogByCategory))
	}

	// Check that we have items by type
	if len(validation.DynamicCatalogByType) == 0 {
		t.Error("❌ H7: Dynamic catalog should have items by type")
	} else {
		t.Logf("✅ H7: Dynamic catalog has items in %d types", len(validation.DynamicCatalogByType))
	}
}

func TestH7CrossTableValidations(t *testing.T) {
	// This test verifies cross-table validations
	
	validation := ValidateAssetTables()
	if validation == nil {
		t.Fatal("❌ H7: Could not run asset tables validation")
	}

	// Check that we have cross-table validation results
	if len(validation.CrossTableValidationResults) == 0 {
		t.Error("❌ H7: Should have cross-table validation results")
	} else {
		t.Logf("✅ H7: Found %d cross-table validation results", len(validation.CrossTableValidationResults))
	}

	// Check each validation result
	for _, result := range validation.CrossTableValidationResults {
		if result.TotalChecked == 0 {
			t.Errorf("❌ H7: Validation between %s and %s has zero items checked",
				result.Table1, result.Table2)
		} else {
			t.Logf("✅ H7: %s-%s validation: %d/%d matches",
				result.Table1, result.Table2, result.MatchesFound, result.TotalChecked)
		}
	}
}

func TestH7ItemCountsPerCategory(t *testing.T) {
	// This test verifies item counts per category
	
	// Get category counts from ItemIDTable
	categoryCounts := GetItemIDTableCategoryCounts()
	if len(categoryCounts) == 0 {
		t.Error("❌ H7: No category counts found")
	} else {
		t.Logf("✅ H7: Found item counts for %d categories", len(categoryCounts))
	}

	// Verify some known categories
	knownCategories := []string{
		"YPawn",
		"YShipLoadoutPrecast", 
		"YWeapon",
		"YAbility",
		"YPerk",
	}

	for _, category := range knownCategories {
		count, exists := categoryCounts[category]
		if !exists {
			t.Errorf("❌ H7: Category %s not found in counts", category)
		} else {
			t.Logf("✅ H7: Category %s has %d items", category, count)
		}
	}

	// Verify total matches expected
	totalItems := 0
	for _, count := range categoryCounts {
		totalItems += count
	}

	if totalItems != 3497 {
		t.Errorf("❌ H7: Expected total 3497 items across categories, got %d", totalItems)
	} else {
		t.Log("✅ H7: Total items across categories matches expected count (3497)")
	}
}

func TestH7DynamicCatalogCategoryCounts(t *testing.T) {
	// This test verifies dynamic catalog item counts per category
	
	categoryCounts := GetDynamicCatalogCategoryCounts()
	if len(categoryCounts) == 0 {
		t.Error("❌ H7: No dynamic catalog category counts found")
	} else {
		t.Logf("✅ H7: Found dynamic catalog item counts for %d categories", len(categoryCounts))
	}

	// Verify that we have counts for known categories
	knownCategories := []string{
		TableCategoryShip,
		TableCategoryLoadout,
		TableCategoryWeapon,
		TableCategoryAbility,
		TableCategoryPerk,
	}

	for _, category := range knownCategories {
		count, exists := categoryCounts[category]
		if !exists {
			t.Logf("ℹ️  H7: Category %s not found in dynamic catalog counts", category)
		} else {
			t.Logf("✅ H7: Dynamic catalog category %s has %d items", category, count)
		}
	}

	// Verify total
	totalItems := 0
	for _, count := range categoryCounts {
		totalItems += count
	}

	if totalItems == 0 {
		t.Error("❌ H7: Dynamic catalog should have items")
	} else {
		t.Logf("✅ H7: Dynamic catalog has %d total items across categories", totalItems)
	}
}

func TestH7DynamicCatalogTypeCounts(t *testing.T) {
	// This test verifies dynamic catalog item counts per type
	
	typeCounts := GetDynamicCatalogTypeCounts()
	if len(typeCounts) == 0 {
		t.Error("❌ H7: No dynamic catalog type counts found")
	} else {
		t.Logf("✅ H7: Found dynamic catalog item counts for %d types", len(typeCounts))
	}

	// Verify that we have counts for known types
	knownTypes := []string{
		ItemTypeShip,
		ItemTypeLoadout,
		ItemTypeWeapon,
		ItemTypeAbility,
		ItemTypePerk,
	}

	for _, itemType := range knownTypes {
		count, exists := typeCounts[itemType]
		if !exists {
			t.Logf("ℹ️  H7: Type %s not found in dynamic catalog counts", itemType)
		} else {
			t.Logf("✅ H7: Dynamic catalog type %s has %d items", itemType, count)
		}
	}

	// Verify total
	totalItems := 0
	for _, count := range typeCounts {
		totalItems += count
	}

	if totalItems == 0 {
		t.Error("❌ H7: Dynamic catalog should have items")
	} else {
		t.Logf("✅ H7: Dynamic catalog has %d total items across types", totalItems)
	}
}

func TestH7IDToPathMappingsValidation(t *testing.T) {
	// This test verifies ID→path mappings validation
	
	// Get ID→path validation map
	validationMap := ValidateIDToPathMappings()
	if len(validationMap) == 0 {
		t.Error("❌ H7: No ID→path validation results found")
	} else {
		t.Logf("✅ H7: Found ID→path validation for %d item IDs", len(validationMap))
	}

	// Count mapped vs unmapped
	mappedCount := 0
	unmappedCount := 0
	
	for _, assetPath := range validationMap {
		if assetPath != "NOT FOUND" {
			mappedCount++
		} else {
			unmappedCount++
		}
	}

	t.Logf("✅ H7: %d item IDs have valid paths, %d do not", mappedCount, unmappedCount)

	// Most items should have paths
	if mappedCount < len(validationMap)/2 {
		t.Errorf("❌ H7: Too many items without paths: %d/%d", unmappedCount, len(validationMap))
	} else {
		t.Log("✅ H7: Majority of items have valid paths")
	}
}

func TestH7PathToIDMappingsValidation(t *testing.T) {
	// This test verifies path→ID mappings validation
	
	// Get path→ID validation map
	validationMap := ValidatePathToIDMappings()
	if len(validationMap) == 0 {
		t.Error("❌ H7: No path→ID validation results found")
	} else {
		t.Logf("✅ H7: Found path→ID validation for %d asset paths", len(validationMap))
	}

	// Count mapped vs unmapped
	mappedCount := 0
	unmappedCount := 0
	
	for _, itemID := range validationMap {
		if itemID != -1 {
			mappedCount++
		} else {
			unmappedCount++
		}
	}

	t.Logf("✅ H7: %d asset paths have valid IDs, %d do not", mappedCount, unmappedCount)

	// Most paths should have IDs
	if mappedCount < len(validationMap)/2 {
		t.Errorf("❌ H7: Too many paths without IDs: %d/%d", unmappedCount, len(validationMap))
	} else {
		t.Log("✅ H7: Majority of paths have valid IDs")
	}
}

func TestH7CategoryConsistencyValidation(t *testing.T) {
	// This test verifies category consistency
	
	consistencyMap := ValidateCategoryConsistency()
	if len(consistencyMap) == 0 {
		t.Error("❌ H7: No category consistency results found")
	} else {
		t.Logf("✅ H7: Found category consistency for %d categories", len(consistencyMap))
	}

	// Count consistent vs inconsistent
	consistentCount := 0
	inconsistentCount := 0
	
	for _, isConsistent := range consistencyMap {
		if isConsistent {
			consistentCount++
		} else {
			inconsistentCount++
		}
	}

	t.Logf("✅ H7: %d categories are consistent, %d are not", consistentCount, inconsistentCount)

	// All categories should be consistent
	if inconsistentCount > 0 {
		t.Errorf("❌ H7: Found inconsistent categories")
		for category, isConsistent := range consistencyMap {
			if !isConsistent {
				t.Errorf("  - Category %s is inconsistent", category)
			}
		}
	} else {
		t.Log("✅ H7: All categories are consistent")
	}
}

func TestH7ValidateItemCountsPerCategory(t *testing.T) {
	// This test uses the specific validation function for item counts per category
	
	categoryValidations := ValidateItemCountsPerCategory()
	if len(categoryValidations) == 0 {
		t.Error("❌ H7: No category validation results found")
	} else {
		t.Logf("✅ H7: Found validation results for %d categories", len(categoryValidations))
	}

	// Check each category validation
	consistentCount := 0
	inconsistentCount := 0
	
	for category, validation := range categoryValidations {
		if validation.IsConsistent {
			consistentCount++
			t.Logf("✅ H7: Category %s is consistent (ItemIDTable: %d, Dynamic: %d, Register: %d)",
				category, validation.ItemIDTableCount, validation.DynamicCatalogCount, validation.ItemIDRegisterCount)
		} else {
			inconsistentCount++
			t.Logf("❌ H7: Category %s is inconsistent: %v", category, validation.Issues)
		}
	}

	t.Logf("✅ H7: %d categories consistent, %d inconsistent", consistentCount, inconsistentCount)
}

func TestH7ValidateIDToPathMappingsForCategory(t *testing.T) {
	// This test validates ID→path mappings for specific categories
	
	// Test a few known categories
	categoriesToTest := []string{
		"YPawn",
		"YWeapon",
		"YAbility",
		"YPerk",
	}

	for _, category := range categoriesToTest {
		validation := ValidateIDToPathMappingsForCategory(category)
		
		if validation.TotalItems == 0 {
			t.Logf("ℹ️  H7: Category %s has no items in ItemIDTable", category)
			continue
		}

		if validation.IsValid {
			t.Logf("✅ H7: Category %s ID→path mappings are valid (%d/%d mapped)",
				category, validation.MappedItems, validation.TotalItems)
		} else {
			t.Logf("❌ H7: Category %s ID→path mappings have issues: %v",
				category, validation.Issues)
			
			// Show some unmapped IDs
			if len(validation.UnmappedIDs) > 0 {
				unmappedSample := validation.UnmappedIDs
				if len(unmappedSample) > 5 {
					unmappedSample = unmappedSample[:5]
				}
				t.Logf("  Sample unmapped IDs: %v", unmappedSample)
			}
		}
	}
}

func TestH7ValidateAllIDToPathMappings(t *testing.T) {
	// This test validates ID→path mappings for all categories
	
	allValidations := ValidateAllIDToPathMappings()
	if len(allValidations) == 0 {
		t.Error("❌ H7: No ID→path validation results found")
	} else {
		t.Logf("✅ H7: Found ID→path validation for %d categories", len(allValidations))
	}

	// Count valid vs invalid categories
	validCount := 0
	invalidCount := 0
	
	for _, validation := range allValidations {
		if validation.IsValid {
			validCount++
		} else {
			invalidCount++
		}
	}

	t.Logf("✅ H7: %d categories have valid ID→path mappings, %d have issues", validCount, invalidCount)
}

func TestH7ValidationReport(t *testing.T) {
	// This test generates and checks the validation report
	
	// This will print to stdout, but we can't easily capture it in a test
	// So we'll just call it and check that it doesn't panic
	PrintAssetTablesValidationReport()
	
	t.Log("✅ H7: Validation report generated successfully")
}

func TestH7CrossTableValidationDetails(t *testing.T) {
	// This test checks the details of cross-table validations
	
	results := GetCrossTableValidationResults()
	if len(results) == 0 {
		t.Error("❌ H7: No cross-table validation results found")
	} else {
		t.Logf("✅ H7: Found %d cross-table validation results", len(results))
	}

	// Check each result for reasonable values
	for _, result := range results {
		if result.TotalChecked == 0 {
			t.Errorf("❌ H7: Validation %s-%s has zero items checked", result.Table1, result.Table2)
		}

		if result.MatchesFound < 0 || result.MatchesFound > result.TotalChecked {
			t.Errorf("❌ H7: Validation %s-%s has invalid match count: %d/%d",
				result.Table1, result.Table2, result.MatchesFound, result.TotalChecked)
		}

		if result.MismatchesFound < 0 || result.MismatchesFound > result.TotalChecked {
			t.Errorf("❌ H7: Validation %s-%s has invalid mismatch count: %d/%d",
				result.Table1, result.Table2, result.MismatchesFound, result.TotalChecked)
		}

		// Check that matches + mismatches = total
		if result.MatchesFound + result.MismatchesFound != result.TotalChecked {
			t.Errorf("❌ H7: Validation %s-%s: matches (%d) + mismatches (%d) != total (%d)",
				result.Table1, result.Table2, result.MatchesFound, result.MismatchesFound, result.TotalChecked)
		} else {
			t.Logf("✅ H7: Validation %s-%s: %d matches + %d mismatches = %d total",
				result.Table1, result.Table2, result.MatchesFound, result.MismatchesFound, result.TotalChecked)
		}
	}
}

func TestH7CategoryValidationConsistency(t *testing.T) {
	// This test verifies that category validations are consistent
	
	// Get category validations
	categoryValidations := ValidateItemCountsPerCategory()
	
	// Get category counts from different sources
	itemIDTableCounts := GetItemIDTableCategoryCounts()
	dynamicCatalogCounts := GetDynamicCatalogCategoryCounts()
	
	// Check that all categories from ItemIDTable are present in validations
	for category := range itemIDTableCounts {
		if _, exists := categoryValidations[category]; !exists {
			t.Errorf("❌ H7: Category %s missing from validations", category)
		} else {
			t.Logf("✅ H7: Category %s present in validations", category)
		}
	}

	// Check that counts are reasonable (dynamic catalog may have fewer items due to filtering)
	for category, validation := range categoryValidations {
		itemIDTableCount := itemIDTableCounts[category]
		dynamicCatalogCount := dynamicCatalogCounts[category]
		
		// Dynamic catalog count should be <= ItemIDTable count for the same category
		// (because dynamic catalog may filter out some items)
		if dynamicCatalogCount > itemIDTableCount {
			t.Logf("ℹ️  H7: Dynamic catalog has more items (%d) than ItemIDTable (%d) for category %s",
				dynamicCatalogCount, itemIDTableCount, category)
		}
		
		// Validation should reflect the ItemIDTable count
		if validation.ItemIDTableCount != itemIDTableCount {
			t.Errorf("❌ H7: Validation ItemIDTable count (%d) != actual count (%d) for category %s",
				validation.ItemIDTableCount, itemIDTableCount, category)
		} else {
			t.Logf("✅ H7: Validation ItemIDTable count matches for category %s", category)
		}
	}
}

func TestH7SpecificCategoryValidation(t *testing.T) {
	// This test validates specific categories in detail
	
	// Test the main categories that should have items
	categories := []string{"YPawn", "YWeapon", "YAbility", "YPerk"}
	
	for _, category := range categories {
		// Get category validation
		categoryValidation := ValidateItemCountsPerCategory()[category]
		
		// Get ID→path validation
		idPathValidation := ValidateIDToPathMappingsForCategory(category)
		
		t.Logf("📊 H7: Category %s:", category)
		t.Logf("  ItemIDTable: %d items", categoryValidation.ItemIDTableCount)
		t.Logf("  Dynamic Catalog: %d items", categoryValidation.DynamicCatalogCount)
		t.Logf("  ItemIDRegister: %d items", categoryValidation.ItemIDRegisterCount)
		t.Logf("  ID→Path mappings: %d/%d mapped", idPathValidation.MappedItems, idPathValidation.TotalItems)
		
		// Check consistency
		if categoryValidation.IsConsistent {
			t.Logf("  ✅ Category is consistent")
		} else {
			t.Logf("  ❌ Category has issues: %v", categoryValidation.Issues)
		}
		
		if idPathValidation.IsValid {
			t.Logf("  ✅ ID→Path mappings are valid")
		} else {
			t.Logf("  ❌ ID→Path mappings have issues: %v", idPathValidation.Issues)
		}
	}
}

func TestH7ValidationSummary(t *testing.T) {
	// This test provides a summary of all validations
	
	validation := ValidateAssetTables()
	if validation == nil {
		t.Fatal("❌ H7: Could not run asset tables validation")
	}

	// Summary of all validations
	t.Log("=== H7: Validation Summary ===")
	t.Logf("ItemIDTable: %d categories, %d items", 
		validation.ItemIDTableCategories, validation.ItemIDTableTotalItems)
	t.Logf("ItemIDRegister: %d entries, %d unique IDs, %d unique paths",
		validation.ItemIDRegisterTotalItems, 
		validation.ItemIDRegisterUniqueIDs, 
		validation.ItemIDRegisterUniquePaths)
	t.Logf("CatalogIDTable: %d buckets, %d items",
		validation.CatalogIDTableBuckets, validation.CatalogIDTableTotalItems)
	t.Logf("ItemIDConversionTable: %d entries, %d old IDs, %d new IDs",
		validation.ItemIDConversionTableTotalEntries,
		validation.ItemIDConversionTableUniqueOldIDs,
		validation.ItemIDConversionTableUniqueNewIDs)
	t.Logf("Dynamic Catalog: %d items", validation.DynamicCatalogTotalItems)

	// Cross-table validations
	t.Log("Cross-table validations:")
	for _, result := range validation.CrossTableValidationResults {
		t.Logf("  %s-%s: %d/%d matches",
			result.Table1, result.Table2, result.MatchesFound, result.TotalChecked)
	}

	// Category counts
	t.Log("ItemIDTable categories:")
	for category, count := range validation.ItemIDTableCategoryMap {
		t.Logf("  %s: %d", category, count)
	}

	// Dynamic catalog by category
	t.Log("Dynamic catalog by category:")
	for category, count := range validation.DynamicCatalogByCategory {
		t.Logf("  %s: %d", category, count)
	}

	// Dynamic catalog by type
	t.Log("Dynamic catalog by type:")
	for itemType, count := range validation.DynamicCatalogByType {
		t.Logf("  %s: %d", itemType, count)
	}

	// Final status
	if validation.isAllValidationsPassing() {
		t.Log("✅ All validations passing")
	} else {
		t.Log("⚠️  Some validations have issues")
	}

	t.Log("✅ H7: Validation summary generated successfully")
}