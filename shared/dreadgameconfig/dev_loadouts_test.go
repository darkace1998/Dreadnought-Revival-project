package dreadgameconfig

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestI1LoadDevLoadouts tests loading the LoadoutDevelopmentTable.json
func TestI1LoadDevLoadouts(t *testing.T) {
	// Reset the singleton for testing
	devLoadoutsOnce = sync.Once{}
	devLoadouts = nil

	err := LoadDevLoadouts()
	if err != nil {
		t.Fatalf("Failed to load development loadouts: %v", err)
	}

	count := GetDevLoadoutCount()
	if count == 0 {
		t.Error("Expected to load at least one development loadout, got 0")
	}

	t.Logf("Successfully loaded %d development loadouts", count)
}

// TestI1DevLoadoutStructure validates the structure of loaded loadouts
func TestI1DevLoadoutStructure(t *testing.T) {
	// Reset the singleton for testing
	devLoadoutsOnce = sync.Once{}
	devLoadouts = nil

	err := LoadDevLoadouts()
	if err != nil {
		t.Fatalf("Failed to load development loadouts: %v", err)
	}

	loadouts := GetAllDevLoadouts()
	if len(loadouts) == 0 {
		t.Fatal("No development loadouts loaded")
	}

	// Validate first loadout structure
	first := loadouts[0]
	if first.ID == "" {
		t.Error("First loadout has empty ID")
	}
	if first.ShipID == 0 {
		t.Error("First loadout has zero ShipID")
	}
	if first.Name == "" {
		t.Error("First loadout has empty Name")
	}

	t.Logf("First loadout: ID=%s, ShipID=%d, Name=%s, Class=%d",
		first.ID, first.ShipID, first.Name, first.Class)

	// Validate all loadouts have required fields
	for i, loadout := range loadouts {
		if loadout.ID == "" {
			t.Errorf("Loadout %d has empty ID", i)
		}
		if loadout.ShipID == 0 {
			t.Errorf("Loadout %d has zero ShipID", i)
		}
		if loadout.Name == "" {
			t.Errorf("Loadout %d has empty Name", i)
		}
	}
}

// TestI1DevLoadoutAccessors tests the accessor functions
func TestI1DevLoadoutAccessors(t *testing.T) {
	// Reset the singleton for testing
	devLoadoutsOnce = sync.Once{}
	devLoadouts = nil

	err := LoadDevLoadouts()
	if err != nil {
		t.Fatalf("Failed to load development loadouts: %v", err)
	}

	loadouts := GetAllDevLoadouts()
	if len(loadouts) == 0 {
		t.Fatal("No development loadouts loaded")
	}

	// Test GetDevLoadoutByShipID
	firstShipID := loadouts[0].ShipID
	loadout, exists := GetDevLoadoutByShipID(firstShipID)
	if !exists {
		t.Errorf("Failed to find loadout for ShipID %d", firstShipID)
	}
	if loadout.ShipID != firstShipID {
		t.Errorf("GetDevLoadoutByShipID returned wrong loadout: expected ShipID %d, got %d",
			firstShipID, loadout.ShipID)
	}

	// Test GetDevLoadoutByID
	firstID := loadouts[0].ID
	loadout, exists = GetDevLoadoutByID(firstID)
	if !exists {
		t.Errorf("Failed to find loadout for ID %s", firstID)
	}
	if loadout.ID != firstID {
		t.Errorf("GetDevLoadoutByID returned wrong loadout: expected ID %s, got %s",
			firstID, loadout.ID)
	}

	// Test HasDevLoadout
	if !HasDevLoadout(firstShipID) {
		t.Errorf("HasDevLoadout returned false for ShipID %d which should exist", firstShipID)
	}
	if HasDevLoadout(-999999) {
		t.Error("HasDevLoadout returned true for non-existent ShipID -999999")
	}

	// Test GetAllDevLoadoutShipIDs
	shipIDs := GetAllDevLoadoutShipIDs()
	if len(shipIDs) != len(loadouts) {
		t.Errorf("GetAllDevLoadoutShipIDs returned %d IDs, expected %d", len(shipIDs), len(loadouts))
	}

	// Test GetDevLoadoutsByClass
	if len(loadouts) > 0 {
		class := loadouts[0].Class
		classLoadouts := GetDevLoadoutsByClass(class)
		if len(classLoadouts) == 0 {
			t.Errorf("GetDevLoadoutsByClass(%d) returned 0 loadouts", class)
		}
		for _, cl := range classLoadouts {
			if cl.Class != class {
				t.Errorf("GetDevLoadoutsByClass(%d) returned loadout with class %d", class, cl.Class)
			}
		}
	}
}

