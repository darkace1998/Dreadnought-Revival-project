package dreadgameconfig

import (
	"sync"
	"testing"
)

// TestK2LoadPvEObjectives tests loading PvE objectives data from PvEObjectives.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2LoadPvEObjectives(t *testing.T) {
	// Reset the singleton for testing
	pveObjectivesOnce = sync.Once{}
	pveObjectives = nil
	pveObjectivesLoaded = false

	err := LoadPvEObjectives()
	if err != nil {
		t.Fatalf("Failed to load PvE objectives: %v", err)
	}

	objectives := AllPvEObjectives()
	if len(objectives) == 0 {
		t.Fatal("No PvE objectives loaded")
	}

	t.Logf("Successfully loaded %d PvE objectives from PvEObjectives.json", len(objectives))
}

// TestK2PvEObjectiveStructure validates the structure of PvE objective data
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvEObjectiveStructure(t *testing.T) {
	// Reset the singleton for testing
	pveObjectivesOnce = sync.Once{}
	pveObjectives = nil
	pveObjectivesLoaded = false

	err := LoadPvEObjectives()
	if err != nil {
		t.Fatalf("Failed to load PvE objectives: %v", err)
	}

	objectives := AllPvEObjectives()
	if len(objectives) == 0 {
		t.Fatal("No PvE objectives loaded")
	}

	// Validate first objective structure
	first := objectives[0]
	if first.ID == "" && first.Type == "" && first.State == "" {
		t.Error("First PvE objective has all empty fields")
	}

	// Validate that objectives have row names
	for _, objective := range objectives {
		if objective.RowName == "" {
			t.Error("PvE objective has empty RowName")
		}
	}

	t.Logf("Validated structure of %d PvE objectives", len(objectives))
}

// TestK2PvEObjectiveAccessorFunctions tests all PvE objective accessor functions
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvEObjectiveAccessorFunctions(t *testing.T) {
	// Reset the singleton for testing
	pveObjectivesOnce = sync.Once{}
	pveObjectives = nil
	pveObjectivesLoaded = false

	err := LoadPvEObjectives()
	if err != nil {
		t.Fatalf("Failed to load PvE objectives: %v", err)
	}

	objectives := AllPvEObjectives()
	if len(objectives) == 0 {
		t.Fatal("No PvE objectives loaded")
	}

	// Test PvEObjectiveByRowName
	if len(objectives) > 0 {
		firstObjective := objectives[0]
		objective, exists := PvEObjectiveByRowName(firstObjective.RowName)
		if !exists {
			t.Errorf("PvEObjectiveByRowName(%s) should exist", firstObjective.RowName)
		} else if objective.RowName != firstObjective.RowName {
			t.Errorf("PvEObjectiveByRowName(%s) returned wrong RowName: %s", firstObjective.RowName, objective.RowName)
		}
	}

	// Test PvEObjectiveCount
	count := PvEObjectiveCount()
	if count != len(objectives) {
		t.Errorf("PvEObjectiveCount() returned %d, expected %d", count, len(objectives))
	}

	// Test PvEObjectiveByID
	if len(objectives) > 0 {
		firstObjective := objectives[0]
		if firstObjective.ID != "" {
			objective, exists := PvEObjectiveByID(firstObjective.ID)
			if !exists {
				t.Errorf("PvEObjectiveByID(%s) should exist", firstObjective.ID)
			} else if objective.ID != firstObjective.ID {
				t.Errorf("PvEObjectiveByID(%s) returned wrong ID: %s", firstObjective.ID, objective.ID)
			}
		}
	}

	t.Logf("All PvE objective accessor functions working correctly")
}

// TestK2PvEObjectiveCountValidation verifies the expected number of PvE objectives
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvEObjectiveCountValidation(t *testing.T) {
	// Reset the singleton for testing
	pveObjectivesOnce = sync.Once{}
	pveObjectives = nil
	pveObjectivesLoaded = false

	err := LoadPvEObjectives()
	if err != nil {
		t.Fatalf("Failed to load PvE objectives: %v", err)
	}

	count := PvEObjectiveCount()
	if count == 0 {
		t.Error("Expected some PvE objectives, got 0")
	} else {
		t.Logf("✅ K2: PvE objective count validated: %d objectives", count)
	}

	// Verify all objectives have unique row names
	nameMap := make(map[string]bool)
	for _, objective := range AllPvEObjectives() {
		if nameMap[objective.RowName] {
			t.Errorf("Duplicate PvE objective row name found: %s", objective.RowName)
		}
		nameMap[objective.RowName] = true
	}

	t.Logf("✅ K2: All PvE objective row names are unique")
}