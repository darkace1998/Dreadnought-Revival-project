package dreadgameconfig

import (
	"strings"
	"sync"
	"testing"
)

// TestJ3LoadPlayerMatchStatistics tests loading player match statistics from DN_PlayerMatchStatistics_DT.json
func TestJ3LoadPlayerMatchStatistics(t *testing.T) {
	// Reset the singleton for testing
	playerMatchStatsOnce = sync.Once{}
	playerMatchStats = nil
	playerMatchStatsLoaded = false

	err := LoadPlayerMatchStatistics()
	if err != nil {
		t.Fatalf("Failed to load player match statistics: %v", err)
	}

	count := PlayerMatchStatCount()
	if count == 0 {
		t.Error("Expected to load at least one player match statistic, got 0")
	}

	t.Logf("Successfully loaded %d player match statistics from DN_PlayerMatchStatistics_DT.json", count)
}

// TestJ3PlayerMatchStatStructure validates the structure of loaded player match statistics
func TestJ3PlayerMatchStatStructure(t *testing.T) {
	// Reset the singleton for testing
	playerMatchStatsOnce = sync.Once{}
	playerMatchStats = nil
	playerMatchStatsLoaded = false

	err := LoadPlayerMatchStatistics()
	if err != nil {
		t.Fatalf("Failed to load player match statistics: %v", err)
	}

	stats := AllPlayerMatchStatistics()
	if len(stats) == 0 {
		t.Fatal("No player match statistics loaded")
	}

	// Validate first stat structure
	first := stats[0]
	if first.CategoryID == "" {
		t.Error("First player match stat has empty CategoryID")
	}
	// Note: Name might be "[text]" placeholder, so we don't validate it
	if first.Priority == 0 {
		t.Error("First player match stat has zero Priority")
	}
	if first.ShortName == "" {
		t.Error("First player match stat has empty ShortName")
	}

	// Validate that all stats have non-empty CategoryID
	for i, stat := range stats {
		if stat.CategoryID == "" {
			t.Errorf("Player match stat %d has empty CategoryID", i)
		}
		if stat.ShortName == "" {
			t.Errorf("Player match stat %d has empty ShortName", i)
		}
	}

	t.Logf("Validated structure of %d player match statistics", len(stats))
}

// TestJ3PlayerMatchStatAccessorFunctions tests all player match stat accessor functions
func TestJ3PlayerMatchStatAccessorFunctions(t *testing.T) {
	// Reset the singleton for testing
	playerMatchStatsOnce = sync.Once{}
	playerMatchStats = nil
	playerMatchStatsLoaded = false

	err := LoadPlayerMatchStatistics()
	if err != nil {
		t.Fatalf("Failed to load player match statistics: %v", err)
	}

	stats := AllPlayerMatchStatistics()
	if len(stats) == 0 {
		t.Fatal("No player match statistics loaded")
	}

	// Test PlayerMatchStatByID
	if len(stats) > 0 {
		id := stats[0].CategoryID
		stat, exists := PlayerMatchStatByID(id)
		if !exists {
			t.Errorf("PlayerMatchStatByID(%s) should exist", id)
		} else if stat.CategoryID != id {
			t.Errorf("PlayerMatchStatByID(%s) returned wrong CategoryID: %s", id, stat.CategoryID)
		}
	}

	// Test PlayerMatchStatByShortName
	if len(stats) > 0 {
		name := stats[0].ShortName
		stat, exists := PlayerMatchStatByShortName(name)
		if !exists {
			t.Errorf("PlayerMatchStatByShortName(%s) should exist", name)
		} else if stat.ShortName != name {
			t.Errorf("PlayerMatchStatByShortName(%s) returned wrong ShortName: %s", name, stat.ShortName)
		}
	}

	// Test PlayerMatchStatCount
	count := PlayerMatchStatCount()
	if count != len(stats) {
		t.Errorf("PlayerMatchStatCount() returned %d, expected %d", count, len(stats))
	}

	// Test PlayerMatchStatPriorities
	priorities := PlayerMatchStatPriorities()
	if len(priorities) == 0 {
		t.Error("PlayerMatchStatPriorities() should return at least one priority")
	}
	t.Logf("Found %d unique priorities: %v", len(priorities), priorities)

	// Test PlayerMatchStatShortNames
	names := PlayerMatchStatShortNames()
	if len(names) != len(stats) {
		t.Errorf("PlayerMatchStatShortNames() returned %d names, expected %d", len(names), len(stats))
	}
	t.Logf("Found %d short names: %v", len(names), names)

	// Test HasPlayerMatchStat
	if len(names) > 0 {
		name := names[0]
		if !HasPlayerMatchStat(name) {
			t.Errorf("HasPlayerMatchStat(%s) should return true", name)
		}
		if HasPlayerMatchStat("NonExistentStat") {
			t.Error("HasPlayerMatchStat(\"NonExistentStat\") should return false")
		}
	}

	t.Logf("All player match stat accessor functions working correctly")
}

