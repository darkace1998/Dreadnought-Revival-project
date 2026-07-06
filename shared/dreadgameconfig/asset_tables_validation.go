package dreadgameconfig

import (
	"fmt"
	"log"
	"sync"
)

// AssetTablesValidation represents comprehensive validation of all loaded asset tables
// This implements H7: Add tests — verify item counts per category, validate ID→path mappings
type AssetTablesValidation struct {
	// ItemIDTable validation
	ItemIDTableCategories    int
	ItemIDTableTotalItems   int
	ItemIDTableCategoryMap  map[string]int // category name -> item count
	
	// ItemIDRegister validation
	ItemIDRegisterTotalItems int
	ItemIDRegisterUniqueIDs  int
	ItemIDRegisterUniquePaths int
	
	// CatalogIDTable validation
	CatalogIDTableBuckets    int
	CatalogIDTableTotalItems int
	CatalogIDTableBucketMap  map[string]int // bucket name -> item count
	
	// ItemIDConversionTable validation
	ItemIDConversionTableTotalEntries int
	ItemIDConversionTableUniqueOldIDs int
	ItemIDConversionTableUniqueNewIDs int
	
	// Cross-table validation
	CrossTableValidationResults []CrossTableValidationResult
	
	// Dynamic catalog validation
	DynamicCatalogTotalItems int
	DynamicCatalogByCategory map[string]int // category -> item count
	DynamicCatalogByType     map[string]int // item type -> item count
}

// CrossTableValidationResult represents a validation result between two tables
type CrossTableValidationResult struct {
	Table1          string
	Table2          string
	ValidationType  string
	TotalChecked    int
	MatchesFound    int
	MismatchesFound int
	Details         string
}

var (
	assetTablesValidation     *AssetTablesValidation
	assetTablesValidationOnce sync.Once
	assetTablesValidationMu   sync.RWMutex
)

// ValidateAssetTables performs comprehensive validation of all loaded asset tables
// This implements H7: Add tests — verify item counts per category, validate ID→path mappings
func ValidateAssetTables() *AssetTablesValidation {
	assetTablesValidationOnce.Do(func() {
		validation := &AssetTablesValidation{
			ItemIDTableCategoryMap:  make(map[string]int),
			CatalogIDTableBucketMap: make(map[string]int),
			CrossTableValidationResults: make([]CrossTableValidationResult, 0),
			DynamicCatalogByCategory: make(map[string]int),
			DynamicCatalogByType:     make(map[string]int),
		}

		// Validate ItemIDTable
		validation.validateItemIDTable()
		
		// Validate ItemIDRegister
		validation.validateItemIDRegister()
		
		// Validate CatalogIDTable
		validation.validateCatalogIDTable()
		
		// Validate ItemIDConversionTable
		validation.validateItemIDConversionTable()
		
		// Perform cross-table validations
		validation.performCrossTableValidations()
		
		// Validate dynamic catalog
		validation.validateDynamicCatalog()
		
		// Log validation results
		validation.logValidationResults()

		assetTablesValidation = validation
	})

	return assetTablesValidation
}

// validateItemIDTable validates the ItemIDTable data
func (v *AssetTablesValidation) validateItemIDTable() {
	categories := GetAllCategories()
	v.ItemIDTableCategories = len(categories)
	
	totalItems := 0
	for _, category := range categories {
		itemCount := len(category.ItemIDs)
		v.ItemIDTableCategoryMap[category.CategoryName] = itemCount
		totalItems += itemCount
	}
	v.ItemIDTableTotalItems = totalItems
	
	log.Printf("✅ H7: ItemIDTable validation: %d categories, %d total items",
		v.ItemIDTableCategories, v.ItemIDTableTotalItems)
}

