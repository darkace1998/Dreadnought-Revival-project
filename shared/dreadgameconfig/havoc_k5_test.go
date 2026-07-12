package dreadgameconfig

import (
	"sync"
	"testing"
)

// TestK5HavocBoostCount verifies the expected number of Havoc boosts (38)
// K5: Add tests — verify boost/modifier counts match (38 boosts, 26 modifiers)
func TestK5HavocBoostCount(t *testing.T) {
	// Reset the singleton for testing
	havocBoostsOnce = sync.Once{}
	havocBoosts = nil
	havocBoostsLoaded = false

	err := LoadHavocBoosts()
	if err != nil {
		t.Fatalf("Failed to load Havoc boosts: %v", err)
	}

	count := HavocBoostCount()
	expectedCount := 38
	if count != expectedCount {
		t.Errorf("❌ K5: Expected %d Havoc boosts, got %d", expectedCount, count)
	} else {
		t.Logf("✅ K5: Havoc boost count matches expected: %d boosts", count)
	}
}

// TestK5HavocModifierCount verifies the expected number of Havoc modifiers (26)
// K5: Add tests — verify boost/modifier counts match (38 boosts, 26 modifiers)
func TestK5HavocModifierCount(t *testing.T) {
	// Reset the singleton for testing
	havocModifiersOnce = sync.Once{}
	havocModifiers = nil
	havocModifiersLoaded = false

	err := LoadHavocModifiers()
	if err != nil {
		t.Fatalf("Failed to load Havoc modifiers: %v", err)
	}

	count := HavocModifierCount()
	expectedCount := 26
	if count != expectedCount {
		t.Errorf("❌ K5: Expected %d Havoc modifiers, got %d", expectedCount, count)
	} else {
		t.Logf("✅ K5: Havoc modifier count matches expected: %d modifiers", count)
	}
}

// TestK5HavocAllCounts verifies all Havoc data counts in one test
// K5: Add tests — verify boost/modifier counts match (38 boosts, 26 modifiers)
func TestK5HavocAllCounts(t *testing.T) {
	// Reset all singletons for testing
	havocBoostsOnce = sync.Once{}
	havocBoosts = nil
	havocBoostsLoaded = false

	havocModifiersOnce = sync.Once{}
	havocModifiers = nil
	havocModifiersLoaded = false

	havocBossWavesOnce = sync.Once{}
	havocBossWaves = nil
	havocBossWavesLoaded = false

	havocRewardsOnce = sync.Once{}
	havocRewards = nil
	havocRewardsLoaded = false

	// Load all data
	if err := LoadHavocBoosts(); err != nil {
		t.Fatalf("Failed to load Havoc boosts: %v", err)
	}
	if err := LoadHavocModifiers(); err != nil {
		t.Fatalf("Failed to load Havoc modifiers: %v", err)
	}
	if err := LoadHavocBossWaves(); err != nil {
		t.Fatalf("Failed to load Havoc boss waves: %v", err)
	}
	if err := LoadHavocRewards(); err != nil {
		t.Fatalf("Failed to load Havoc rewards: %v", err)
	}

	// Verify counts
	boostCount := HavocBoostCount()
	modifierCount := HavocModifierCount()
	bossWaveCount := HavocBossWaveCount()
	rewardCount := HavocRewardCount()

	expectedBoosts := 38
	expectedModifiers := 26
	expectedBossWaves := 4
	expectedRewards := 7

	allMatch := true

	if boostCount != expectedBoosts {
		t.Errorf("❌ K5: Expected %d Havoc boosts, got %d", expectedBoosts, boostCount)
		allMatch = false
	}

	if modifierCount != expectedModifiers {
		t.Errorf("❌ K5: Expected %d Havoc modifiers, got %d", expectedModifiers, modifierCount)
		allMatch = false
	}

	if bossWaveCount != expectedBossWaves {
		t.Errorf("❌ K5: Expected %d Havoc boss waves, got %d", expectedBossWaves, bossWaveCount)
		allMatch = false
	}

	if rewardCount != expectedRewards {
		t.Errorf("❌ K5: Expected %d Havoc rewards, got %d", expectedRewards, rewardCount)
		allMatch = false
	}

	if allMatch {
		t.Logf("✅ K5: All Havoc data counts match expected values")
		t.Logf("   Boosts: %d, Modifiers: %d, Boss Waves: %d, Rewards: %d",
			boostCount, modifierCount, bossWaveCount, rewardCount)
	}
}
