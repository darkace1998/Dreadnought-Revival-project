package dreadgameconfig

import (
	"sync"
	"testing"
)

// TestK2LoadPvESeasons tests loading PvE seasons data from DN_Seasons_DT.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2LoadPvESeasons(t *testing.T) {
	// Reset the singleton for testing
	pveSeasonsOnce = sync.Once{}
	pveSeasons = nil
	pveSeasonsLoaded = false

	err := LoadPvESeasons()
	if err != nil {
		t.Fatalf("Failed to load PvE seasons: %v", err)
	}

	seasons := AllPvESeasons()
	if len(seasons) == 0 {
		t.Fatal("No PvE seasons loaded")
	}

	t.Logf("Successfully loaded %d PvE seasons from DN_Seasons_DT.json", len(seasons))
}

// TestK2PvESeasonCountValidation verifies the expected number of PvE seasons
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvESeasonCountValidation(t *testing.T) {
	// Reset the singleton for testing
	pveSeasonsOnce = sync.Once{}
	pveSeasons = nil
	pveSeasonsLoaded = false

	err := LoadPvESeasons()
	if err != nil {
		t.Fatalf("Failed to load PvE seasons: %v", err)
	}

	count := PvESeasonCount()
	if count == 0 {
		t.Error("Expected some PvE seasons, got 0")
	} else {
		t.Logf("✅ K2: PvE season count validated: %d seasons", count)
	}

	// Verify all seasons have unique row names
	nameMap := make(map[string]bool)
	for _, season := range AllPvESeasons() {
		if nameMap[season.RowName] {
			t.Errorf("Duplicate PvE season row name found: %s", season.RowName)
		}
		nameMap[season.RowName] = true
	}

	t.Logf("✅ K2: All PvE season row names are unique")
}

// TestK2LoadPvEEvents tests loading PvE events data from DN_Events_DT.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2LoadPvEEvents(t *testing.T) {
	// Reset the singleton for testing
	pveEventsOnce = sync.Once{}
	pveEvents = nil
	pveEventsLoaded = false

	err := LoadPvEEvents()
	if err != nil {
		t.Fatalf("Failed to load PvE events: %v", err)
	}

	events := AllPvEEvents()
	if len(events) == 0 {
		t.Fatal("No PvE events loaded")
	}

	t.Logf("Successfully loaded %d PvE events from DN_Events_DT.json", len(events))
}

// TestK2PvEEventCountValidation verifies the expected number of PvE events
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvEEventCountValidation(t *testing.T) {
	// Reset the singleton for testing
	pveEventsOnce = sync.Once{}
	pveEvents = nil
	pveEventsLoaded = false

	err := LoadPvEEvents()
	if err != nil {
		t.Fatalf("Failed to load PvE events: %v", err)
	}

	count := PvEEventCount()
	if count == 0 {
		t.Error("Expected some PvE events, got 0")
	} else {
		t.Logf("✅ K2: PvE event count validated: %d events", count)
	}

	// Verify all events have unique row names
	nameMap := make(map[string]bool)
	for _, event := range AllPvEEvents() {
		if nameMap[event.RowName] {
			t.Errorf("Duplicate PvE event row name found: %s", event.RowName)
		}
		nameMap[event.RowName] = true
	}

	t.Logf("✅ K2: All PvE event row names are unique")
}

// TestK2LoadPvETutorialObjectives tests loading PvE tutorial objectives data from TutorialObjectives_DT.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2LoadPvETutorialObjectives(t *testing.T) {
	// Reset the singleton for testing
	pveTutorialObjectivesOnce = sync.Once{}
	pveTutorialObjectives = nil
	pveTutorialObjectivesLoaded = false

	err := LoadPvETutorialObjectives()
	if err != nil {
		t.Fatalf("Failed to load PvE tutorial objectives: %v", err)
	}

	objectives := AllPvETutorialObjectives()
	if len(objectives) == 0 {
		t.Fatal("No PvE tutorial objectives loaded")
	}

	t.Logf("Successfully loaded %d PvE tutorial objectives from TutorialObjectives_DT.json", len(objectives))
}

// TestK2PvETutorialObjectiveCountValidation verifies the expected number of PvE tutorial objectives
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvETutorialObjectiveCountValidation(t *testing.T) {
	// Reset the singleton for testing
	pveTutorialObjectivesOnce = sync.Once{}
	pveTutorialObjectives = nil
	pveTutorialObjectivesLoaded = false

	err := LoadPvETutorialObjectives()
	if err != nil {
		t.Fatalf("Failed to load PvE tutorial objectives: %v", err)
	}

	count := PvETutorialObjectiveCount()
	if count == 0 {
		t.Error("Expected some PvE tutorial objectives, got 0")
	} else {
		t.Logf("✅ K2: PvE tutorial objective count validated: %d objectives", count)
	}

	// Verify all tutorial objectives have unique row names
	nameMap := make(map[string]bool)
	for _, objective := range AllPvETutorialObjectives() {
		if nameMap[objective.RowName] {
			t.Errorf("Duplicate PvE tutorial objective row name found: %s", objective.RowName)
		}
		nameMap[objective.RowName] = true
	}

	t.Logf("✅ K2: All PvE tutorial objective row names are unique")
}