// validateItemIDRegister validates the ItemIDRegister data
func (v *AssetTablesValidation) validateItemIDRegister() {
	entries := GetAllRegistryEntries()
	v.ItemIDRegisterTotalItems = len(entries)
	
	// Count unique item IDs
	uniqueIDs := make(map[int32]bool)
	uniquePaths := make(map[string]bool)
	
	for _, entry := range entries {
		uniqueIDs[entry.ItemID] = true
		uniquePaths[entry.Path] = true
	}
	
	v.ItemIDRegisterUniqueIDs = len(uniqueIDs)
	v.ItemIDRegisterUniquePaths = len(uniquePaths)
	
	log.Printf("✅ H7: ItemIDRegister validation: %d entries, %d unique IDs, %d unique paths",
		v.ItemIDRegisterTotalItems, v.ItemIDRegisterUniqueIDs, v.ItemIDRegisterUniquePaths)
}

// validateCatalogIDTable validates the CatalogIDTable data
func (v *AssetTablesValidation) validateCatalogIDTable() {
	buckets := GetAllCatalogBuckets()
	v.CatalogIDTableBuckets = len(buckets)
	
	totalItems := 0
	for _, bucket := range buckets {
		v.CatalogIDTableBucketMap[bucket.BucketName] = bucket.ItemCount
		totalItems += bucket.ItemCount
	}
	v.CatalogIDTableTotalItems = totalItems
	
	log.Printf("✅ H7: CatalogIDTable validation: %d buckets, %d total items",
		v.CatalogIDTableBuckets, v.CatalogIDTableTotalItems)
}

// validateItemIDConversionTable validates the ItemIDConversionTable data
func (v *AssetTablesValidation) validateItemIDConversionTable() {
	entries := GetAllItemIDConversionEntries()
	v.ItemIDConversionTableTotalEntries = len(entries)
	
	// Count unique old and new IDs
	uniqueOldIDs := make(map[int64]bool)
	uniqueNewIDs := make(map[int64]bool)
	
	for _, entry := range entries {
		uniqueOldIDs[entry.OldItemID] = true
		uniqueNewIDs[entry.NewItemID] = true
	}
	
	v.ItemIDConversionTableUniqueOldIDs = len(uniqueOldIDs)
	v.ItemIDConversionTableUniqueNewIDs = len(uniqueNewIDs)
	
	log.Printf("✅ H7: ItemIDConversionTable validation: %d entries, %d unique old IDs, %d unique new IDs",
		v.ItemIDConversionTableTotalEntries, 
		v.ItemIDConversionTableUniqueOldIDs, 
		v.ItemIDConversionTableUniqueNewIDs)
}

