package dreadgameconfig

import (
	"sync"
	"testing"
)

// TestK2LoadPvEKillScoringHavoc tests loading PvE kill scoring Havoc data from PvEKillScoring_Havoc.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2LoadPvEKillScoringHavoc(t *testing.T) {
	// Reset the singleton for testing
	pveKillScoringHavocOnce = sync.Once{}
	pveKillScoringHavoc = nil
	pveKillScoringHavocLoaded = false

	err := LoadPvEKillScoringHavoc()
	if err != nil {
		t.Fatalf("Failed to load PvE kill scoring Havoc: %v", err)
	}

	scorings := AllPvEKillScoringsHavoc()
	if len(scorings) == 0 {
		t.Fatal("No PvE kill scorings Havoc loaded")
	}

	t.Logf("Successfully loaded %d PvE kill scorings Havoc from PvEKillScoring_Havoc.json", len(scorings))
}

// TestK2PvEKillScoringHavocCountValidation verifies the expected number of PvE kill scorings Havoc
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvEKillScoringHavocCountValidation(t *testing.T) {
	// Reset the singleton for testing
	pveKillScoringHavocOnce = sync.Once{}
	pveKillScoringHavoc = nil
	pveKillScoringHavocLoaded = false

	err := LoadPvEKillScoringHavoc()
	if err != nil {
		t.Fatalf("Failed to load PvE kill scoring Havoc: %v", err)
	}

	count := PvEKillScoringHavocCount()
	if count == 0 {
		t.Error("Expected some PvE kill scorings Havoc, got 0")
	} else {
		t.Logf("✅ K2: PvE kill scoring Havoc count validated: %d scorings", count)
	}
}

// TestK2LoadPvEWaveScoringHavoc tests loading PvE wave scoring Havoc data from PvEWaveScoring_Havoc.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2LoadPvEWaveScoringHavoc(t *testing.T) {
	// Reset the singleton for testing
	pveWaveScoringHavocOnce = sync.Once{}
	pveWaveScoringHavoc = nil
	pveWaveScoringHavocLoaded = false

	err := LoadPvEWaveScoringHavoc()
	if err != nil {
		t.Fatalf("Failed to load PvE wave scoring Havoc: %v", err)
	}

	scorings := AllPvEWaveScoringsHavoc()
	if len(scorings) == 0 {
		t.Fatal("No PvE wave scorings Havoc loaded")
	}

	t.Logf("Successfully loaded %d PvE wave scorings Havoc from PvEWaveScoring_Havoc.json", len(scorings))
}

// TestK2PvEWaveScoringHavocCountValidation verifies the expected number of PvE wave scorings Havoc
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvEWaveScoringHavocCountValidation(t *testing.T) {
	// Reset the singleton for testing
	pveWaveScoringHavocOnce = sync.Once{}
	pveWaveScoringHavoc = nil
	pveWaveScoringHavocLoaded = false

	err := LoadPvEWaveScoringHavoc()
	if err != nil {
		t.Fatalf("Failed to load PvE wave scoring Havoc: %v", err)
	}

	count := PvEWaveScoringHavocCount()
	if count == 0 {
		t.Error("Expected some PvE wave scorings Havoc, got 0")
	} else {
		t.Logf("✅ K2: PvE wave scoring Havoc count validated: %d scorings", count)
	}
}

// TestK2LoadPvERemainingPlayerScoring tests loading PvE remaining player scoring data from PvERemainingPlayerScoring.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2LoadPvERemainingPlayerScoring(t *testing.T) {
	// Reset the singleton for testing
	pveRemainingPlayerScoringOnce = sync.Once{}
	pveRemainingPlayerScoring = nil
	pveRemainingPlayerScoringLoaded = false

	err := LoadPvERemainingPlayerScoring()
	if err != nil {
		t.Fatalf("Failed to load PvE remaining player scoring: %v", err)
	}

	scorings := AllPvERemainingPlayerScorings()
	if len(scorings) == 0 {
		t.Fatal("No PvE remaining player scorings loaded")
	}

	t.Logf("Successfully loaded %d PvE remaining player scorings from PvERemainingPlayerScoring.json", len(scorings))
}

