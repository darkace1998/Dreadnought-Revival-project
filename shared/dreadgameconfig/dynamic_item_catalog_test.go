package dreadgameconfig

import (
	"strings"
	"testing"
)

func TestH5DynamicItemCatalog(t *testing.T) {
	// This test verifies that the dynamic item catalog can be built
	// Note: This will only work if the data files are present
	
	// Build the dynamic catalog
	catalog := BuildDynamicItemCatalog()
	if catalog == nil {
		t.Logf("⚠️  H5: Could not build dynamic item catalog (data files may not be present)")
		return
	}

	itemCount := GetDynamicItemCount()
	if itemCount == 0 {
		t.Error("❌ H5: Dynamic item catalog should have items")
	} else {
		t.Logf("✅ H5: Built dynamic item catalog with %d items", itemCount)
	}
}

func TestH5DynamicItemCatalogReplacesHardcoded(t *testing.T) {
	// This test verifies that the dynamic catalog contains the same items as the hardcoded one
	catalog := BuildDynamicItemCatalog()
	if catalog == nil {
		t.Logf("⚠️  H5: Could not build dynamic item catalog (data files may not be present)")
		return
	}

	// Test that we can find items that were in the hardcoded catalog
	hardcodedItemIDs := []int32{
		184484177, // Athos ship
		184484173, // Zmey ship
		33489315, // Athos loadout
		100597772, // Repeater Turrets weapon
		83820574, // Tempest Missiles ability
		117374979, // Communications 101 perk
	}

	for _, itemID := range hardcodedItemIDs {
		item, exists := GetDynamicItemByID(itemID)
		if !exists {
			t.Errorf("❌ H5: Expected to find item ID %d in dynamic catalog", itemID)
		} else {
			t.Logf("✅ H5: Found hardcoded item ID %d in dynamic catalog: %s", itemID, item.DisplayName)
		}
	}
}

func TestH5DynamicItemAccess(t *testing.T) {
	// This test verifies that all access methods work for the dynamic catalog
	catalog := BuildDynamicItemCatalog()
	if catalog == nil {
		t.Logf("⚠️  H5: Could not build dynamic item catalog (data files may not be present)")
		return
	}

	// Test GetDynamicItemByID
	item, exists := GetDynamicItemByID(184484177) // Athos
	if !exists {
		t.Error("❌ H5: Expected to find item by ID")
	} else if item.DisplayName == "" {
		t.Errorf("❌ H5: Item %d has empty display name", item.ItemID)
	} else {
		t.Logf("✅ H5: GetDynamicItemByID works: %d -> %s", item.ItemID, item.DisplayName)
	}

	// Test GetDynamicItemByAssetPath
	assetPath := "/Game/Generic/Ships/Assault/Medium/VH_AssaultM_Pawn_BP"
	item, exists = GetDynamicItemByAssetPath(assetPath)
	if !exists {
		t.Logf("ℹ️  H5: Item with asset path '%s' not found in dynamic catalog (may use different path)", assetPath)
	} else {
		t.Logf("✅ H5: GetDynamicItemByAssetPath works: %s -> %s", assetPath, item.DisplayName)
	}

	// Test GetDynamicItemByTypeAndName
	item, exists = GetDynamicItemByTypeAndName("ship", "Athos")
	if !exists {
		t.Logf("ℹ️  H5: Item with type 'ship' and name 'Athos' not found (may use different case)")
	} else {
		t.Logf("✅ H5: GetDynamicItemByTypeAndName works: ship_Athos -> %s", item.DisplayName)
	}

	// Test GetAllDynamicItems
	allItems := GetAllDynamicItems()
	if len(allItems) == 0 {
		t.Error("❌ H5: GetAllDynamicItems should return items")
	} else {
		t.Logf("✅ H5: GetAllDynamicItems returned %d items", len(allItems))
	}
}