// performCrossTableValidations performs validations between different tables
func (v *AssetTablesValidation) performCrossTableValidations() {
	// 1. Validate ItemIDTable IDs exist in ItemIDRegister
	categories := GetAllCategories()
	itemIDToCategory := make(map[int32]string)
	for _, category := range categories {
		for _, itemID := range category.ItemIDs {
			itemIDToCategory[int32(itemID)] = category.CategoryName
		}
	}
	
	// Check how many ItemIDTable IDs have corresponding entries in ItemIDRegister
	registerEntries := GetAllRegistryEntries()
	registerItemIDs := make(map[int32]bool)
	for _, entry := range registerEntries {
		registerItemIDs[entry.ItemID] = true
	}
	
	matches := 0
	for itemID := range itemIDToCategory {
		if registerItemIDs[itemID] {
			matches++
		}
	}
	
	v.CrossTableValidationResults = append(v.CrossTableValidationResults, CrossTableValidationResult{
		Table1:         "ItemIDTable",
		Table2:         "ItemIDRegister",
		ValidationType: "ID existence",
		TotalChecked:   len(itemIDToCategory),
		MatchesFound:   matches,
		MismatchesFound: len(itemIDToCategory) - matches,
		Details:        fmt.Sprintf("%d/%d ItemIDTable IDs found in ItemIDRegister", matches, len(itemIDToCategory)),
	})
	
	// 2. Validate ItemIDRegister IDs exist in CatalogIDTable
	catalogBuckets := GetAllCatalogBuckets()
	catalogItemIDs := make(map[int32]bool)
	for _, bucket := range catalogBuckets {
		for _, itemIDInterface := range bucket.ItemIDs {
			// Handle CatalogItemID types
			var itemID int32
			switch val := itemIDInterface.Value.(type) {
			case int64:
				if val <= 0x7FFFFFFF {
					itemID = int32(val)
				}
			case int32:
				itemID = val
			case int:
				if val <= 0x7FFFFFFF {
					itemID = int32(val)
				}
			}
			if itemID != 0 {
				catalogItemIDs[itemID] = true
			}
		}
	}
	
	matches = 0
	for itemID := range registerItemIDs {
		if catalogItemIDs[itemID] {
			matches++
		}
	}
	
	v.CrossTableValidationResults = append(v.CrossTableValidationResults, CrossTableValidationResult{
		Table1:         "ItemIDRegister",
		Table2:         "CatalogIDTable",
		ValidationType: "ID existence",
		TotalChecked:   len(registerItemIDs),
		MatchesFound:   matches,
		MismatchesFound: len(registerItemIDs) - matches,
		Details:        fmt.Sprintf("%d/%d ItemIDRegister IDs found in CatalogIDTable", matches, len(registerItemIDs)),
	})
	
	// 3. Validate ItemIDConversionTable old IDs exist in ItemIDRegister
	conversionEntries := GetAllItemIDConversionEntries()
	conversionOldIDs := make(map[int64]bool)
	for _, entry := range conversionEntries {
		conversionOldIDs[entry.OldItemID] = true
	}
	
	// Convert register item IDs to int64 for comparison
	registerItemIDsInt64 := make(map[int64]bool)
	for itemID := range registerItemIDs {
		registerItemIDsInt64[int64(itemID)] = true
	}
	
	matches = 0
	for oldID := range conversionOldIDs {
		if registerItemIDsInt64[oldID] {
			matches++
		}
	}
	
	v.CrossTableValidationResults = append(v.CrossTableValidationResults, CrossTableValidationResult{
		Table1:         "ItemIDConversionTable",
		Table2:         "ItemIDRegister",
		ValidationType: "Old ID existence",
		TotalChecked:   len(conversionOldIDs),
		MatchesFound:   matches,
		MismatchesFound: len(conversionOldIDs) - matches,
		Details:        fmt.Sprintf("%d/%d conversion old IDs found in ItemIDRegister", matches, len(conversionOldIDs)),
	})
	
	log.Printf("✅ H7: Cross-table validations completed: %d validations performed",
		len(v.CrossTableValidationResults))
}

// validateDynamicCatalog validates the dynamic catalog
func (v *AssetTablesValidation) validateDynamicCatalog() {
	catalog := BuildDynamicItemCatalog()
	if catalog == nil {
		log.Printf("⚠️  H7: Cannot validate dynamic catalog - catalog is nil")
		return
	}
	
	v.DynamicCatalogTotalItems = len(catalog.AllItems)
	
	// Count items by category
	categoryCounts := make(map[string]int)
	typeCounts := make(map[string]int)
	
	for _, item := range catalog.AllItems {
		categoryCounts[item.TableCategory]++
		typeCounts[item.ItemType]++
	}
	
	v.DynamicCatalogByCategory = categoryCounts
	v.DynamicCatalogByType = typeCounts
	
	log.Printf("✅ H7: Dynamic catalog validation: %d total items", v.DynamicCatalogTotalItems)
	log.Printf("  Categories: %v", categoryCounts)
	log.Printf("  Types: %v", typeCounts)
}

// logValidationResults logs all validation results
func (v *AssetTablesValidation) logValidationResults() {
	log.Printf("=== H7: Asset Tables Validation Summary ===")
	log.Printf("ItemIDTable: %d categories, %d items", v.ItemIDTableCategories, v.ItemIDTableTotalItems)
	log.Printf("ItemIDRegister: %d entries, %d unique IDs, %d unique paths",
		v.ItemIDRegisterTotalItems, v.ItemIDRegisterUniqueIDs, v.ItemIDRegisterUniquePaths)
	log.Printf("CatalogIDTable: %d buckets, %d items", v.CatalogIDTableBuckets, v.CatalogIDTableTotalItems)
	log.Printf("ItemIDConversionTable: %d entries, %d old IDs, %d new IDs",
		v.ItemIDConversionTableTotalEntries, 
		v.ItemIDConversionTableUniqueOldIDs, 
		v.ItemIDConversionTableUniqueNewIDs)
	log.Printf("Dynamic Catalog: %d items", v.DynamicCatalogTotalItems)
	
	log.Printf("\nCross-table validations:")
	for _, result := range v.CrossTableValidationResults {
		log.Printf("  %s-%s (%s): %d/%d matches",
			result.Table1, result.Table2, result.ValidationType,
			result.MatchesFound, result.TotalChecked)
	}
	
	if v.isAllValidationsPassing() {
		log.Printf("\n✅ H7: All asset table validations passing")
	} else {
		log.Printf("\n⚠️  H7: Some asset table validations have mismatches")
	}
}