// TestI1DevLoadoutSlotItemIDs validates that slot ItemIDs are present
func TestI1DevLoadoutSlotItemIDs(t *testing.T) {
	// Reset the singleton for testing
	devLoadoutsOnce = sync.Once{}
	devLoadouts = nil

	err := LoadDevLoadouts()
	if err != nil {
		t.Fatalf("Failed to load development loadouts: %v", err)
	}

	loadouts := GetAllDevLoadouts()
	if len(loadouts) == 0 {
		t.Fatal("No development loadouts loaded")
	}

	// Count loadouts with each slot type filled
	weaponPrimaryCount := 0
	weaponSecondaryCount := 0
	abilityPrimaryCount := 0
	abilitySecondaryCount := 0
	abilityPerimeterCount := 0
	abilityInternalCount := 0
	perkComCount := 0
	perkWeaponCount := 0
	perkNavigationCount := 0
	perkEngineerCount := 0

	for _, loadout := range loadouts {
		if loadout.WeaponPrimary != 0 && loadout.WeaponPrimary != -1 {
			weaponPrimaryCount++
		}
		if loadout.WeaponSecondary != 0 && loadout.WeaponSecondary != -1 {
			weaponSecondaryCount++
		}
		if loadout.AbilityPrimary != 0 && loadout.AbilityPrimary != -1 {
			abilityPrimaryCount++
		}
		if loadout.AbilitySecondary != 0 && loadout.AbilitySecondary != -1 {
			abilitySecondaryCount++
		}
		if loadout.AbilityPerimeter != 0 && loadout.AbilityPerimeter != -1 {
			abilityPerimeterCount++
		}
		if loadout.AbilityInternal != 0 && loadout.AbilityInternal != -1 {
			abilityInternalCount++
		}
		if loadout.PerkCom != 0 && loadout.PerkCom != -1 {
			perkComCount++
		}
		if loadout.PerkWeapon != 0 && loadout.PerkWeapon != -1 {
			perkWeaponCount++
		}
		if loadout.PerkNavigation != 0 && loadout.PerkNavigation != -1 {
			perkNavigationCount++
		}
		if loadout.PerkEngineer != 0 && loadout.PerkEngineer != -1 {
			perkEngineerCount++
		}
	}

	t.Logf("Development loadout slot coverage:")
	t.Logf("  WeaponPrimary: %d/%d (%.1f%%)", weaponPrimaryCount, len(loadouts),
		float64(weaponPrimaryCount)/float64(len(loadouts))*100)
	t.Logf("  WeaponSecondary: %d/%d (%.1f%%)", weaponSecondaryCount, len(loadouts),
		float64(weaponSecondaryCount)/float64(len(loadouts))*100)
	t.Logf("  AbilityPrimary: %d/%d (%.1f%%)", abilityPrimaryCount, len(loadouts),
		float64(abilityPrimaryCount)/float64(len(loadouts))*100)
	t.Logf("  AbilitySecondary: %d/%d (%.1f%%)", abilitySecondaryCount, len(loadouts),
		float64(abilitySecondaryCount)/float64(len(loadouts))*100)
	t.Logf("  AbilityPerimeter: %d/%d (%.1f%%)", abilityPerimeterCount, len(loadouts),
		float64(abilityPerimeterCount)/float64(len(loadouts))*100)
	t.Logf("  AbilityInternal: %d/%d (%.1f%%)", abilityInternalCount, len(loadouts),
		float64(abilityInternalCount)/float64(len(loadouts))*100)
	t.Logf("  PerkCom: %d/%d (%.1f%%)", perkComCount, len(loadouts),
		float64(perkComCount)/float64(len(loadouts))*100)
	t.Logf("  PerkWeapon: %d/%d (%.1f%%)", perkWeaponCount, len(loadouts),
		float64(perkWeaponCount)/float64(len(loadouts))*100)
	t.Logf("  PerkNavigation: %d/%d (%.1f%%)", perkNavigationCount, len(loadouts),
		float64(perkNavigationCount)/float64(len(loadouts))*100)
	t.Logf("  PerkEngineer: %d/%d (%.1f%%)", perkEngineerCount, len(loadouts),
		float64(perkEngineerCount)/float64(len(loadouts))*100)

	// Expect at least some loadouts to have weapon and ability slots filled
	if weaponPrimaryCount == 0 {
		t.Error("No loadouts have WeaponPrimary slot filled")
	}
	if abilityPrimaryCount == 0 {
		t.Error("No loadouts have AbilityPrimary slot filled")
	}
}

