package dreadgameconfig

import (
	"fmt"
	"sync"
	"testing"
)

// TestK1LoadHavocBossWaves tests loading Havoc boss wave data from DN_HavocBossWaves_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1LoadHavocBossWaves(t *testing.T) {
	// Reset the singleton for testing
	havocBossWavesOnce = sync.Once{}
	havocBossWaves = nil
	havocBossWavesLoaded = false

	err := LoadHavocBossWaves()
	if err != nil {
		t.Fatalf("Failed to load Havoc boss waves: %v", err)
	}

	waves := AllHavocBossWaves()
	if len(waves) == 0 {
		t.Fatal("No Havoc boss waves loaded")
	}

	t.Logf("Successfully loaded %d Havoc boss waves from DN_HavocBossWaves_DT.json", len(waves))
}

// TestK1HavocBossWaveCountValidation verifies the expected number of Havoc boss waves
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1HavocBossWaveCountValidation(t *testing.T) {
	// Reset the singleton for testing
	havocBossWavesOnce = sync.Once{}
	havocBossWaves = nil
	havocBossWavesLoaded = false

	err := LoadHavocBossWaves()
	if err != nil {
		t.Fatalf("Failed to load Havoc boss waves: %v", err)
	}

	count := HavocBossWaveCount()
	expectedCount := 4 // Expected 4 boss waves based on todo.md
	if count != expectedCount {
		t.Errorf("Expected %d Havoc boss waves, got %d", expectedCount, count)
	} else {
		t.Logf("✅ K1: Havoc boss wave count validated: %d waves", count)
	}

	// Verify all boss waves have unique row names
	nameMap := make(map[string]bool)
	for _, wave := range AllHavocBossWaves() {
		if nameMap[wave.RowName] {
			t.Errorf("Duplicate Havoc boss wave row name found: %s", wave.RowName)
		}
		nameMap[wave.RowName] = true
	}

	t.Logf("✅ K1: All Havoc boss wave row names are unique")
}

// TestK1LoadHavocRewards tests loading Havoc reward data from DN_HavocRewards_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1LoadHavocRewards(t *testing.T) {
	// Reset the singleton for testing
	havocRewardsOnce = sync.Once{}
	havocRewards = nil
	havocRewardsLoaded = false

	err := LoadHavocRewards()
	if err != nil {
		t.Fatalf("Failed to load Havoc rewards: %v", err)
	}

	rewards := AllHavocRewards()
	if len(rewards) == 0 {
		t.Fatal("No Havoc rewards loaded")
	}

	t.Logf("Successfully loaded %d Havoc rewards from DN_HavocRewards_DT.json", len(rewards))
}

// TestK1HavocRewardCountValidation verifies the expected number of Havoc rewards
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1HavocRewardCountValidation(t *testing.T) {
	// Reset the singleton for testing
	havocRewardsOnce = sync.Once{}
	havocRewards = nil
	havocRewardsLoaded = false

	err := LoadHavocRewards()
	if err != nil {
		t.Fatalf("Failed to load Havoc rewards: %v", err)
	}

	count := HavocRewardCount()
	expectedCount := 7 // Expected 7 rewards based on todo.md
	if count != expectedCount {
		t.Errorf("Expected %d Havoc rewards, got %d", expectedCount, count)
	} else {
		t.Logf("✅ K1: Havoc reward count validated: %d rewards", count)
	}

	// Verify all rewards have unique row names
	nameMap := make(map[string]bool)
	for _, reward := range AllHavocRewards() {
		if nameMap[reward.RowName] {
			t.Errorf("Duplicate Havoc reward row name found: %s", reward.RowName)
		}
		nameMap[reward.RowName] = true
	}

	t.Logf("✅ K1: All Havoc reward row names are unique")
}

// TestK1LoadHavocLoadouts tests loading Havoc loadout data from DN_HavocLoadouts_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1LoadHavocLoadouts(t *testing.T) {
	// Reset the singleton for testing
	havocLoadoutsOnce = sync.Once{}
	havocLoadouts = nil
	havocLoadoutsLoaded = false

	err := LoadHavocLoadouts()
	if err != nil {
		t.Fatalf("Failed to load Havoc loadouts: %v", err)
	}

	loadouts := AllHavocLoadouts()
	if len(loadouts) == 0 {
		t.Fatal("No Havoc loadouts loaded")
	}

	t.Logf("Successfully loaded %d Havoc loadouts from DN_HavocLoadouts_DT.json", len(loadouts))
}

// TestK1HavocLoadoutStructure validates the structure of Havoc loadout data
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1HavocLoadoutStructure(t *testing.T) {
	// Reset the singleton for testing
	havocLoadoutsOnce = sync.Once{}
	havocLoadouts = nil
	havocLoadoutsLoaded = false

	err := LoadHavocLoadouts()
	if err != nil {
		t.Fatalf("Failed to load Havoc loadouts: %v", err)
	}

	loadouts := AllHavocLoadouts()
	if len(loadouts) == 0 {
		t.Fatal("No Havoc loadouts loaded")
	}

	// Validate that loadouts have ship IDs (some may be empty as placeholders)
	emptyShipIDCount := 0
	for _, loadout := range loadouts {
		if loadout.ShipID == "" {
			emptyShipIDCount++
		}
	}
	if emptyShipIDCount > 0 {
		t.Logf("Note: %d Havoc loadouts have empty ShipID (may be placeholders)", emptyShipIDCount)
	}

	t.Logf("Validated structure of %d Havoc loadouts", len(loadouts))
}