// TestJ3PlayerMatchStatCountValidation verifies the expected number of player match statistics
func TestJ3PlayerMatchStatCountValidation(t *testing.T) {
	// Reset the singleton for testing
	playerMatchStatsOnce = sync.Once{}
	playerMatchStats = nil
	playerMatchStatsLoaded = false

	err := LoadPlayerMatchStatistics()
	if err != nil {
		t.Fatalf("Failed to load player match statistics: %v", err)
	}

	count := PlayerMatchStatCount()
	expectedCount := 15 // Expected 15 stat categories based on the JSON file
	if count != expectedCount {
		t.Errorf("Expected %d player match statistics, got %d", expectedCount, count)
	} else {
		t.Logf("✅ J3: Player match stat count validated: %d statistics", count)
	}

	// Verify all stats have unique CategoryIDs
	idMap := make(map[string]bool)
	for _, stat := range AllPlayerMatchStatistics() {
		if idMap[stat.CategoryID] {
			t.Errorf("Duplicate player match stat CategoryID found: %s", stat.CategoryID)
		}
		idMap[stat.CategoryID] = true
	}

	// Verify all stats have unique ShortNames
	nameMap := make(map[string]bool)
	for _, stat := range AllPlayerMatchStatistics() {
		if nameMap[stat.ShortName] {
			t.Errorf("Duplicate player match stat ShortName found: %s", stat.ShortName)
		}
		nameMap[stat.ShortName] = true
	}

	t.Logf("✅ J3: All player match stat IDs and names are unique")
}

// TestJ3PlayerMatchStatShortNamesValidation validates short name extraction
func TestJ3PlayerMatchStatShortNamesValidation(t *testing.T) {
	// Reset the singleton for testing
	playerMatchStatsOnce = sync.Once{}
	playerMatchStats = nil
	playerMatchStatsLoaded = false

	err := LoadPlayerMatchStatistics()
	if err != nil {
		t.Fatalf("Failed to load player match statistics: %v", err)
	}

	stats := AllPlayerMatchStatistics()
	if len(stats) == 0 {
		t.Fatal("No player match statistics loaded")
	}

	// Test specific known short names
	expectedShortNames := []string{
		"Assists",
		"Kills",
		"DoubleKills",
		"DamageCausedDestroyer",
		"DamageCausedCorvette",
		"DamageCausedTacticalCruiser",
		"DamageCausedDreadnought",
		"DamageCausedArtilleryCruiser",
		"PowerUsage",
		"DamageCausedByAbilities",
		"Healing",
		"ControlPointsCaptured",
		"ControlPointDefenseKills",
		"TotalAmountTerritoryClaimed",
		"TotalAmountTerritoryCleared",
	}

	// Check that all expected short names exist
	for _, expectedName := range expectedShortNames {
		if !HasPlayerMatchStat(expectedName) {
			t.Errorf("Expected to find player match stat with ShortName: %s", expectedName)
		}
	}

	// Check that we have exactly the expected number of stats
	if PlayerMatchStatCount() != len(expectedShortNames) {
		t.Errorf("Expected %d player match stats, got %d", len(expectedShortNames), PlayerMatchStatCount())
	}

	t.Logf("✅ J3: All expected player match stat short names validated")
}