// TestK2PvERemainingPlayerScoringCountValidation verifies the expected number of PvE remaining player scorings
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvERemainingPlayerScoringCountValidation(t *testing.T) {
	// Reset the singleton for testing
	pveRemainingPlayerScoringOnce = sync.Once{}
	pveRemainingPlayerScoring = nil
	pveRemainingPlayerScoringLoaded = false

	err := LoadPvERemainingPlayerScoring()
	if err != nil {
		t.Fatalf("Failed to load PvE remaining player scoring: %v", err)
	}

	count := PvERemainingPlayerScoringCount()
	if count == 0 {
		t.Error("Expected some PvE remaining player scorings, got 0")
	} else {
		t.Logf("✅ K2: PvE remaining player scoring count validated: %d scorings", count)
	}
}

// TestK2LoadPvERemainingPlayerScoringEscort tests loading PvE remaining player scoring Escort data from PvERemainingPlayerScoring_Escort.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2LoadPvERemainingPlayerScoringEscort(t *testing.T) {
	// Reset the singleton for testing
	pveRemainingPlayerScoringEscortOnce = sync.Once{}
	pveRemainingPlayerScoringEscort = nil
	pveRemainingPlayerScoringEscortLoaded = false

	err := LoadPvERemainingPlayerScoringEscort()
	if err != nil {
		t.Fatalf("Failed to load PvE remaining player scoring Escort: %v", err)
	}

	scorings := AllPvERemainingPlayerScoringsEscort()
	if len(scorings) == 0 {
		t.Fatal("No PvE remaining player scorings Escort loaded")
	}

	t.Logf("Successfully loaded %d PvE remaining player scorings Escort from PvERemainingPlayerScoring_Escort.json", len(scorings))
}

// TestK2PvERemainingPlayerScoringEscortCountValidation verifies the expected number of PvE remaining player scorings Escort
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvERemainingPlayerScoringEscortCountValidation(t *testing.T) {
	// Reset the singleton for testing
	pveRemainingPlayerScoringEscortOnce = sync.Once{}
	pveRemainingPlayerScoringEscort = nil
	pveRemainingPlayerScoringEscortLoaded = false

	err := LoadPvERemainingPlayerScoringEscort()
	if err != nil {
		t.Fatalf("Failed to load PvE remaining player scoring Escort: %v", err)
	}

	count := PvERemainingPlayerScoringEscortCount()
	if count == 0 {
		t.Error("Expected some PvE remaining player scorings Escort, got 0")
	} else {
		t.Logf("✅ K2: PvE remaining player scoring Escort count validated: %d scorings", count)
	}
}

// TestK2LoadPvERemainingPlayerScoringHavoc tests loading PvE remaining player scoring Havoc data from vERemainingPlayerScoring_Havoc.json
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2LoadPvERemainingPlayerScoringHavoc(t *testing.T) {
	// Reset the singleton for testing
	pveRemainingPlayerScoringHavocOnce = sync.Once{}
	pveRemainingPlayerScoringHavoc = nil
	pveRemainingPlayerScoringHavocLoaded = false

	err := LoadPvERemainingPlayerScoringHavoc()
	if err != nil {
		t.Fatalf("Failed to load PvE remaining player scoring Havoc: %v", err)
	}

	scorings := AllPvERemainingPlayerScoringsHavoc()
	if len(scorings) == 0 {
		t.Fatal("No PvE remaining player scorings Havoc loaded")
	}

	t.Logf("Successfully loaded %d PvE remaining player scorings Havoc from vERemainingPlayerScoring_Havoc.json", len(scorings))
}

// TestK2PvERemainingPlayerScoringHavocCountValidation verifies the expected number of PvE remaining player scorings Havoc
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2PvERemainingPlayerScoringHavocCountValidation(t *testing.T) {
	// Reset the singleton for testing
	pveRemainingPlayerScoringHavocOnce = sync.Once{}
	pveRemainingPlayerScoringHavoc = nil
	pveRemainingPlayerScoringHavocLoaded = false

	err := LoadPvERemainingPlayerScoringHavoc()
	if err != nil {
		t.Fatalf("Failed to load PvE remaining player scoring Havoc: %v", err)
	}

	count := PvERemainingPlayerScoringHavocCount()
	if count == 0 {
		t.Error("Expected some PvE remaining player scorings Havoc, got 0")
	} else {
		t.Logf("✅ K2: PvE remaining player scoring Havoc count validated: %d scorings", count)
	}
}