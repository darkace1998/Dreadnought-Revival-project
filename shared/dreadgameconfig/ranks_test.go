package dreadgameconfig

import (
	"sync"
	"testing"
)

// TestJ1LoadRanks tests loading rank data from DN_Ranks_Player.json
func TestJ1LoadRanks(t *testing.T) {
	// Reset the singleton for testing
	ranksOnce = sync.Once{}
	ranks = nil
	ranksLoaded = false

	err := LoadRanks()
	if err != nil {
		t.Fatalf("Failed to load ranks: %v", err)
	}

	count := RankCount()
	if count == 0 {
		t.Error("Expected to load at least one rank, got 0")
	}

	t.Logf("Successfully loaded %d ranks from DN_Ranks_Player.json", count)
}

// TestJ1RankStructure validates the structure of loaded ranks
func TestJ1RankStructure(t *testing.T) {
	// Reset the singleton for testing
	ranksOnce = sync.Once{}
	ranks = nil
	ranksLoaded = false

	err := LoadRanks()
	if err != nil {
		t.Fatalf("Failed to load ranks: %v", err)
	}

	ranks := AllRanks()
	if len(ranks) == 0 {
		t.Fatal("No ranks loaded")
	}

	// Validate first rank structure
	first := ranks[0]
	if first.RankName == "" {
		t.Error("First rank has empty RankName")
	}

	// Validate that ranks are sorted by RankID
	for i := 1; i < len(ranks); i++ {
		if ranks[i-1].RankID > ranks[i].RankID {
			t.Errorf("Ranks are not sorted by RankID: %d > %d", ranks[i-1].RankID, ranks[i].RankID)
		}
	}

	t.Logf("Validated structure of %d ranks", len(ranks))
}

// TestJ1RankAccessorFunctions tests all rank accessor functions
func TestJ1RankAccessorFunctions(t *testing.T) {
	// Reset the singleton for testing
	ranksOnce = sync.Once{}
	ranks = nil
	ranksLoaded = false

	err := LoadRanks()
	if err != nil {
		t.Fatalf("Failed to load ranks: %v", err)
	}

	ranks := AllRanks()
	if len(ranks) == 0 {
		t.Fatal("No ranks loaded")
	}

	// Test RankByID
	if len(ranks) > 0 {
		firstRank := ranks[0]
		rank, exists := RankByID(firstRank.RankID)
		if !exists {
			t.Errorf("RankByID(%d) should exist", firstRank.RankID)
		} else if rank.RankID != firstRank.RankID {
			t.Errorf("RankByID(%d) returned wrong RankID: %d", firstRank.RankID, rank.RankID)
		}
	}

	// Test RankByName
	if len(ranks) > 0 {
		firstRank := ranks[0]
		rank, exists := RankByName(firstRank.RankName)
		if !exists {
			t.Errorf("RankByName(%s) should exist", firstRank.RankName)
		} else if rank.RankName != firstRank.RankName {
			t.Errorf("RankByName(%s) returned wrong RankName: %s", firstRank.RankName, rank.RankName)
		}
	}

	// Test RankCount
	count := RankCount()
	if count != len(ranks) {
		t.Errorf("RankCount() returned %d, expected %d", count, len(ranks))
	}

	// Test RankXPThreshold
	threshold := RankXPThreshold(1)
	if threshold != 0 {
		t.Errorf("RankXPThreshold(1) should return 0, got %d", threshold)
	}

	threshold = RankXPThreshold(5)
	if threshold != 1000 {
		t.Errorf("RankXPThreshold(5) should return 1000, got %d", threshold)
	}

	t.Logf("All rank accessor functions working correctly")
}

// TestJ1RankCountValidation verifies the expected number of ranks
func TestJ1RankCountValidation(t *testing.T) {
	// Reset the singleton for testing
	ranksOnce = sync.Once{}
	ranks = nil
	ranksLoaded = false

	err := LoadRanks()
	if err != nil {
		t.Fatalf("Failed to load ranks: %v", err)
	}

	count := RankCount()
	expectedCount := 51 // Expected 51 ranks based on todo.md
	if count != expectedCount {
		t.Errorf("Expected %d ranks, got %d", expectedCount, count)
	} else {
		t.Logf("✅ J1: Rank count validated: %d ranks", count)
	}

	// Verify all ranks have unique IDs
	idMap := make(map[int32]bool)
	for _, rank := range AllRanks() {
		if idMap[rank.RankID] {
			t.Errorf("Duplicate rank ID found: %d", rank.RankID)
		}
		idMap[rank.RankID] = true
	}

	// Verify we have all expected rank IDs (0-50)
	for i := 0; i < expectedCount; i++ {
		if !idMap[int32(i)] {
			t.Errorf("Missing rank ID: %d", i)
		}
	}

	t.Logf("✅ J1: All rank IDs validated (0-%d)", expectedCount-1)
}

// TestJ1RankXPThresholds validates XP thresholds for all rank ranges
func TestJ1RankXPThresholds(t *testing.T) {
	// Test XP thresholds at rank boundaries
	testCases := []struct {
		rank     int32
		expected int32
	}{
		{1, 0},      // Rank 1: 0 XP
		{2, 1000},   // Rank 2-5: 1000 XP
		{5, 1000},   // Rank 5: 1000 XP
		{6, 2000},   // Rank 6-10: 2000 XP
		{10, 2000},  // Rank 10: 2000 XP
		{11, 3500},  // Rank 11-20: 3500 XP
		{20, 3500},  // Rank 20: 3500 XP
		{21, 5000},  // Rank 21-30: 5000 XP
		{30, 5000},  // Rank 30: 5000 XP
		{31, 7500},  // Rank 31-40: 7500 XP
		{40, 7500},  // Rank 40: 7500 XP
		{41, 10000}, // Rank 41-50: 10000 XP
		{50, 10000}, // Rank 50: 10000 XP
		{51, 15000}, // Rank 51+: 15000 XP
	}

	for _, tc := range testCases {
		threshold := RankXPThreshold(tc.rank)
		if threshold != tc.expected {
			t.Errorf("RankXPThreshold(%d) returned %d, expected %d", tc.rank, threshold, tc.expected)
		}
	}

	t.Logf("✅ J1: All rank XP thresholds validated")
}
