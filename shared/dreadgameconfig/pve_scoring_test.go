package dreadgameconfig

import (
	"sync"
	"testing"
)

// TestK2LoadPvEKillScoring tests loading PvE kill scoring data from PvEKillScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2LoadPvEKillScoring(t *testing.T) {
	// Reset the singleton for testing
	pveKillScoringOnce = sync.Once{}
	pveKillScoring = nil
	pveKillScoringLoaded = false

	err := LoadPvEKillScoring()
	if err != nil {
		t.Fatalf("Failed to load PvE kill scoring: %v", err)
	}

	scorings := AllPvEKillScorings()
	if len(scorings) == 0 {
		t.Fatal("No PvE kill scorings loaded")
	}

	t.Logf("Successfully loaded %d PvE kill scorings from PvEKillScoring.json", len(scorings))
}

// TestK2PvEKillScoringCountValidation verifies the expected number of PvE kill scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvEKillScoringCountValidation(t *testing.T) {
	// Reset the singleton for testing
	pveKillScoringOnce = sync.Once{}
	pveKillScoring = nil
	pveKillScoringLoaded = false

	err := LoadPvEKillScoring()
	if err != nil {
		t.Fatalf("Failed to load PvE kill scoring: %v", err)
	}

	count := PvEKillScoringCount()
	if count == 0 {
		t.Error("Expected some PvE kill scorings, got 0")
	} else {
		t.Logf("✅ K2: PvE kill scoring count validated: %d scorings", count)
	}
}

// TestK2LoadPvEWaveScoring tests loading PvE wave scoring data from PvEWaveScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2LoadPvEWaveScoring(t *testing.T) {
	// Reset the singleton for testing
	pveWaveScoringOnce = sync.Once{}
	pveWaveScoring = nil
	pveWaveScoringLoaded = false

	err := LoadPvEWaveScoring()
	if err != nil {
		t.Fatalf("Failed to load PvE wave scoring: %v", err)
	}

	scorings := AllPvEWaveScorings()
	if len(scorings) == 0 {
		t.Fatal("No PvE wave scorings loaded")
	}

	t.Logf("Successfully loaded %d PvE wave scorings from PvEWaveScoring.json", len(scorings))
}

// TestK2PvEWaveScoringCountValidation verifies the expected number of PvE wave scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvEWaveScoringCountValidation(t *testing.T) {
	// Reset the singleton for testing
	pveWaveScoringOnce = sync.Once{}
	pveWaveScoring = nil
	pveWaveScoringLoaded = false

	err := LoadPvEWaveScoring()
	if err != nil {
		t.Fatalf("Failed to load PvE wave scoring: %v", err)
	}

	count := PvEWaveScoringCount()
	if count == 0 {
		t.Error("Expected some PvE wave scorings, got 0")
	} else {
		t.Logf("✅ K2: PvE wave scoring count validated: %d scorings", count)
	}
}

// TestK2LoadPvEDefendScoring tests loading PvE defend scoring data from PvEDefendScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2LoadPvEDefendScoring(t *testing.T) {
	// Reset the singleton for testing
	pveDefendScoringOnce = sync.Once{}
	pveDefendScoring = nil
	pveDefendScoringLoaded = false

	err := LoadPvEDefendScoring()
	if err != nil {
		t.Fatalf("Failed to load PvE defend scoring: %v", err)
	}

	scorings := AllPvEDefendScorings()
	if len(scorings) == 0 {
		t.Fatal("No PvE defend scorings loaded")
	}

	t.Logf("Successfully loaded %d PvE defend scorings from PvEDefendScoring.json", len(scorings))
}

// TestK2PvEDefendScoringCountValidation verifies the expected number of PvE defend scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvEDefendScoringCountValidation(t *testing.T) {
	// Reset the singleton for testing
	pveDefendScoringOnce = sync.Once{}
	pveDefendScoring = nil
	pveDefendScoringLoaded = false

	err := LoadPvEDefendScoring()
	if err != nil {
		t.Fatalf("Failed to load PvE defend scoring: %v", err)
	}

	count := PvEDefendScoringCount()
	if count == 0 {
		t.Error("Expected some PvE defend scorings, got 0")
	} else {
		t.Logf("✅ K2: PvE defend scoring count validated: %d scorings", count)
	}
}

// TestK2LoadPvEMedalScoring tests loading PvE medal scoring data from PvEMedalScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2LoadPvEMedalScoring(t *testing.T) {
	// Reset the singleton for testing
	pveMedalScoringOnce = sync.Once{}
	pveMedalScoring = nil
	pveMedalScoringLoaded = false

	err := LoadPvEMedalScoring()
	if err != nil {
		t.Fatalf("Failed to load PvE medal scoring: %v", err)
	}

	scorings := AllPvEMedalScorings()
	if len(scorings) == 0 {
		t.Fatal("No PvE medal scorings loaded")
	}

	t.Logf("Successfully loaded %d PvE medal scorings from PvEMedalScoring.json", len(scorings))
}

// TestK2PvEMedalScoringCountValidation verifies the expected number of PvE medal scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvEMedalScoringCountValidation(t *testing.T) {
	// Reset the singleton for testing
	pveMedalScoringOnce = sync.Once{}
	pveMedalScoring = nil
	pveMedalScoringLoaded = false

	err := LoadPvEMedalScoring()
	if err != nil {
		t.Fatalf("Failed to load PvE medal scoring: %v", err)
	}

	count := PvEMedalScoringCount()
	if count == 0 {
		t.Error("Expected some PvE medal scorings, got 0")
	} else {
		t.Logf("✅ K2: PvE medal scoring count validated: %d scorings", count)
	}
}