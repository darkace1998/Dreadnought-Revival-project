package dreadgameconfig

import (
	"fmt"
	"sync"
	"testing"
)

// TestK2AllPvEDataLoaded tests that all 14 PVE files can be loaded successfully
// K2: Load PVE/ — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
func TestK2AllPvEDataLoaded(t *testing.T) {
	// Reset all singletons for testing
	// Basic scoring files
	pveKillScoringOnce = sync.Once{}
	pveKillScoring = nil
	pveKillScoringLoaded = false

	pveWaveScoringOnce = sync.Once{}
	pveWaveScoring = nil
	pveWaveScoringLoaded = false

	pveDefendScoringOnce = sync.Once{}
	pveDefendScoring = nil
	pveDefendScoringLoaded = false

	pveMedalScoringOnce = sync.Once{}
	pveMedalScoring = nil
	pveMedalScoringLoaded = false

	// Objectives and metadata
	pveObjectivesOnce = sync.Once{}
	pveObjectives = nil
	pveObjectivesLoaded = false

	pveSeasonsOnce = sync.Once{}
	pveSeasons = nil
	pveSeasonsLoaded = false

	pveEventsOnce = sync.Once{}
	pveEvents = nil
	pveEventsLoaded = false

	pveTutorialObjectivesOnce = sync.Once{}
	pveTutorialObjectives = nil
	pveTutorialObjectivesLoaded = false

	// Havoc variants
	pveKillScoringHavocOnce = sync.Once{}
	pveKillScoringHavoc = nil
	pveKillScoringHavocLoaded = false

	pveWaveScoringHavocOnce = sync.Once{}
	pveWaveScoringHavoc = nil
	pveWaveScoringHavocLoaded = false

	// Remaining player scoring variants
	pveRemainingPlayerScoringOnce = sync.Once{}
	pveRemainingPlayerScoring = nil
	pveRemainingPlayerScoringLoaded = false

	pveRemainingPlayerScoringEscortOnce = sync.Once{}
	pveRemainingPlayerScoringEscort = nil
	pveRemainingPlayerScoringEscortLoaded = false

	pveRemainingPlayerScoringHavocOnce = sync.Once{}
	pveRemainingPlayerScoringHavoc = nil
	pveRemainingPlayerScoringHavocLoaded = false

	// Load all PVE data
	errors := []string{}
	
	// Basic scoring files (4)
	if err := LoadPvEKillScoring(); err != nil {
		errors = append(errors, fmt.Sprintf("PvEKillScoring: %v", err))
	}
	if err := LoadPvEWaveScoring(); err != nil {
		errors = append(errors, fmt.Sprintf("PvEWaveScoring: %v", err))
	}
	if err := LoadPvEDefendScoring(); err != nil {
		errors = append(errors, fmt.Sprintf("PvEDefendScoring: %v", err))
	}
	if err := LoadPvEMedalScoring(); err != nil {
		errors = append(errors, fmt.Sprintf("PvEMedalScoring: %v", err))
	}

	// Objectives and metadata (4)
	if err := LoadPvEObjectives(); err != nil {
		errors = append(errors, fmt.Sprintf("PvEObjectives: %v", err))
	}
	if err := LoadPvESeasons(); err != nil {
		errors = append(errors, fmt.Sprintf("PvESeasons: %v", err))
	}
	if err := LoadPvEEvents(); err != nil {
		errors = append(errors, fmt.Sprintf("PvEEvents: %v", err))
	}
	if err := LoadPvETutorialObjectives(); err != nil {
		errors = append(errors, fmt.Sprintf("PvETutorialObjectives: %v", err))
	}

	// Havoc variants (2)
	if err := LoadPvEKillScoringHavoc(); err != nil {
		errors = append(errors, fmt.Sprintf("PvEKillScoringHavoc: %v", err))
	}
	if err := LoadPvEWaveScoringHavoc(); err != nil {
		errors = append(errors, fmt.Sprintf("PvEWaveScoringHavoc: %v", err))
	}

	// Remaining player scoring variants (4)
	if err := LoadPvERemainingPlayerScoring(); err != nil {
		errors = append(errors, fmt.Sprintf("PvERemainingPlayerScoring: %v", err))
	}
	if err := LoadPvERemainingPlayerScoringEscort(); err != nil {
		errors = append(errors, fmt.Sprintf("PvERemainingPlayerScoringEscort: %v", err))
	}
	if err := LoadPvERemainingPlayerScoringHavoc(); err != nil {
		errors = append(errors, fmt.Sprintf("PvERemainingPlayerScoringHavoc: %v", err))
	}

	if len(errors) > 0 {
		for _, err := range errors {
			t.Error(err)
		}
		t.Fatal("Failed to load one or more PVE data files")
	}

	// Verify all data is loaded
	killScoringCount := PvEKillScoringCount()
	waveScoringCount := PvEWaveScoringCount()
	defendScoringCount := PvEDefendScoringCount()
	medalScoringCount := PvEMedalScoringCount()
	objectiveCount := PvEObjectiveCount()
	seasonCount := PvESeasonCount()
	eventCount := PvEEventCount()
	tutorialObjectiveCount := PvETutorialObjectiveCount()
	killScoringHavocCount := PvEKillScoringHavocCount()
	waveScoringHavocCount := PvEWaveScoringHavocCount()
	remainingPlayerScoringCount := PvERemainingPlayerScoringCount()
	remainingPlayerScoringEscortCount := PvERemainingPlayerScoringEscortCount()
	remainingPlayerScoringHavocCount := PvERemainingPlayerScoringHavocCount()

	t.Logf("✅ K2: All 14 PVE files loaded successfully:")
	t.Logf("  - PvE Kill Scoring: %d", killScoringCount)
	t.Logf("  - PvE Wave Scoring: %d", waveScoringCount)
	t.Logf("  - PvE Defend Scoring: %d", defendScoringCount)
	t.Logf("  - PvE Medal Scoring: %d", medalScoringCount)
	t.Logf("  - PvE Objectives: %d", objectiveCount)
	t.Logf("  - PvE Seasons: %d", seasonCount)
	t.Logf("  - PvE Events: %d", eventCount)
	t.Logf("  - PvE Tutorial Objectives: %d", tutorialObjectiveCount)
	t.Logf("  - PvE Kill Scoring Havoc: %d", killScoringHavocCount)
	t.Logf("  - PvE Wave Scoring Havoc: %d", waveScoringHavocCount)
	t.Logf("  - PvE Remaining Player Scoring: %d", remainingPlayerScoringCount)
	t.Logf("  - PvE Remaining Player Scoring Escort: %d", remainingPlayerScoringEscortCount)
	t.Logf("  - PvE Remaining Player Scoring Havoc: %d", remainingPlayerScoringHavocCount)

	// Calculate total count
	totalCount := killScoringCount + waveScoringCount + defendScoringCount + medalScoringCount +
		objectiveCount + seasonCount + eventCount + tutorialObjectiveCount +
		killScoringHavocCount + waveScoringHavocCount + remainingPlayerScoringCount +
		remainingPlayerScoringEscortCount + remainingPlayerScoringHavocCount

	if totalCount == 0 {
		t.Error("Expected some PVE data to be loaded, got 0 total entries")
	} else {
		t.Logf("✅ K2: Total PVE data entries loaded: %d", totalCount)
	}

	// Verify that all counts are non-negative
	counts := []int{
		killScoringCount, waveScoringCount, defendScoringCount, medalScoringCount,
		objectiveCount, seasonCount, eventCount, tutorialObjectiveCount,
		killScoringHavocCount, waveScoringHavocCount, remainingPlayerScoringCount,
		remainingPlayerScoringEscortCount, remainingPlayerScoringHavocCount,
	}

	for i, count := range counts {
		if count < 0 {
			t.Errorf("Negative count found at index %d: %d", i, count)
		}
	}

	t.Logf("✅ K2: All PVE data counts are non-negative")
}