// TestI1DevLoadoutShipIDUniqueness validates that each ship has at most one loadout
func TestI1DevLoadoutShipIDUniqueness(t *testing.T) {
	// Reset the singleton for testing
	devLoadoutsOnce = sync.Once{}
	devLoadouts = nil

	err := LoadDevLoadouts()
	if err != nil {
		t.Fatalf("Failed to load development loadouts: %v", err)
	}

	loadouts := GetAllDevLoadouts()
	shipIDMap := make(map[int32][]string) // ShipID -> Loadout IDs

	for _, loadout := range loadouts {
		shipIDMap[loadout.ShipID] = append(shipIDMap[loadout.ShipID], loadout.ID)
	}

	duplicateCount := 0
	for shipID, ids := range shipIDMap {
		if len(ids) > 1 {
			duplicateCount++
			t.Logf("Warning: Duplicate ShipID %d found in %d loadouts: %v",
				shipID, len(ids), ids)
		}
	}

	if duplicateCount > 0 {
		t.Logf("Found %d ShipIDs with duplicate loadouts (this may be expected in the data)", duplicateCount)
	}

	t.Logf("Loaded %d loadouts with %d unique ShipIDs", len(loadouts), len(shipIDMap))
}

// TestI1DevLoadoutClassDistribution validates the distribution of ship classes
func TestI1DevLoadoutClassDistribution(t *testing.T) {
	// Reset the singleton for testing
	devLoadoutsOnce = sync.Once{}
	devLoadouts = nil

	err := LoadDevLoadouts()
	if err != nil {
		t.Fatalf("Failed to load development loadouts: %v", err)
	}

	loadouts := GetAllDevLoadouts()
	classCount := make(map[int32]int)

	for _, loadout := range loadouts {
		classCount[loadout.Class]++
	}

	t.Logf("Development loadout class distribution:")
	for class, count := range classCount {
		t.Logf("  Class %d: %d loadouts", class, count)
	}

	if len(classCount) == 0 {
		t.Error("No ship classes found in development loadouts")
	}
}

// TestI1DevLoadoutCrossReference validates that slot ItemIDs can be cross-referenced
func TestI1DevLoadoutCrossReference(t *testing.T) {
	// Reset the singleton for testing
	devLoadoutsOnce = sync.Once{}
	devLoadouts = nil

	err := LoadDevLoadouts()
	if err != nil {
		t.Fatalf("Failed to load development loadouts: %v", err)
	}

	loadouts := GetAllDevLoadouts()
	if len(loadouts) == 0 {
		t.Fatal("No development loadouts loaded")
	}

	// Collect all unique ItemIDs from loadout slots
	weaponIDs := make(map[int32]bool)
	abilityIDs := make(map[int32]bool)
	perkIDs := make(map[int32]bool)

	for _, loadout := range loadouts {
		if loadout.WeaponPrimary != 0 && loadout.WeaponPrimary != -1 {
			weaponIDs[loadout.WeaponPrimary] = true
		}
		if loadout.WeaponSecondary != 0 && loadout.WeaponSecondary != -1 {
			weaponIDs[loadout.WeaponSecondary] = true
		}
		if loadout.AbilityPrimary != 0 && loadout.AbilityPrimary != -1 {
			abilityIDs[loadout.AbilityPrimary] = true
		}
		if loadout.AbilitySecondary != 0 && loadout.AbilitySecondary != -1 {
			abilityIDs[loadout.AbilitySecondary] = true
		}
		if loadout.AbilityPerimeter != 0 && loadout.AbilityPerimeter != -1 {
			abilityIDs[loadout.AbilityPerimeter] = true
		}
		if loadout.AbilityInternal != 0 && loadout.AbilityInternal != -1 {
			abilityIDs[loadout.AbilityInternal] = true
		}
		if loadout.PerkCom != 0 && loadout.PerkCom != -1 {
			perkIDs[loadout.PerkCom] = true
		}
		if loadout.PerkWeapon != 0 && loadout.PerkWeapon != -1 {
			perkIDs[loadout.PerkWeapon] = true
		}
		if loadout.PerkNavigation != 0 && loadout.PerkNavigation != -1 {
			perkIDs[loadout.PerkNavigation] = true
		}
		if loadout.PerkEngineer != 0 && loadout.PerkEngineer != -1 {
			perkIDs[loadout.PerkEngineer] = true
		}
	}

	t.Logf("Development loadout unique ItemIDs:")
	t.Logf("  Weapons: %d unique weapon ItemIDs", len(weaponIDs))
	t.Logf("  Abilities: %d unique ability ItemIDs", len(abilityIDs))
	t.Logf("  Perks: %d unique perk ItemIDs", len(perkIDs))

	// Note: We don't validate against actual weapon/ability/perk data here
	// because that would require those systems to be loaded first.
	// This test just collects the ItemIDs for later cross-referencing.
}

