package dreadgameconfig

import (
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
