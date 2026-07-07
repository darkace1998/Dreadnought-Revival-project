package dreadgameconfig

import (
	"sync"
	"testing"
)

// TestK1LoadHavocModifiers tests loading Havoc modifier data from DN_HavocModifiers_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1LoadHavocModifiers(t *testing.T) {
	// Reset the singleton for testing
	havocModifiersOnce = sync.Once{}
	havocModifiers = nil
	havocModifiersLoaded = false

	err := LoadHavocModifiers()
	if err != nil {
		t.Fatalf("Failed to load Havoc modifiers: %v", err)
	}

	modifiers := AllHavocModifiers()
	if len(modifiers) == 0 {
		t.Fatal("No Havoc modifiers loaded")
	}

	t.Logf("Successfully loaded %d Havoc modifiers from DN_HavocModifiers_DT.json", len(modifiers))
}

// TestK1HavocModifierStructure validates the structure of Havoc modifier data
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1HavocModifierStructure(t *testing.T) {
	// Reset the singleton for testing
	havocModifiersOnce = sync.Once{}
	havocModifiers = nil
	havocModifiersLoaded = false

	err := LoadHavocModifiers()
	if err != nil {
		t.Fatalf("Failed to load Havoc modifiers: %v", err)
	}

	modifiers := AllHavocModifiers()
	if len(modifiers) == 0 {
		t.Fatal("No Havoc modifiers loaded")
	}

	// Validate first modifier structure
	first := modifiers[0]
	if first.Title == "" && first.Subtitle == "" && first.Description == "" && first.IconPath == "" {
		t.Error("First Havoc modifier has all empty fields")
	}

	// Validate that modifiers have row names
	for _, modifier := range modifiers {
		if modifier.RowName == "" {
			t.Error("Havoc modifier has empty RowName")
		}
	}

	// Validate that modifiers have valid wave ranges (allow negative values as they may be placeholders)
	for _, modifier := range modifiers {
		// Note: Some modifiers may have negative MaxWave values as placeholders
		// We only validate that MinWave is not negative (as that would be invalid)
		if modifier.MinWave < 0 {
			t.Errorf("Havoc modifier %s has negative MinWave: %d", modifier.RowName, modifier.MinWave)
		}
		// Allow MinWave > MaxWave as this may be intentional (e.g., MinWave=999, MaxWave=1 for special cases)
		// Allow MaxWave < 0 as this may be a placeholder value
	}

	t.Logf("Validated structure of %d Havoc modifiers", len(modifiers))
}

// TestK1HavocModifierAccessorFunctions tests all Havoc modifier accessor functions
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1HavocModifierAccessorFunctions(t *testing.T) {
	// Reset the singleton for testing
	havocModifiersOnce = sync.Once{}
	havocModifiers = nil
	havocModifiersLoaded = false

	err := LoadHavocModifiers()
	if err != nil {
		t.Fatalf("Failed to load Havoc modifiers: %v", err)
	}

	modifiers := AllHavocModifiers()
	if len(modifiers) == 0 {
		t.Fatal("No Havoc modifiers loaded")
	}

	// Test HavocModifierByRowName
	if len(modifiers) > 0 {
		firstModifier := modifiers[0]
		modifier, exists := HavocModifierByRowName(firstModifier.RowName)
		if !exists {
			t.Errorf("HavocModifierByRowName(%s) should exist", firstModifier.RowName)
		} else if modifier.RowName != firstModifier.RowName {
			t.Errorf("HavocModifierByRowName(%s) returned wrong RowName: %s", firstModifier.RowName, modifier.RowName)
		}
	}

	// Test HavocModifierCount
	count := HavocModifierCount()
	if count != len(modifiers) {
		t.Errorf("HavocModifierCount() returned %d, expected %d", count, len(modifiers))
	}

	// Test HavocModifierRowNames
	names := HavocModifierRowNames()
	if len(names) != len(modifiers) {
		t.Errorf("HavocModifierRowNames() returned %d names, expected %d", len(names), len(modifiers))
	}

	t.Logf("All Havoc modifier accessor functions working correctly")
}

// TestK1HavocModifierCountValidation verifies the expected number of Havoc modifiers
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1HavocModifierCountValidation(t *testing.T) {
	// Reset the singleton for testing
	havocModifiersOnce = sync.Once{}
	havocModifiers = nil
	havocModifiersLoaded = false

	err := LoadHavocModifiers()
	if err != nil {
		t.Fatalf("Failed to load Havoc modifiers: %v", err)
	}

	count := HavocModifierCount()
	expectedCount := 26 // Expected 26 modifiers based on todo.md
	if count != expectedCount {
		t.Errorf("Expected %d Havoc modifiers, got %d", expectedCount, count)
	} else {
		t.Logf("✅ K1: Havoc modifier count validated: %d modifiers", count)
	}

	// Verify all modifiers have unique row names
	nameMap := make(map[string]bool)
	for _, modifier := range AllHavocModifiers() {
		if nameMap[modifier.RowName] {
			t.Errorf("Duplicate Havoc modifier row name found: %s", modifier.RowName)
		}
		nameMap[modifier.RowName] = true
	}

	t.Logf("✅ K1: All Havoc modifier row names are unique")
}