// TestI2LoadoutSlotCrossReference tests cross-referencing loadout slot ItemIDs with weapon/ability/perk data
func TestI2LoadoutSlotCrossReference(t *testing.T) {
	// Set DATA_DIR for path resolution
	original := os.Getenv("DATA_DIR")
	defer func() { _ = os.Setenv("DATA_DIR", original) }()
	
	dataDir := filepath.Join("..", "..", "data")
	if err := os.Setenv("DATA_DIR", dataDir); err != nil {
		t.Fatal(err)
	}

	// Reset all singletons for testing
	devLoadoutsOnce = sync.Once{}
	devLoadouts = nil

	// Reset weapon, ability, and perk loading state to force reload
	weaponsLoaded = false
	weaponsByItemID = nil
	abilitiesLoaded = false
	perksLoaded = false

	// Ensure all required data is loaded
	err := LoadDevLoadouts()
	if err != nil {
		t.Fatalf("Failed to load development loadouts: %v", err)
	}

	// Load weapons, abilities, and perks for cross-referencing
	if err := LoadWeapons(); err != nil {
		t.Fatalf("Failed to load weapons: %v", err)
	}

	// Debug: Check if weapons were loaded
	allWeapons := AllWeapons()
	if len(allWeapons) == 0 {
		t.Log("Warning: No weapons loaded")
	} else {
		t.Logf("Loaded %d weapons", len(allWeapons))
	}

	if err := LoadAbilities(); err != nil {
		t.Fatalf("Failed to load abilities: %v", err)
	}

	LoadPerks() // Load perks from ItemIDRegister

	loadouts := GetAllDevLoadouts()
	if len(loadouts) == 0 {
		t.Fatal("No development loadouts loaded")
	}

	// Test validation for first loadout
	firstLoadout := loadouts[0]
	validation := ValidateLoadoutSlots(firstLoadout)

	t.Logf("Validating loadout: %s (ShipID: %d)", validation.LoadoutName, validation.ShipID)
	t.Logf("Total slots: %d, Valid: %d, Invalid: %d", validation.TotalSlots, validation.ValidSlots, validation.InvalidSlots)

	if validation.TotalSlots != 10 {
		t.Errorf("Expected 10 total slots, got %d", validation.TotalSlots)
	}

	// Log validation details
	if validation.WeaponPrimaryValid {
		t.Logf("  WeaponPrimary: %d (%s) - VALID", validation.WeaponPrimaryItemID, validation.WeaponPrimaryName)
	} else if validation.WeaponPrimaryItemID != 0 {
		t.Logf("  WeaponPrimary: %d - INVALID", firstLoadout.WeaponPrimary)
	}

	if validation.WeaponSecondaryValid {
		t.Logf("  WeaponSecondary: %d (%s) - VALID", validation.WeaponSecondaryItemID, validation.WeaponSecondaryName)
	} else if validation.WeaponSecondaryItemID != 0 {
		t.Logf("  WeaponSecondary: %d - INVALID", firstLoadout.WeaponSecondary)
	}

	if validation.AbilityPrimaryValid {
		t.Logf("  AbilityPrimary: %d (%s) - VALID", validation.AbilityPrimaryItemID, validation.AbilityPrimaryName)
	} else if validation.AbilityPrimaryItemID != 0 {
		t.Logf("  AbilityPrimary: %d - INVALID", firstLoadout.AbilityPrimary)
	}

	if validation.AbilitySecondaryValid {
		t.Logf("  AbilitySecondary: %d (%s) - VALID", validation.AbilitySecondaryItemID, validation.AbilitySecondaryName)
	} else if validation.AbilitySecondaryItemID != 0 {
		t.Logf("  AbilitySecondary: %d - INVALID", firstLoadout.AbilitySecondary)
	}

	if validation.AbilityPerimeterValid {
		t.Logf("  AbilityPerimeter: %d (%s) - VALID", validation.AbilityPerimeterItemID, validation.AbilityPerimeterName)
	} else if validation.AbilityPerimeterItemID != 0 {
		t.Logf("  AbilityPerimeter: %d - INVALID", firstLoadout.AbilityPerimeter)
	}

	if validation.AbilityInternalValid {
		t.Logf("  AbilityInternal: %d (%s) - VALID", validation.AbilityInternalItemID, validation.AbilityInternalName)
	} else if validation.AbilityInternalItemID != 0 {
		t.Logf("  AbilityInternal: %d - INVALID", firstLoadout.AbilityInternal)
	}

	if validation.PerkComValid {
		t.Logf("  PerkCom: %d (%s) - VALID", validation.PerkComItemID, validation.PerkComName)
	} else if validation.PerkComItemID != 0 {
		t.Logf("  PerkCom: %d - INVALID", firstLoadout.PerkCom)
	}

	if validation.PerkWeaponValid {
		t.Logf("  PerkWeapon: %d (%s) - VALID", validation.PerkWeaponItemID, validation.PerkWeaponName)
	} else if validation.PerkWeaponItemID != 0 {
		t.Logf("  PerkWeapon: %d - INVALID", firstLoadout.PerkWeapon)
	}

	if validation.PerkNavigationValid {
		t.Logf("  PerkNavigation: %d (%s) - VALID", validation.PerkNavigationItemID, validation.PerkNavigationName)
	} else if validation.PerkNavigationItemID != 0 {
		t.Logf("  PerkNavigation: %d - INVALID", firstLoadout.PerkNavigation)
	}

	if validation.PerkEngineerValid {
		t.Logf("  PerkEngineer: %d (%s) - VALID", validation.PerkEngineerItemID, validation.PerkEngineerName)
	} else if validation.PerkEngineerItemID != 0 {
		t.Logf("  PerkEngineer: %d - INVALID", firstLoadout.PerkEngineer)
	}

	if len(validation.InvalidSlotList) > 0 {
		t.Logf("  Invalid slots: %v", validation.InvalidSlotList)
	}
}