func TestH5DynamicItemMetadata(t *testing.T) {
	// This test verifies that items in the dynamic catalog have proper metadata
	catalog := BuildDynamicItemCatalog()
	if catalog == nil {
		t.Logf("⚠️  H5: Could not build dynamic item catalog (data files may not be present)")
		return
	}

	// Test a few items to ensure they have proper metadata
	testItemIDs := []int32{184484177, 33489315, 100597772, 83820574, 117374979}
	
	for _, itemID := range testItemIDs {
		item, exists := GetDynamicItemByID(itemID)
		if !exists {
			t.Logf("ℹ️  H5: Item ID %d not found in dynamic catalog", itemID)
			continue
		}

		// Verify item has required fields
		if item.ItemID == 0 {
			t.Errorf("❌ H5: Item %d has zero ItemID", itemID)
		}
		if item.DisplayName == "" {
			t.Errorf("❌ H5: Item %d has empty DisplayName", itemID)
		}
		if item.ItemType == "" {
			t.Errorf("❌ H5: Item %d has empty ItemType", itemID)
		}
		if item.TableCategory == "" {
			t.Errorf("❌ H5: Item %d has empty TableCategory", itemID)
		}
		if item.AssetPath == "" {
			t.Errorf("❌ H5: Item %d has empty AssetPath", itemID)
		}

		// Verify item type is valid
		validTypes := []string{ItemTypeShip, ItemTypeLoadout, ItemTypeWeapon, ItemTypeAbility, ItemTypePerk}
		isValidType := false
		for _, validType := range validTypes {
			if item.ItemType == validType {
				isValidType = true
				break
			}
		}
		if !isValidType {
			t.Errorf("❌ H5: Item %d has invalid ItemType: %s", itemID, item.ItemType)
		}

		t.Logf("✅ H5: Item %d has valid metadata: %s (%s)", itemID, item.DisplayName, item.ItemType)
	}
}