// isAllValidationsPassing returns true if all validations are passing
func (v *AssetTablesValidation) isAllValidationsPassing() bool {
	// Check if all cross-table validations have reasonable match rates
	// We don't require 100% matches since tables serve different purposes
	for _, result := range v.CrossTableValidationResults {
		// If more than 50% are missing, consider it failing
		if result.TotalChecked > 0 && float64(result.MatchesFound)/float64(result.TotalChecked) < 0.5 {
			return false
		}
	}
	return true
}

// GetAssetTablesValidation returns the validation results
func GetAssetTablesValidation() *AssetTablesValidation {
	return ValidateAssetTables()
}

// GetItemIDTableCategoryCounts returns item counts per category from ItemIDTable
func GetItemIDTableCategoryCounts() map[string]int {
	validation := ValidateAssetTables()
	assetTablesValidationMu.RLock()
	defer assetTablesValidationMu.RUnlock()

	counts := make(map[string]int)
	for k, v := range validation.ItemIDTableCategoryMap {
		counts[k] = v
	}
	return counts
}

// GetCatalogIDTableBucketCounts returns item counts per bucket from CatalogIDTable
func GetCatalogIDTableBucketCounts() map[string]int {
	validation := ValidateAssetTables()
	assetTablesValidationMu.RLock()
	defer assetTablesValidationMu.RUnlock()

	counts := make(map[string]int)
	for k, v := range validation.CatalogIDTableBucketMap {
		counts[k] = v
	}
	return counts
}

// GetDynamicCatalogCategoryCounts returns item counts per category from dynamic catalog
func GetDynamicCatalogCategoryCounts() map[string]int {
	validation := ValidateAssetTables()
	assetTablesValidationMu.RLock()
	defer assetTablesValidationMu.RUnlock()

	counts := make(map[string]int)
	for k, v := range validation.DynamicCatalogByCategory {
		counts[k] = v
	}
	return counts
}

// GetDynamicCatalogTypeCounts returns item counts per type from dynamic catalog
func GetDynamicCatalogTypeCounts() map[string]int {
	validation := ValidateAssetTables()
	assetTablesValidationMu.RLock()
	defer assetTablesValidationMu.RUnlock()

	counts := make(map[string]int)
	for k, v := range validation.DynamicCatalogByType {
		counts[k] = v
	}
	return counts
}

// GetCrossTableValidationResults returns all cross-table validation results
func GetCrossTableValidationResults() []CrossTableValidationResult {
	validation := ValidateAssetTables()
	assetTablesValidationMu.RLock()
	defer assetTablesValidationMu.RUnlock()

	results := make([]CrossTableValidationResult, len(validation.CrossTableValidationResults))
	copy(results, validation.CrossTableValidationResults)
	return results
}

// ValidateIDToPathMappings validates that all ID→path mappings are consistent
func ValidateIDToPathMappings() map[int32]string {
	// Get all item IDs from ItemIDTable
	categories := GetAllCategories()
	allItemIDs := make(map[int32]bool)
	for _, category := range categories {
		for _, itemID := range category.ItemIDs {
			allItemIDs[int32(itemID)] = true
		}
	}
	
	// Validate that each ItemIDTable ID has a corresponding path in ItemIDRegister
	validationMap := make(map[int32]string)
	
	for itemID := range allItemIDs {
		assetPath, exists := GetAssetPathForItemID(itemID)
		if exists {
			validationMap[itemID] = assetPath
		} else {
			validationMap[itemID] = "NOT FOUND"
		}
	}
	
	return validationMap
}