// TestK1LoadHavocEnemyModifiers tests loading Havoc enemy modifier data from DN_HavocPermanentEnemyModifiers_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1LoadHavocEnemyModifiers(t *testing.T) {
	// Reset the singleton for testing
	havocEnemyModifiersOnce = sync.Once{}
	havocEnemyModifiers = nil
	havocEnemyModifiersLoaded = false

	err := LoadHavocEnemyModifiers()
	if err != nil {
		t.Fatalf("Failed to load Havoc enemy modifiers: %v", err)
	}

	modifiers := AllHavocEnemyModifiers()
	if len(modifiers) == 0 {
		t.Fatal("No Havoc enemy modifiers loaded")
	}

	t.Logf("Successfully loaded %d Havoc enemy modifiers from DN_HavocPermanentEnemyModifiers_DT.json", len(modifiers))
}

// TestK1LoadHavocUnlockables tests loading Havoc unlockable data from DN_HavocUnlockables_DT.json
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1LoadHavocUnlockables(t *testing.T) {
	// Reset the singleton for testing
	havocUnlockablesOnce = sync.Once{}
	havocUnlockables = nil
	havocUnlockablesLoaded = false

	err := LoadHavocUnlockables()
	if err != nil {
		t.Fatalf("Failed to load Havoc unlockables: %v", err)
	}

	unlockables := AllHavocUnlockables()
	if len(unlockables) == 0 {
		t.Fatal("No Havoc unlockables loaded")
	}

	t.Logf("Successfully loaded %d Havoc unlockables from DN_HavocUnlockables_DT.json", len(unlockables))
}

// TestK1AllHavocDataLoaded tests that all 7 Havoc files can be loaded successfully
// K1: Load Progression/Havoc/ — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
func TestK1AllHavocDataLoaded(t *testing.T) {
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

	havocLoadoutsOnce = sync.Once{}
	havocLoadouts = nil
	havocLoadoutsLoaded = false

	havocEnemyModifiersOnce = sync.Once{}
	havocEnemyModifiers = nil
	havocEnemyModifiersLoaded = false

	havocUnlockablesOnce = sync.Once{}
	havocUnlockables = nil
	havocUnlockablesLoaded = false

	// Load all Havoc data
	errors := []string{}
	
	if err := LoadHavocBoosts(); err != nil {
		errors = append(errors, fmt.Sprintf("Boosts: %v", err))
	}
	if err := LoadHavocModifiers(); err != nil {
		errors = append(errors, fmt.Sprintf("Modifiers: %v", err))
	}
	if err := LoadHavocBossWaves(); err != nil {
		errors = append(errors, fmt.Sprintf("BossWaves: %v", err))
	}
	if err := LoadHavocRewards(); err != nil {
		errors = append(errors, fmt.Sprintf("Rewards: %v", err))
	}
	if err := LoadHavocLoadouts(); err != nil {
		errors = append(errors, fmt.Sprintf("Loadouts: %v", err))
	}
	if err := LoadHavocEnemyModifiers(); err != nil {
		errors = append(errors, fmt.Sprintf("EnemyModifiers: %v", err))
	}
	if err := LoadHavocUnlockables(); err != nil {
		errors = append(errors, fmt.Sprintf("Unlockables: %v", err))
	}

	if len(errors) > 0 {
		for _, err := range errors {
			t.Error(err)
		}
		t.Fatal("Failed to load one or more Havoc data files")
	}

	// Verify all data is loaded
	boostCount := HavocBoostCount()
	modifierCount := HavocModifierCount()
	bossWaveCount := HavocBossWaveCount()
	rewardCount := HavocRewardCount()
	loadoutCount := HavocLoadoutCount()
	enemyModifierCount := HavocEnemyModifierCount()
	unlockableCount := HavocUnlockableCount()

	t.Logf("✅ K1: All 7 Havoc files loaded successfully:")
	t.Logf("  - Boosts: %d", boostCount)
	t.Logf("  - Modifiers: %d", modifierCount)
	t.Logf("  - Boss Waves: %d", bossWaveCount)
	t.Logf("  - Rewards: %d", rewardCount)
	t.Logf("  - Loadouts: %d", loadoutCount)
	t.Logf("  - Enemy Modifiers: %d", enemyModifierCount)
	t.Logf("  - Unlockables: %d", unlockableCount)

	// Verify expected counts from todo.md
	expectedCounts := map[string]int{
		"Boosts":          38,
		"Modifiers":       26,
		"Boss Waves":      4,
		"Rewards":         7,
		"Loadouts":        0, // todo.md doesn't specify count for loadouts
		"Enemy Modifiers": 0, // todo.md doesn't specify count for enemy modifiers
		"Unlockables":     0, // todo.md doesn't specify count for unlockables
	}

	actualCounts := map[string]int{
		"Boosts":          boostCount,
		"Modifiers":       modifierCount,
		"Boss Waves":      bossWaveCount,
		"Rewards":         rewardCount,
		"Loadouts":        loadoutCount,
		"Enemy Modifiers": enemyModifierCount,
		"Unlockables":     unlockableCount,
	}

	for name, expected := range expectedCounts {
		if expected > 0 {
			actual := actualCounts[name]
			if actual != expected {
				t.Errorf("Expected %d %s, got %d", expected, name, actual)
			} else {
				t.Logf("✅ K1: %s count validated: %d", name, actual)
			}
		}
	}
}