// TestJ3PlayerMatchStatPrioritiesValidation validates priority values
func TestJ3PlayerMatchStatPrioritiesValidation(t *testing.T) {
	// Reset the singleton for testing
	playerMatchStatsOnce = sync.Once{}
	playerMatchStats = nil
	playerMatchStatsLoaded = false

	err := LoadPlayerMatchStatistics()
	if err != nil {
		t.Fatalf("Failed to load player match statistics: %v", err)
	}

	priorities := PlayerMatchStatPriorities()
	if len(priorities) == 0 {
		t.Fatal("No priorities found")
	}

	// Check that priorities are sorted in ascending order
	for i := 1; i < len(priorities); i++ {
		if priorities[i-1] > priorities[i] {
			t.Errorf("Priorities are not sorted in ascending order: %d > %d", priorities[i-1], priorities[i])
		}
	}

	// Check that stats are sorted by priority in descending order
	stats := AllPlayerMatchStatistics()
	for i := 1; i < len(stats); i++ {
		if stats[i-1].Priority < stats[i].Priority {
			t.Errorf("Stats are not sorted by priority in descending order: %d < %d",
				stats[i-1].Priority, stats[i].Priority)
		}
	}

	// Check specific priority values
	// Based on the JSON data, we have priorities: 2, 5, 6, 10, 18, 19, 20, 21, 25, 29, 30
	expectedPriorities := []int32{2, 5, 6, 10, 18, 19, 20, 21, 25, 29, 30}
	if len(priorities) != len(expectedPriorities) {
		t.Errorf("Expected %d unique priorities, got %d", len(expectedPriorities), len(priorities))
	} else {
		for i, expected := range expectedPriorities {
			if priorities[i] != expected {
				t.Errorf("Priority[%d] = %d, expected %d", i, priorities[i], expected)
			}
		}
		t.Logf("✅ J3: All priority values validated: %v", priorities)
	}
}

// TestJ3PlayerMatchStatCategoryIDValidation validates category ID parsing
func TestJ3PlayerMatchStatCategoryIDValidation(t *testing.T) {
	// Reset the singleton for testing
	playerMatchStatsOnce = sync.Once{}
	playerMatchStats = nil
	playerMatchStatsLoaded = false

	err := LoadPlayerMatchStatistics()
	if err != nil {
		t.Fatalf("Failed to load player match statistics: %v", err)
	}

	stats := AllPlayerMatchStatistics()
	if len(stats) == 0 {
		t.Fatal("No player match statistics loaded")
	}

	// Check that all CategoryIDs have the expected format
	for _, stat := range stats {
		if !strings.Contains(stat.CategoryID, "EYPlayerMatchStatisticsCategoryID::YPMSCID_") {
			t.Errorf("CategoryID %s doesn't contain expected prefix", stat.CategoryID)
		}
	}

	// Check specific known CategoryIDs
	expectedCategoryIDs := []string{
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_Assists",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_Kills",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_DoubleKills",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_DamageCausedDestroyer",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_DamageCausedCorvette",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_DamageCausedTacticalCruiser",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_DamageCausedDreadnought",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_DamageCausedArtilleryCruiser",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_PowerUsage",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_DamageCausedByAbilities",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_Healing",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_ControlPointsCaptured",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_ControlPointDefenseKills",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_TotalAmountTerritoryClaimed",
		"EYPlayerMatchStatisticsCategoryID::YPMSCID_TotalAmountTerritoryCleared",
	}

	// Check that all expected CategoryIDs exist
	for _, expectedID := range expectedCategoryIDs {
		if _, exists := PlayerMatchStatByID(expectedID); !exists {
			t.Errorf("Expected to find player match stat with CategoryID: %s", expectedID)
		}
	}

	t.Logf("✅ J3: All expected player match stat CategoryIDs validated")
}