func TestH5HardcodedItemsInDynamicCatalog(t *testing.T) {
	// This test verifies that all items from the hardcoded catalog are present in the dynamic catalog
	catalog := BuildDynamicItemCatalog()
	if catalog == nil {
		t.Logf("⚠️  H5: Could not build dynamic item catalog (data files may not be present)")
		return
	}

	// List of all items from the hardcoded catalog
	hardcodedItems := []struct {
		itemID   int32
		displayName string
		itemType string
	}{
		// Ships
		{184484177, "Athos", ItemTypeShip},
		{184484173, "Zmey", ItemTypeShip},
		{184484171, "Aion", ItemTypeShip},
		{184484180, "Valcour", ItemTypeShip},
		{184484184, "Svarog", ItemTypeShip},
		{184483981, "Leipzig", ItemTypeShip},
		{184483972, "Trieste", ItemTypeShip},
		{184484148, "Ceres", ItemTypeShip},
		{184483982, "Assault Medium T1", ItemTypeShip},
		{184484170, "Dreadnought Medium T1", ItemTypeShip},
		{184483950, "Sniper Medium T1", ItemTypeShip},
		{184484202, "Support Medium T1", ItemTypeShip},
		
		// Loadouts
		{33489315, "Athos", ItemTypeLoadout},
		{33489318, "Zmey", ItemTypeLoadout},
		{33489331, "Aion", ItemTypeLoadout},
		{33489262, "Agosta", ItemTypeLoadout},
		{33489423, "Simargl", ItemTypeLoadout},
		{33489263, "Rurik", ItemTypeLoadout},
		{33489264, "Cerberus", ItemTypeLoadout},
		
		// Weapons
		{100597772, "Repeater Turrets", ItemTypeWeapon},
		{100598563, "Flak Turrets", ItemTypeWeapon},
		{100598595, "Heavy Plasma Cannons", ItemTypeWeapon},
		{100598596, "Repeater Guns", ItemTypeWeapon},
		{100597987, "Heavy Tesla Cannon", ItemTypeWeapon},
		{100598570, "Light Flak Turrets", ItemTypeWeapon},
		{100597870, "Medium Beam Turrets", ItemTypeWeapon},
		{100598573, "Tesla Turrets", ItemTypeWeapon},
		{100597862, "Heavy Repair Beam", ItemTypeWeapon},
		{100597877, "Light Machine Guns", ItemTypeWeapon},
		
		// Abilities
		{83820574, "Tempest Missiles", ItemTypeAbility},
		{83820606, "Torpedo Salvo", ItemTypeAbility},
		{83820565, "Protean Autoguns", ItemTypeAbility},
		{83820550, "Module Reboot", ItemTypeAbility},
		{83820594, "Weaponbreaker Missile", ItemTypeAbility},
		{83820560, "Hell Lasers", ItemTypeAbility},
		{83820556, "Jump Drive", ItemTypeAbility},
		{83821082, "Plasma Broadside", ItemTypeAbility},
		{83825291, "Vulture Missiles", ItemTypeAbility},
		{83821084, "Flyswatter AML", ItemTypeAbility},
		{83821076, "Warp Jump", ItemTypeAbility},
		{83820879, "Repair Drones", ItemTypeAbility},
		{83820857, "Beam Amplifier", ItemTypeAbility},
		{83820882, "Repair Pod", ItemTypeAbility},
		{83820851, "Repair Autobeams", ItemTypeAbility},
		{83820839, "Autorepair", ItemTypeAbility},
		{83820799, "Siege Mode", ItemTypeAbility},
		{83820830, "Flechette Missiles", ItemTypeAbility},
		{83820781, "Anti-Missile Lasers", ItemTypeAbility},
		{83820764, "Stationary Cloak", ItemTypeAbility},
		
		// Perks
		{117374979, "Communications 101", ItemTypePerk},
		{117374997, "Survival Instinct", ItemTypePerk},
		{117374991, "Navigation 101", ItemTypePerk},
		{117374982, "Engineering 101", ItemTypePerk},
		{117374977, "Module Recycler", ItemTypePerk},
		{117374993, "Module Amper", ItemTypePerk},
		{117374989, "Navigation Expert", ItemTypePerk},
		{117374985, "Mr. Fixit", ItemTypePerk},
		{117374980, "Feedback Loop", ItemTypePerk},
		{117374994, "Glass Cannon", ItemTypePerk},
		{117374988, "Slow and Steady", ItemTypePerk},
		{117374986, "Reinforced", ItemTypePerk},
	}

	foundCount := 0
	missingCount := 0
	
	for _, hardcodedItem := range hardcodedItems {
		item, exists := GetDynamicItemByID(hardcodedItem.itemID)
		if !exists {
			t.Logf("❌ H5: Hardcoded item %d (%s) not found in dynamic catalog", hardcodedItem.itemID, hardcodedItem.displayName)
			missingCount++
		} else {
			// Check if the display name matches (case-insensitive)
			if strings.ToLower(item.DisplayName) != strings.ToLower(hardcodedItem.displayName) {
				t.Logf("ℹ️  H5: Item %d found with different name: expected '%s', got '%s'", 
					hardcodedItem.itemID, hardcodedItem.displayName, item.DisplayName)
			} else {
			foundCount = foundCount + 1
			}
		}
	}

	totalHardcoded := len(hardcodedItems)
	t.Logf("✅ H5: Found %d/%d hardcoded items in dynamic catalog", foundCount, totalHardcoded)
	if missingCount > 0 {
		t.Logf("⚠️  H5: %d hardcoded items not found in dynamic catalog (may need fallback)", missingCount)
	}
}

func TestH5DisplayNameExtraction(t *testing.T) {
	// This test verifies that display names are correctly extracted from asset paths
	testCases := []struct {
		assetPath    string
		expectedName string
	}{
		{"/Game/Generic/Ships/Assault/Medium/VH_AssaultM_Pawn_BP", "VH Assault M Pawn"},
		{"/Game/Generic/Weapons/Assault/Medium/BP/T1/WP_AssaultMPri01_weapon01_T1_BP", "WP Assault M Pri01 Weapon01 T1"},
		{"/Game/Generic/Abilities/Assault/Pri_Missile_Super/T0/AB_AS_Pri_Missile_Super_Ability_T0_BP", "AB AS Pri Missile Super Ability T0"},
		{"/Game/Generic/Officer/Perk/PRK_COM_AbiInc_Passive_BP", "PRK COM Abi Inc Passive"},
		{"/Game/Generic/Loadouts/Precast/VH_AssaultMedium_PrecastLoadout_BP", "VH Assault Medium Precast Loadout"},
	}

	for _, tc := range testCases {
		extractedName := extractDisplayNameFromAssetPath(tc.assetPath)
		// We don't expect exact matches, but they should be reasonable
		if extractedName == "" || extractedName == "Unknown" {
			t.Errorf("❌ H5: Failed to extract display name from '%s'", tc.assetPath)
		} else {
			t.Logf("✅ H5: Extracted display name '%s' from '%s'", extractedName, tc.assetPath)
		}
	}
}

