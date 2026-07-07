package dreadgameconfig

import (
	"sync"
	"testing"
)

// TestK1LoadHavocBoosts tests loading Havoc boost data from DN_HavocBoosts_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1LoadHavocBoosts(t *testing.T) {
	// Reset the singleton for testing
	havocBoostsOnce = sync.Once{}
	havocBoosts = nil
	havocBoostsLoaded = false

	err := LoadHavocBoosts()
	if err != nil {
		t.Fatalf("Failed to load Havoc boosts: %v", err)
	}

	boosts := AllHavocBoosts()
	if len(boosts) == 0 {
		t.Fatal("No Havoc boosts loaded")
	}

	t.Logf("Successfully loaded %d Havoc boosts from DN_HavocBoosts_DT.json", len(boosts))
}

// TestK1HavocBoostStructure validates the structure of Havoc boost data
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1HavocBoostStructure(t *testing.T) {
	// Reset the singleton for testing
	havocBoostsOnce = sync.Once{}
	havocBoosts = nil
	havocBoostsLoaded = false

	err := LoadHavocBoosts()
	if err != nil {
		t.Fatalf("Failed to load Havoc boosts: %v", err)
	}

	boosts := AllHavocBoosts()
	if len(boosts) == 0 {
		t.Fatal("No Havoc boosts loaded")
	}

	// Validate first boost structure
	first := boosts[0]
	if first.Title == "" && first.Description == "" && first.Feats == "" && first.IconPath == "" {
		t.Error("First Havoc boost has all empty fields")
	}

	// Validate that boosts have row names
	for _, boost := range boosts {
		if boost.RowName == "" {
			t.Error("Havoc boost has empty RowName")
		}
	}

	// Validate that boosts have non-negative weights and costs
	for _, boost := range boosts {
		if boost.Weight < 0 {
			t.Errorf("Havoc boost %s has negative weight: %d", boost.RowName, boost.Weight)
		}
		if boost.Cost < 0 {
			t.Errorf("Havoc boost %s has negative cost: %d", boost.RowName, boost.Cost)
		}
	}

	t.Logf("Validated structure of %d Havoc boosts", len(boosts))
}

// TestK1HavocBoostAccessorFunctions tests all Havoc boost accessor functions
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1HavocBoostAccessorFunctions(t *testing.T) {
	// Reset the singleton for testing
	havocBoostsOnce = sync.Once{}
	havocBoosts = nil
	havocBoostsLoaded = false

	err := LoadHavocBoosts()
	if err != nil {
		t.Fatalf("Failed to load Havoc boosts: %v", err)
	}

	boosts := AllHavocBoosts()
	if len(boosts) == 0 {
		t.Fatal("No Havoc boosts loaded")
	}

	// Test HavocBoostByRowName
	if len(boosts) > 0 {
		firstBoost := boosts[0]
		boost, exists := HavocBoostByRowName(firstBoost.RowName)
		if !exists {
			t.Errorf("HavocBoostByRowName(%s) should exist", firstBoost.RowName)
		} else if boost.RowName != firstBoost.RowName {
			t.Errorf("HavocBoostByRowName(%s) returned wrong RowName: %s", firstBoost.RowName, boost.RowName)
		}
	}

	// Test HavocBoostCount
	count := HavocBoostCount()
	if count != len(boosts) {
		t.Errorf("HavocBoostCount() returned %d, expected %d", count, len(boosts))
	}

	// Test HavocBoostRowNames
	names := HavocBoostRowNames()
	if len(names) != len(boosts) {
		t.Errorf("HavocBoostRowNames() returned %d names, expected %d", len(names), len(boosts))
	}

	t.Logf("All Havoc boost accessor functions working correctly")
}

// TestK1HavocBoostCountValidation verifies the expected number of Havoc boosts
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1HavocBoostCountValidation(t *testing.T) {
	// Reset the singleton for testing
	havocBoostsOnce = sync.Once{}
	havocBoosts = nil
	havocBoostsLoaded = false

	err := LoadHavocBoosts()
	if err != nil {
		t.Fatalf("Failed to load Havoc boosts: %v", err)
	}

	count := HavocBoostCount()
	expectedCount := 38 // Expected 38 boosts based on todo.md
	if count != expectedCount {
		t.Errorf("Expected %d Havoc boosts, got %d", expectedCount, count)
	} else {
		t.Logf("✅ K1: Havoc boost count validated: %d boosts", count)
	}

	// Verify all boosts have unique row names
	nameMap := make(map[string]bool)
	for _, boost := range AllHavocBoosts() {
		if nameMap[boost.RowName] {
			t.Errorf("Duplicate Havoc boost row name found: %s", boost.RowName)
		}
		nameMap[boost.RowName] = true
	}

	t.Logf("✅ K1: All Havoc boost row names are unique")
}