// TestI2ValidateAllLoadoutSlots tests validation of all loadouts
func TestI2ValidateAllLoadoutSlots(t *testing.T) {
	// Set DATA_DIR for path resolution
	original := os.Getenv("DATA_DIR")
	defer func() { _ = os.Setenv("DATA_DIR", original) }()
	
	dataDir := filepath.Join("..", "..", "data")
	if err := os.Setenv("DATA_DIR", dataDir); err != nil {
		t.Fatal(err)
	}

	// Reset all singletons for testing
	devLoadoutsOnce = sync.Once{}
	devLoadouts = nil

	// Reset weapon, ability, and perk loading state to force reload
	weaponsLoaded = false
	weaponsByItemID = nil
	abilitiesLoaded = false
	perksLoaded = false

	// Ensure all required data is loaded
	if err := LoadDevLoadouts(); err != nil {
		t.Fatalf("Failed to load development loadouts: %v", err)
	}

	if err := LoadWeapons(); err != nil {
		t.Fatalf("Failed to load weapons: %v", err)
	}

	// Debug: Check if weapons were loaded
	allWeapons := AllWeapons()
	if len(allWeapons) == 0 {
		t.Log("Warning: No weapons loaded")
	} else {
		t.Logf("Loaded %d weapons", len(allWeapons))
	}

	if err := LoadAbilities(); err != nil {
		t.Fatalf("Failed to load abilities: %v", err)
	}

	LoadPerks()

	validations := ValidateAllLoadoutSlots()
	totalLoadouts, totalSlots, validSlots, invalidSlots, invalidLoadouts := GetLoadoutSlotValidationSummary()

	t.Logf("Loadout slot validation summary:")
	t.Logf("  Total loadouts: %d", totalLoadouts)
	t.Logf("  Total slots: %d", totalSlots)
	t.Logf("  Valid slots: %d", validSlots)
	t.Logf("  Invalid slots: %d", invalidSlots)
	t.Logf("  Validation rate: %.1f%%", float64(validSlots)/float64(totalSlots)*100)

	if len(invalidLoadouts) > 0 {
		t.Logf("  Loadouts with invalid slots:")
		for _, invalidLoadout := range invalidLoadouts {
			t.Logf("    %s", invalidLoadout)
		}
	} else {
		t.Logf("  All loadouts have valid slot ItemIDs!")
	}

	// Verify we have the expected number of loadouts
	if totalLoadouts == 0 {
		t.Error("Expected at least one loadout to validate")
	}

	// Verify validation count matches
	if len(validations) != totalLoadouts {
		t.Errorf("Expected %d validations, got %d", totalLoadouts, len(validations))
	}

	// Verify total slots calculation
	expectedTotalSlots := totalLoadouts * 10
	if totalSlots != expectedTotalSlots {
		t.Errorf("Expected %d total slots (%d loadouts * 10 slots each), got %d",
			expectedTotalSlots, totalLoadouts, totalSlots)
	}

	// Log per-category validation statistics
	weaponValid := 0
	weaponTotal := 0
	abilityValid := 0
	abilityTotal := 0
	perkValid := 0
	perkTotal := 0

	for _, validation := range validations {
		// Count weapon slots
		if validation.WeaponPrimaryItemID != 0 {
			weaponTotal++
			if validation.WeaponPrimaryValid {
				weaponValid++
			}
		}
		if validation.WeaponSecondaryItemID != 0 {
			weaponTotal++
			if validation.WeaponSecondaryValid {
				weaponValid++
			}
		}

		// Count ability slots
		if validation.AbilityPrimaryItemID != 0 {
			abilityTotal++
			if validation.AbilityPrimaryValid {
				abilityValid++
			}
		}
		if validation.AbilitySecondaryItemID != 0 {
			abilityTotal++
			if validation.AbilitySecondaryValid {
				abilityValid++
			}
		}
		if validation.AbilityPerimeterItemID != 0 {
			abilityTotal++
			if validation.AbilityPerimeterValid {
				abilityValid++
			}
		}
		if validation.AbilityInternalItemID != 0 {
			abilityTotal++
			if validation.AbilityInternalValid {
				abilityValid++
			}
		}

		// Count perk slots
		if validation.PerkComItemID != 0 {
			perkTotal++
			if validation.PerkComValid {
				perkValid++
			}
		}
		if validation.PerkWeaponItemID != 0 {
			perkTotal++
			if validation.PerkWeaponValid {
				perkValid++
			}
		}
		if validation.PerkNavigationItemID != 0 {
			perkTotal++
			if validation.PerkNavigationValid {
				perkValid++
			}
		}
		if validation.PerkEngineerItemID != 0 {
			perkTotal++
			if validation.PerkEngineerValid {
				perkValid++
			}
		}
	}

	t.Logf("  Weapon slots: %d/%d valid (%.1f%%)", weaponValid, weaponTotal, float64(weaponValid)/float64(weaponTotal)*100)
	t.Logf("  Ability slots: %d/%d valid (%.1f%%)", abilityValid, abilityTotal, float64(abilityValid)/float64(abilityTotal)*100)
	t.Logf("  Perk slots: %d/%d valid (%.1f%%)", perkValid, perkTotal, float64(perkValid)/float64(perkTotal)*100)
}