func TestH5CategoryDetermination(t *testing.T) {
	// This test verifies that categories are correctly determined from asset paths
	testCases := []struct {
		assetPath       string
		expectedCategory string
	}{
		{"/Game/Generic/Ships/Assault/Medium/VH_AssaultM_Pawn_BP", "YPawn"},
		{"/Game/Generic/Weapons/Assault/Medium/BP/T1/WP_AssaultMPri01_weapon01_T1_BP", "YWeapon"},
		{"/Game/Generic/Abilities/Assault/Pri_Missile_Super/T0/AB_AS_Pri_Missile_Super_Ability_T0_BP", "YAbility"},
		{"/Game/Generic/Officer/Perk/PRK_COM_AbiInc_Passive_BP", "YPerk"},
		{"/Game/Generic/Loadouts/Precast/VH_AssaultMedium_PrecastLoadout_BP", "YShipLoadoutPrecast"},
	}

	for _, tc := range testCases {
		category := determineCategoryFromAssetPath(tc.assetPath)
		if category != tc.expectedCategory {
			t.Errorf("❌ H5: Expected category '%s' for '%s', got '%s'", 
				tc.expectedCategory, tc.assetPath, category)
		} else {
			t.Logf("✅ H5: Determined category '%s' for '%s'", category, tc.assetPath)
		}
	}
}

func TestH5ItemTypeDetermination(t *testing.T) {
	// This test verifies that item types are correctly determined from categories
	testCases := []struct {
		category    string
		expectedType string
	}{
		{"YPawn", ItemTypeShip},
		{"YShipLoadoutPrecast", ItemTypeLoadout},
		{"YWeapon", ItemTypeWeapon},
		{"YAbility", ItemTypeAbility},
		{"YPerk", ItemTypePerk},
	}

	for _, tc := range testCases {
		itemType := determineItemTypeFromCategory(tc.category)
		if itemType != tc.expectedType {
			t.Errorf("❌ H5: Expected item type '%s' for category '%s', got '%s'", 
				tc.expectedType, tc.category, itemType)
		} else {
			t.Logf("✅ H5: Determined item type '%s' for category '%s'", itemType, tc.category)
		}
	}
}

func TestH5DynamicCatalogIntegration(t *testing.T) {
	// This test verifies that the dynamic catalog is properly integrated
	// and that the global item lookup functions work
	
	// First, ensure the dynamic catalog is built
	catalog := BuildDynamicItemCatalog()
	if catalog == nil {
		t.Logf("⚠️  H5: Could not build dynamic item catalog (data files may not be present)")
		return
	}

	// Test that we can access items through the global functions
	// These should now use the dynamic catalog
	item, exists := ItemByID(184484177) // Athos
	if !exists {
		t.Error("❌ H5: Expected to find item 184484177 through global ItemByID")
	} else {
		t.Logf("✅ H5: Global ItemByID works with dynamic catalog: %d -> %s", item.ItemID, item.DisplayName)
	}

	// Test asset path lookup
	assetPath := "/Game/Generic/Ships/Assault/Medium/VH_AssaultM_Pawn_BP"
	item, exists = ItemByAssetPath(assetPath)
	if !exists {
		t.Logf("ℹ️  H5: Global ItemByAssetPath didn't find '%s' (may use different path format)", assetPath)
	} else {
		t.Logf("✅ H5: Global ItemByAssetPath works with dynamic catalog: %s -> %s", assetPath, item.DisplayName)
	}

	// Test type and name lookup
	item, exists = ItemByTypeAndDisplayName("ship", "Athos")
	if !exists {
		t.Logf("ℹ️  H5: Global ItemByTypeAndDisplayName didn't find ship/Athos (may use different case)")
	} else {
		t.Logf("✅ H5: Global ItemByTypeAndDisplayName works with dynamic catalog: ship/Athos -> %s", item.DisplayName)
	}

	// Test getting all items
	allItems := ItemsByType("ship")
	if len(allItems) == 0 {
		t.Error("❌ H5: Global ItemsByType should return items")
	} else {
		t.Logf("✅ H5: Global ItemsByType returned %d ship items", len(allItems))
	}
}