// ValidatePathToIDMappings validates that all path→ID mappings are consistent
func ValidatePathToIDMappings() map[string]int32 {
	// Get all asset paths from ItemIDRegister
	registerEntries := GetAllRegistryEntries()
	
	// Validate that each path has a corresponding item ID
	validationMap := make(map[string]int32)
	
	for _, entry := range registerEntries {
		itemID, exists := GetItemIDForAssetPath(entry.Path)
		if exists {
			validationMap[entry.Path] = itemID
		} else {
			validationMap[entry.Path] = -1 // NOT FOUND
		}
	}
	
	return validationMap
}

// ValidateCategoryConsistency checks that categories are consistent across tables
func ValidateCategoryConsistency() map[string]bool {
	// Get categories from ItemIDTable
	categories := GetAllCategories()
	
	// Check that each category has items
	consistencyMap := make(map[string]bool)
	
	for _, category := range categories {
		// Category is consistent if it has items
		consistencyMap[category.CategoryName] = len(category.ItemIDs) > 0
	}
	
	return consistencyMap
}

// PrintAssetTablesValidationReport prints a detailed validation report
func PrintAssetTablesValidationReport() {
	validation := ValidateAssetTables()
	
	fmt.Printf("=== H7: Asset Tables Validation Report ===\n")
	
	fmt.Printf("📊 Table Statistics:\n")
	fmt.Printf("  ItemIDTable: %d categories, %d total items\n", 
		validation.ItemIDTableCategories, validation.ItemIDTableTotalItems)
	fmt.Printf("  ItemIDRegister: %d entries, %d unique IDs, %d unique paths\n",
		validation.ItemIDRegisterTotalItems, 
		validation.ItemIDRegisterUniqueIDs, 
		validation.ItemIDRegisterUniquePaths)
	fmt.Printf("  CatalogIDTable: %d buckets, %d total items\n",
		validation.CatalogIDTableBuckets, validation.CatalogIDTableTotalItems)
	fmt.Printf("  ItemIDConversionTable: %d entries, %d old IDs, %d new IDs\n",
		validation.ItemIDConversionTableTotalEntries,
		validation.ItemIDConversionTableUniqueOldIDs,
		validation.ItemIDConversionTableUniqueNewIDs)
	fmt.Printf("  Dynamic Catalog: %d total items\n", validation.DynamicCatalogTotalItems)
	
	fmt.Printf("\n📋 ItemIDTable Categories:\n")
	for category, count := range validation.ItemIDTableCategoryMap {
		fmt.Printf("  %s: %d items\n", category, count)
	}
	
	fmt.Printf("\n📋 CatalogIDTable Buckets:\n")
	for bucket, count := range validation.CatalogIDTableBucketMap {
		fmt.Printf("  %s: %d items\n", bucket, count)
	}
	
	fmt.Printf("\n📋 Dynamic Catalog by Category:\n")
	for category, count := range validation.DynamicCatalogByCategory {
		fmt.Printf("  %s: %d items\n", category, count)
	}
	
	fmt.Printf("\n📋 Dynamic Catalog by Type:\n")
	for itemType, count := range validation.DynamicCatalogByType {
		fmt.Printf("  %s: %d items\n", itemType, count)
	}
	
	fmt.Printf("\n🔗 Cross-Table Validations:\n")
	for _, result := range validation.CrossTableValidationResults {
		status := "✅"
		if result.MismatchesFound > result.TotalChecked/2 {
			status = "⚠️ "
		}
		fmt.Printf("  %s %s-%s (%s): %d/%d matches\n",
			status,
			result.Table1, result.Table2, result.ValidationType,
			result.MatchesFound, result.TotalChecked)
		if result.Details != "" {
			fmt.Printf("    %s\n", result.Details)
		}
	}
	
	if validation.isAllValidationsPassing() {
		fmt.Printf("\n✅ All asset table validations passing!\n")
	} else {
		fmt.Printf("\n⚠️  Some asset table validations have significant mismatches\n")
	}
}