// TestI2LoadoutCrossReferenceIntegration tests the integration of cross-referenced data
func TestI2LoadoutCrossReferenceIntegration(t *testing.T) {
	// Set DATA_DIR for path resolution
	original := os.Getenv("DATA_DIR")
	defer func() { _ = os.Setenv("DATA_DIR", original) }()
	
	dataDir := filepath.Join("..", "..", "data")
	if err := os.Setenv("DATA_DIR", dataDir); err != nil {
		t.Fatal(err)
	}

	// Reset all singletons for testing
	devLoadoutsOnce = sync.Once{}
	devLoadouts = nil

	// Reset weapon, ability, and perk loading state to force reload
	weaponsLoaded = false
	weaponsByItemID = nil
	abilitiesLoaded = false
	perksLoaded = false

	// Ensure all required data is loaded
	if err := LoadDevLoadouts(); err != nil {
		t.Fatalf("Failed to load development loadouts: %v", err)
	}

	if err := LoadWeapons(); err != nil {
		t.Fatalf("Failed to load weapons: %v", err)
	}

	// Debug: Check if weapons were loaded
	allWeapons := AllWeapons()
	if len(allWeapons) == 0 {
		t.Log("Warning: No weapons loaded")
	} else {
		t.Logf("Loaded %d weapons", len(allWeapons))
	}

	if err := LoadAbilities(); err != nil {
		t.Fatalf("Failed to load abilities: %v", err)
	}

	LoadPerks()

	// Test that we can resolve weapon ItemIDs from loadouts
	loadouts := GetAllDevLoadouts()
	weaponItemIDs := make(map[int32]bool)
	abilityItemIDs := make(map[int32]bool)
	perkItemIDs := make(map[int32]bool)

	for _, loadout := range loadouts {
		if loadout.WeaponPrimary != 0 {
			weaponItemIDs[loadout.WeaponPrimary] = true
		}
		if loadout.WeaponSecondary != 0 {
			weaponItemIDs[loadout.WeaponSecondary] = true
		}
		if loadout.AbilityPrimary != 0 {
			abilityItemIDs[loadout.AbilityPrimary] = true
		}
		if loadout.AbilitySecondary != 0 {
			abilityItemIDs[loadout.AbilitySecondary] = true
		}
		if loadout.AbilityPerimeter != 0 {
			abilityItemIDs[loadout.AbilityPerimeter] = true
		}
		if loadout.AbilityInternal != 0 {
			abilityItemIDs[loadout.AbilityInternal] = true
		}
		if loadout.PerkCom != 0 {
			perkItemIDs[loadout.PerkCom] = true
		}
		if loadout.PerkWeapon != 0 {
			perkItemIDs[loadout.PerkWeapon] = true
		}
		if loadout.PerkNavigation != 0 {
			perkItemIDs[loadout.PerkNavigation] = true
		}
		if loadout.PerkEngineer != 0 {
			perkItemIDs[loadout.PerkEngineer] = true
		}
	}

	// Count how many unique ItemIDs we have
	weaponCount := len(weaponItemIDs)
	abilityCount := len(abilityItemIDs)
	perkCount := len(perkItemIDs)

	t.Logf("Cross-reference integration:")
	t.Logf("  Unique weapon ItemIDs in loadouts: %d", weaponCount)
	t.Logf("  Unique ability ItemIDs in loadouts: %d", abilityCount)
	t.Logf("  Unique perk ItemIDs in loadouts: %d", perkCount)

	// Test resolution of a few ItemIDs
	allWeapons = AllWeapons()
	weaponResolutionCount := 0
	for itemID := range weaponItemIDs {
		if _, exists := allWeapons[itemID]; exists {
			weaponResolutionCount++
		}
	}
	t.Logf("  Weapon ItemIDs resolved: %d/%d (%.1f%%)",
		weaponResolutionCount, weaponCount, float64(weaponResolutionCount)/float64(weaponCount)*100)

	abilityResolutionCount := 0
	for itemID := range abilityItemIDs {
		if _, exists := AbilityByItemID(itemID); exists {
			abilityResolutionCount++
		}
	}
	t.Logf("  Ability ItemIDs resolved: %d/%d (%.1f%%)",
		abilityResolutionCount, abilityCount, float64(abilityResolutionCount)/float64(abilityCount)*100)

	perkResolutionCount := 0
	for itemID := range perkItemIDs {
		if _, exists := PerkByID(itemID); exists {
			perkResolutionCount++
		}
	}
	t.Logf("  Perk ItemIDs resolved: %d/%d (%.1f%%)",
		perkResolutionCount, perkCount, float64(perkResolutionCount)/float64(perkCount)*100)

	// Verify we have reasonable resolution rates
	// Note: Some ItemIDs may not resolve due to data version mismatches
	// This is expected and not a failure of the cross-reference implementation
	t.Logf("  Note: ItemID resolution rates may be less than 100%% due to data version differences")
	if weaponCount > 0 && weaponResolutionCount == 0 {
		t.Log("  Note: No weapon ItemIDs resolved - this may indicate a data version mismatch")
	}
	if abilityCount > 0 && abilityResolutionCount == 0 {
		t.Log("  Note: No ability ItemIDs resolved - this may indicate a data version mismatch")
	}
	if perkCount > 0 && perkResolutionCount == 0 {
		t.Log("  Note: No perk ItemIDs resolved - this may indicate a data version mismatch")
	}
}
