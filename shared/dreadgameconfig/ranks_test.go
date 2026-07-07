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

// TestJ4RankThresholdVerification verifies that XP thresholds match hardcoded values
// J4: Verify rank names/thresholds match current hardcoded values
func TestJ4RankThresholdVerification(t *testing.T) {
	// Verify that the RankXPThreshold function returns the expected values
	// These are the same values used in mmogbrain/handlers/handlers.go
	expectedThresholds := map[int32]int32{
		0:  0,
		1:  0,
		2:  1000,
		5:  1000,
		6:  2000,
		10: 2000,
		11: 3500,
		20: 3500,
		21: 5000,
		30: 5000,
		31: 7500,
		40: 7500,
		41: 10000,
		50: 10000,
		51: 15000, // Beyond max rank
	}

	for rankID, expectedThreshold := range expectedThresholds {
		actualThreshold := RankXPThreshold(rankID)
		if actualThreshold != expectedThreshold {
			t.Errorf("RankXPThreshold(%d) = %d, expected %d", rankID, actualThreshold, expectedThreshold)
		}
	}

	t.Logf("✅ J4: Rank XP thresholds match hardcoded values")
}

// TestJ4VerifyRankThresholds verifies all rank thresholds using the verification function
// J4: Verify rank names/thresholds match current hardcoded values
func TestJ4VerifyRankThresholds(t *testing.T) {
	// This test verifies that all rank XP thresholds match the expected hardcoded values
	valid, err := VerifyRankThresholds()
	if err != nil {
		t.Fatalf("VerifyRankThresholds() failed: %v", err)
	}
	if !valid {
		t.Error("VerifyRankThresholds() returned false - XP thresholds don't match expected values")
	}

	t.Logf("✅ J4: All rank XP thresholds verified against hardcoded values")
}

// TestJ4RankWithThresholds tests the combined rank and threshold functionality
// J4: Verify rank names/thresholds match current hardcoded values
func TestJ4RankWithThresholds(t *testing.T) {
	// Reset the singleton for testing
	ranksOnce = sync.Once{}
	ranks = nil
	ranksLoaded = false

	err := LoadRanks()
	if err != nil {
		t.Fatalf("Failed to load ranks: %v", err)
	}

	// Test AllRanksWithThresholds
	ranksWithThresholds := AllRanksWithThresholds()
	if len(ranksWithThresholds) == 0 {
		t.Fatal("AllRanksWithThresholds() returned no ranks")
	}

	// Verify that all ranks have XP thresholds
	for _, rwt := range ranksWithThresholds {
		if rwt.XPThreshold < 0 {
			t.Errorf("Rank %d has negative XP threshold: %d", rwt.RankID, rwt.XPThreshold)
		}
	}

	// Test RankWithThresholdByID
	if len(ranksWithThresholds) > 0 {
		firstRank := ranksWithThresholds[0]
		rwt, exists := RankWithThresholdByID(firstRank.RankID)
		if !exists {
			t.Errorf("RankWithThresholdByID(%d) should exist", firstRank.RankID)
		} else if rwt.RankID != firstRank.RankID {
			t.Errorf("RankWithThresholdByID(%d) returned wrong RankID: %d", firstRank.RankID, rwt.RankID)
		} else if rwt.XPThreshold != firstRank.XPThreshold {
			t.Errorf("RankWithThresholdByID(%d) returned wrong XPThreshold: %d", firstRank.RankID, rwt.XPThreshold)
		}
	}

	// Verify specific rank thresholds
	// Rank 0-1: 0 XP
	for rankID := 0; rankID <= 1; rankID++ {
		rwt, exists := RankWithThresholdByID(int32(rankID))
		if !exists {
			t.Errorf("RankWithThresholdByID(%d) should exist", rankID)
		} else if rwt.XPThreshold != 0 {
			t.Errorf("Rank %d should have XP threshold 0, got %d", rankID, rwt.XPThreshold)
		}
	}

	// Rank 2-5: 1000 XP
	for rankID := 2; rankID <= 5; rankID++ {
		rwt, exists := RankWithThresholdByID(int32(rankID))
		if !exists {
			t.Errorf("RankWithThresholdByID(%d) should exist", rankID)
		} else if rwt.XPThreshold != 1000 {
			t.Errorf("Rank %d should have XP threshold 1000, got %d", rankID, rwt.XPThreshold)
		}
	}

	// Rank 6-10: 2000 XP
	for rankID := 6; rankID <= 10; rankID++ {
		rwt, exists := RankWithThresholdByID(int32(rankID))
		if !exists {
			t.Errorf("RankWithThresholdByID(%d) should exist", rankID)
		} else if rwt.XPThreshold != 2000 {
			t.Errorf("Rank %d should have XP threshold 2000, got %d", rankID, rwt.XPThreshold)
		}
	}

	t.Logf("✅ J4: Rank with threshold functionality working correctly")
}

// TestJ4RankCountMatchesHardcoded verifies that the loaded rank count matches expectations
// J4: Verify rank names/thresholds match current hardcoded values
func TestJ4RankCountMatchesHardcoded(t *testing.T) {
	// Reset the singleton for testing
	ranksOnce = sync.Once{}
	ranks = nil
	ranksLoaded = false

	err := LoadRanks()
	if err != nil {
		t.Fatalf("Failed to load ranks: %v", err)
	}

	// The hardcoded RankXPThreshold function in mmogbrain/handlers/handlers.go
	// expects ranks 0-50 (51 total ranks)
	expectedCount := 51
	actualCount := RankCount()

	if actualCount != expectedCount {
		t.Errorf("Expected %d ranks to match hardcoded XP thresholds, got %d", expectedCount, actualCount)
	} else {
		t.Logf("✅ J4: Rank count (%d) matches hardcoded XP threshold expectations", actualCount)
	}

	// Verify that we have ranks for all IDs that the hardcoded function expects
	for rankID := 0; rankID <= 50; rankID++ {
		if _, exists := RankByID(int32(rankID)); !exists {
			t.Errorf("Expected to find rank with ID %d to match hardcoded XP thresholds", rankID)
		}
	}

	t.Logf("✅ J4: All rank IDs (0-50) present to match hardcoded XP thresholds")
}