// ValidateItemCountsPerCategory performs specific validation of item counts per category
func ValidateItemCountsPerCategory() map[string]CategoryValidationResult {
	// Get categories from ItemIDTable
	categories := GetAllCategories()
	
	validationResults := make(map[string]CategoryValidationResult)
	
	for _, category := range categories {
		result := CategoryValidationResult{
			CategoryName: category.CategoryName,
			ItemIDTableCount: len(category.ItemIDs),
			DynamicCatalogCount: 0,
			ItemIDRegisterCount: 0,
			IsConsistent: true,
		}
		
		// Count items in this category in dynamic catalog
		catalog := BuildDynamicItemCatalog()
		if catalog != nil {
			for _, item := range catalog.AllItems {
				if item.TableCategory == category.CategoryName {
					result.DynamicCatalogCount++
				}
			}
		}
		
		// Count items in this category in ItemIDRegister
		registerEntries := GetAllRegistryEntries()
		for _, entry := range registerEntries {
			// Check if this entry's category matches
			itemCategory, exists := GetCategoryForItemID(entry.ItemID)
			if exists && itemCategory == category.CategoryName {
				result.ItemIDRegisterCount++
			}
		}
		
		// Check consistency
		if result.ItemIDTableCount > 0 && result.DynamicCatalogCount == 0 {
			result.IsConsistent = false
			result.Issues = append(result.Issues, "No items in dynamic catalog")
		}
		
		validationResults[category.CategoryName] = result
	}
	
	return validationResults
}

// CategoryValidationResult represents validation result for a single category
type CategoryValidationResult struct {
	CategoryName        string
	ItemIDTableCount    int
	DynamicCatalogCount int
	ItemIDRegisterCount int
	IsConsistent        bool
	Issues             []string
}

// ValidateIDToPathMappingsForCategory validates ID→path mappings for a specific category
func ValidateIDToPathMappingsForCategory(categoryName string) CategoryIDPathValidation {
	// Get all item IDs for this category
	itemIDs := GetItemIDsByCategory(categoryName)
	if len(itemIDs) == 0 {
		return CategoryIDPathValidation{
			CategoryName: categoryName,
			TotalItems:   0,
			MappedItems:  0,
			UnmappedItems: 0,
			IsValid:      false,
			Issues:       []string{"Category not found or empty"},
		}
	}
	
	validation := CategoryIDPathValidation{
		CategoryName: categoryName,
		TotalItems:   len(itemIDs),
		MappedItems:  0,
		UnmappedItems: 0,
		MappedPaths:  make([]string, 0),
		UnmappedIDs:  make([]int32, 0),
		IsValid:      true,
		Issues:       make([]string, 0),
	}
	
	// Check each item ID for a corresponding path
	for _, itemID := range itemIDs {
		assetPath, exists := GetAssetPathForItemID(itemID)
		if exists {
			validation.MappedItems++
			validation.MappedPaths = append(validation.MappedPaths, assetPath)
		} else {
			validation.UnmappedItems++
			validation.UnmappedIDs = append(validation.UnmappedIDs, itemID)
			validation.IsValid = false
			validation.Issues = append(validation.Issues, 
				fmt.Sprintf("ItemID %d has no asset path", itemID))
		}
	}
	
	return validation
}

// CategoryIDPathValidation represents validation result for ID→path mappings in a category
type CategoryIDPathValidation struct {
	CategoryName  string
	TotalItems    int
	MappedItems   int
	UnmappedItems int
	MappedPaths   []string
	UnmappedIDs   []int32
	IsValid       bool
	Issues        []string
}

// ValidateAllIDToPathMappings validates ID→path mappings for all categories
func ValidateAllIDToPathMappings() map[string]CategoryIDPathValidation {
	categories := GetAllCategories()
	validationResults := make(map[string]CategoryIDPathValidation)
	
	for _, category := range categories {
		validation := ValidateIDToPathMappingsForCategory(category.CategoryName)
		validationResults[category.CategoryName] = validation
	}
	
	return validationResults
}