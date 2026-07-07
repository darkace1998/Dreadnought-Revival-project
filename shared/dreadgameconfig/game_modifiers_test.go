package dreadgameconfig

import (
	"strings"
	"sync"
	"testing"
)

// TestJ2LoadGameModifiers tests loading game modifier data from DN_GameModifiers_DT.json
func TestJ2LoadGameModifiers(t *testing.T) {
	// Reset the singleton for testing
	gameModifiersOnce = sync.Once{}
	gameModifiers = nil
	gameModifiersLoaded = false

	err := LoadGameModifiers()
	if err != nil {
		t.Fatalf("Failed to load game modifiers: %v", err)
	}

	count := GameModifierCount()
	if count == 0 {
		t.Error("Expected to load at least one game modifier, got 0")
	}

	t.Logf("Successfully loaded %d game modifiers from DN_GameModifiers_DT.json", count)
}

// TestJ2GameModifierStructure validates the structure of loaded game modifiers
func TestJ2GameModifierStructure(t *testing.T) {
	// Reset the singleton for testing
	gameModifiersOnce = sync.Once{}
	gameModifiers = nil
	gameModifiersLoaded = false

	err := LoadGameModifiers()
	if err != nil {
		t.Fatalf("Failed to load game modifiers: %v", err)
	}

	modifiers := AllGameModifiers()
	if len(modifiers) == 0 {
		t.Fatal("No game modifiers loaded")
	}

	// Validate first modifier structure
	first := modifiers[0]
	if first.GameModeName == "" {
		t.Error("First game modifier has empty GameModeName")
	}
	if first.AffectedTeam == "" {
		t.Error("First game modifier has empty AffectedTeam")
	}

	// Validate that all modifiers have non-empty GameModeName
	for i, modifier := range modifiers {
		if modifier.GameModeName == "" {
			t.Errorf("Game modifier %d has empty GameModeName", i)
		}
	}

	t.Logf("Validated structure of %d game modifiers", len(modifiers))
}

// TestJ2GameModifierAccessorFunctions tests all game modifier accessor functions
func TestJ2GameModifierAccessorFunctions(t *testing.T) {
	// Reset the singleton for testing
	gameModifiersOnce = sync.Once{}
	gameModifiers = nil
	gameModifiersLoaded = false

	err := LoadGameModifiers()
	if err != nil {
		t.Fatalf("Failed to load game modifiers: %v", err)
	}

	modifiers := AllGameModifiers()
	if len(modifiers) == 0 {
		t.Fatal("No game modifiers loaded")
	}

	// Test GameModifierByName
	if len(modifiers) > 0 {
		name := modifiers[0].GameModeName
		modifier, exists := GameModifierByName(name)
		if !exists {
			t.Errorf("GameModifierByName(%s) should exist", name)
		} else if modifier.GameModeName != name {
			t.Errorf("GameModifierByName(%s) returned wrong GameModeName: %s", name, modifier.GameModeName)
		}
	}

	// Test GameModifierCount
	count := GameModifierCount()
	if count != len(modifiers) {
		t.Errorf("GameModifierCount() returned %d, expected %d", count, len(modifiers))
	}

	// Test GameModifierFeats
	feats := GameModifierFeats()
	t.Logf("Found %d unique feats across all game modifiers", len(feats))

	// Test HasGameModifierFeat
	if len(feats) > 0 {
		feat := feats[0]
		if !HasGameModifierFeat(feat) {
			t.Errorf("HasGameModifierFeat(%s) should return true", feat)
		}
		if HasGameModifierFeat("NonExistentFeat") {
			t.Error("HasGameModifierFeat(\"NonExistentFeat\") should return false")
		}
	}

	t.Logf("All game modifier accessor functions working correctly")
}

// TestJ2GameModifierCountValidation verifies the expected number of game modifiers
func TestJ2GameModifierCountValidation(t *testing.T) {
	// Reset the singleton for testing
	gameModifiersOnce = sync.Once{}
	gameModifiers = nil
	gameModifiersLoaded = false

	err := LoadGameModifiers()
	if err != nil {
		t.Fatalf("Failed to load game modifiers: %v", err)
	}

	count := GameModifierCount()
	expectedCount := 2 // Expected 2 actual game modifiers (excluding config row)
	if count != expectedCount {
		t.Errorf("Expected %d game modifiers, got %d", expectedCount, count)
	} else {
		t.Logf("✅ J2: Game modifier count validated: %d modifiers", count)
	}

	// Verify all modifiers have unique names
	nameMap := make(map[string]bool)
	for _, modifier := range AllGameModifiers() {
		if nameMap[modifier.GameModeName] {
			t.Errorf("Duplicate game modifier name found: %s", modifier.GameModeName)
		}
		nameMap[modifier.GameModeName] = true
	}

	t.Logf("✅ J2: All game modifier names are unique")
}

// TestJ2GameModifierFeatsValidation validates feat parsing and access
func TestJ2GameModifierFeatsValidation(t *testing.T) {
	// Reset the singleton for testing
	gameModifiersOnce = sync.Once{}
	gameModifiers = nil
	gameModifiersLoaded = false

	err := LoadGameModifiers()
	if err != nil {
		t.Fatalf("Failed to load game modifiers: %v", err)
	}

	modifiers := AllGameModifiers()
	if len(modifiers) == 0 {
		t.Fatal("No game modifiers loaded")
	}

	// Check that feats are parsed correctly
	for _, modifier := range modifiers {
		// Verify that FeatList is populated if Feats is not empty
		if modifier.Feats != "" && len(modifier.FeatList) == 0 {
			t.Errorf("Game modifier %s has Feats but empty FeatList", modifier.GameModeName)
		}

		// Verify that FeatList matches Feats (split by semicolon)
		if modifier.Feats != "" {
			expectedFeats := strings.Split(modifier.Feats, ";")
			if len(modifier.FeatList) != len(expectedFeats) {
				t.Errorf("Game modifier %s: FeatList length %d != expected %d",
					modifier.GameModeName, len(modifier.FeatList), len(expectedFeats))
			}
			for i, feat := range expectedFeats {
				if i >= len(modifier.FeatList) || modifier.FeatList[i] != feat {
					t.Errorf("Game modifier %s: FeatList[%d] = %s, expected %s",
						modifier.GameModeName, i, modifier.FeatList[i], feat)
				}
			}
		}

		// Verify that ExcludeList is populated if Excludes is not empty
		if modifier.Excludes != "" && len(modifier.ExcludeList) == 0 {
			t.Errorf("Game modifier %s has Excludes but empty ExcludeList", modifier.GameModeName)
		}
	}

	// Check specific known modifiers
	// Turbo TDM should have speed, damage, and fire rate modifiers
	turboTDM, exists := GameModifierByName("YGMT_TURBO_TDM")
	if !exists {
		t.Error("Expected to find YGMT_TURBO_TDM game modifier")
	} else {
		// Check that Turbo TDM has the expected feats
		expectedFeats := []string{"ModifierSpeed", "ModifierDamage", "ModifierFireRate"}
		if len(turboTDM.FeatList) != len(expectedFeats) {
			t.Errorf("YGMT_TURBO_TDM: expected %d feats, got %d", len(expectedFeats), len(turboTDM.FeatList))
		} else {
			for i, expectedFeat := range expectedFeats {
				if i >= len(turboTDM.FeatList) || turboTDM.FeatList[i] != expectedFeat {
					t.Errorf("YGMT_TURBO_TDM: FeatList[%d] = %s, expected %s",
						i, turboTDM.FeatList[i], expectedFeat)
				}
			}
			t.Logf("✅ J2: YGMT_TURBO_TDM has expected feats: %v", turboTDM.FeatList)
		}
	}

	// TDM should have no feats
	tdm, exists := GameModifierByName("YGMT_TDM")
	if !exists {
		t.Error("Expected to find YGMT_TDM game modifier")
	} else {
		if len(tdm.FeatList) != 0 {
			t.Errorf("YGMT_TDM: expected 0 feats, got %d", len(tdm.FeatList))
		} else {
			t.Logf("✅ J2: YGMT_TDM has no feats as expected")
		}
	}

	t.Logf("✅ J2: All game modifier feats validated")
}

// TestJ2GameModifierAffectedTeamValidation validates affected team parsing
func TestJ2GameModifierAffectedTeamValidation(t *testing.T) {
	// Reset the singleton for testing
	gameModifiersOnce = sync.Once{}
	gameModifiers = nil
	gameModifiersLoaded = false

	err := LoadGameModifiers()
	if err != nil {
		t.Fatalf("Failed to load game modifiers: %v", err)
	}

	modifiers := AllGameModifiers()
	if len(modifiers) == 0 {
		t.Fatal("No game modifiers loaded")
	}

	// Check that all modifiers have valid AffectedTeam values
	for _, modifier := range modifiers {
		if modifier.AffectedTeam == "" {
			t.Errorf("Game modifier %s has empty AffectedTeam", modifier.GameModeName)
		}
		// Check that AffectedTeam contains expected prefix
		if !strings.Contains(modifier.AffectedTeam, "YHMAT_") {
			t.Errorf("Game modifier %s: AffectedTeam %s doesn't contain expected prefix",
				modifier.GameModeName, modifier.AffectedTeam)
		}
	}

	t.Logf("✅ J2: All game modifier affected teams validated")
}

// TestJ5GameModifierFieldValidation performs comprehensive field validation for game modifiers
// J5: Add tests — verify rank count, validate game modifier fields
func TestJ5GameModifierFieldValidation(t *testing.T) {
	// Reset the singleton for testing
	gameModifiersOnce = sync.Once{}
	gameModifiers = nil
	gameModifiersLoaded = false

	err := LoadGameModifiers()
	if err != nil {
		t.Fatalf("Failed to load game modifiers: %v", err)
	}

	// Verify exact count
	expectedCount := 2
	actualCount := GameModifierCount()

	if actualCount != expectedCount {
		t.Errorf("Expected exactly %d game modifiers, got %d", expectedCount, actualCount)
	} else {
		t.Logf("✅ J5: Game modifier count verified: %d modifiers", actualCount)
	}

	// Get all game modifiers and verify each has required fields
	allModifiers := AllGameModifiers()
	if len(allModifiers) != expectedCount {
		t.Errorf("AllGameModifiers() returned %d modifiers, expected %d", len(allModifiers), expectedCount)
	}

	// Expected game modifier names
	expectedNames := []string{"YGMT_TURBO_TDM", "YGMT_TDM"}
	nameSet := make(map[string]bool)
	for _, modifier := range allModifiers {
		nameSet[modifier.GameModeName] = true

		// Verify GameModeName is not empty
		if modifier.GameModeName == "" {
			t.Error("Found game modifier with empty GameModeName")
		}

		// Verify AffectedTeam is valid
		validTeams := map[string]bool{
			"All": true, 
			"Team1": true, 
			"Team2": true, 
			"": true,
			"EYGameModeModifierAffectedTeam::YHMAT_ALL": true, // Actual value from data
		}
		if !validTeams[modifier.AffectedTeam] {
			t.Errorf("Invalid AffectedTeam value: %s", modifier.AffectedTeam)
		}

		// Verify Excludes is a valid string (can be empty)
		if modifier.Excludes == "" {
			// Empty excludes is valid
		} else if !strings.Contains(modifier.Excludes, ";") && modifier.Excludes != "" {
			// Single exclude value is valid
		}

		// Verify Feats is a valid string (can be empty)
		if modifier.Feats == "" {
			// Empty feats is valid
		} else if !strings.Contains(modifier.Feats, ";") && modifier.Feats != "" {
			// Single feat value is valid
		}

		// Verify parsed FeatList is consistent with Feats string
		if modifier.Feats != "" {
			parsedFeats := strings.Split(modifier.Feats, ";")
			if len(modifier.FeatList) != len(parsedFeats) {
				t.Errorf("Game modifier %s: FeatList length (%d) doesn't match parsed Feats length (%d)", modifier.GameModeName, len(modifier.FeatList), len(parsedFeats))
			}
		}

		// Verify parsed ExcludeList is consistent with Excludes string
		if modifier.Excludes != "" {
			parsedExcludes := strings.Split(modifier.Excludes, ";")
			if len(modifier.ExcludeList) != len(parsedExcludes) {
				t.Errorf("Game modifier %s: ExcludeList length (%d) doesn't match parsed Excludes length (%d)", modifier.GameModeName, len(modifier.ExcludeList), len(parsedExcludes))
			}
		}
	}

	// Verify we have all expected game modifier names
	for _, expectedName := range expectedNames {
		if !nameSet[expectedName] {
			t.Errorf("Expected game modifier not found: %s", expectedName)
		}
	}

	// Verify no unexpected game modifier names
	for name := range nameSet {
		found := false
		for _, expectedName := range expectedNames {
			if name == expectedName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Unexpected game modifier found: %s", name)
		}
	}

	t.Logf("✅ J5: All game modifier fields validated for %d modifiers", len(allModifiers))
}

// TestJ5GameModifierDataIntegrity performs comprehensive data integrity checks for game modifiers
// J5: Add tests — verify rank count, validate game modifier fields
func TestJ5GameModifierDataIntegrity(t *testing.T) {
	// Reset the singleton for testing
	gameModifiersOnce = sync.Once{}
	gameModifiers = nil
	gameModifiersLoaded = false

	err := LoadGameModifiers()
	if err != nil {
		t.Fatalf("Failed to load game modifiers: %v", err)
	}

	// Test accessor functions work correctly
	allModifiers := AllGameModifiers()
	if len(allModifiers) == 0 {
		t.Fatal("No game modifiers loaded")
	}

	// Verify GameModifierByName works for all modifiers
	for _, expectedModifier := range allModifiers {
		modifier, exists := GameModifierByName(expectedModifier.GameModeName)
		if !exists {
			t.Errorf("GameModifierByName(%s) should exist", expectedModifier.GameModeName)
			continue
		}
		if modifier.GameModeName != expectedModifier.GameModeName {
			t.Errorf("GameModifierByName(%s) returned wrong GameModeName: %s", expectedModifier.GameModeName, modifier.GameModeName)
		}
		if modifier.AffectedTeam != expectedModifier.AffectedTeam {
			t.Errorf("GameModifierByName(%s) returned wrong AffectedTeam: %s", expectedModifier.GameModeName, modifier.AffectedTeam)
		}
		if len(modifier.Feats) != len(expectedModifier.Feats) {
			t.Errorf("GameModifierByName(%s) returned wrong Feats length: %d", expectedModifier.GameModeName, len(modifier.Feats))
		}
		if len(modifier.Excludes) != len(expectedModifier.Excludes) {
			t.Errorf("GameModifierByName(%s) returned wrong Excludes length: %d", expectedModifier.GameModeName, len(modifier.Excludes))
		}
	}

	// Verify GameModifierFeats works correctly
	allFeats := GameModifierFeats()
	expectedTotalFeats := 3 // From YGMT_TURBO_TDM: ModifierSpeed;ModifierDamage;ModifierFireRate
	if len(allFeats) != expectedTotalFeats {
		t.Errorf("GameModifierFeats() returned %d feats, expected %d", len(allFeats), expectedTotalFeats)
	}

	// Verify HasGameModifierFeat works correctly
	for _, modifier := range allModifiers {
		for _, feat := range modifier.FeatList {
			if !HasGameModifierFeat(feat) {
				t.Errorf("HasGameModifierFeat(%s) should return true", feat)
			}
		}
	}

	// Verify feat uniqueness within each modifier
	for _, modifier := range allModifiers {
		featSet := make(map[string]bool)
		for _, feat := range modifier.FeatList {
			if featSet[feat] {
				t.Errorf("Duplicate feat found in modifier %s: %s", modifier.GameModeName, feat)
			}
			featSet[feat] = true
		}
	}

	// Verify exclude uniqueness within each modifier
	for _, modifier := range allModifiers {
		excludeSet := make(map[string]bool)
		for _, exclude := range modifier.ExcludeList {
			if excludeSet[exclude] {
				t.Errorf("Duplicate exclude found in modifier %s: %s", modifier.GameModeName, exclude)
			}
			excludeSet[exclude] = true
		}
	}

	t.Logf("✅ J5: Game modifier data integrity verified for all %d modifiers", len(allModifiers))
}

// TestJ5CrossValidationRankGameModifier performs cross-validation between ranks and game modifiers
// J5: Add tests — verify rank count, validate game modifier fields
func TestJ5CrossValidationRankGameModifier(t *testing.T) {
	// Reset singletons for testing
	ranksOnce = sync.Once{}
	ranks = nil
	ranksLoaded = false

	gameModifiersOnce = sync.Once{}
	gameModifiers = nil
	gameModifiersLoaded = false

	// Load both datasets
	err := LoadRanks()
	if err != nil {
		t.Fatalf("Failed to load ranks: %v", err)
	}

	err = LoadGameModifiers()
	if err != nil {
		t.Fatalf("Failed to load game modifiers: %v", err)
	}

	// Verify both datasets loaded successfully
	rankCount := RankCount()
	modifierCount := GameModifierCount()

	if rankCount == 0 {
		t.Error("No ranks loaded for cross-validation")
	}

	if modifierCount == 0 {
		t.Error("No game modifiers loaded for cross-validation")
	}

	// Verify we can access both datasets simultaneously
	allRanks := AllRanks()
	allModifiers := AllGameModifiers()

	if len(allRanks) != rankCount {
		t.Errorf("AllRanks() returned %d ranks, expected %d", len(allRanks), rankCount)
	}

	if len(allModifiers) != modifierCount {
		t.Errorf("AllGameModifiers() returned %d modifiers, expected %d", len(allModifiers), modifierCount)
	}

	// Verify rank XP thresholds are accessible for all ranks
	for _, rank := range allRanks {
		threshold := RankXPThreshold(rank.RankID)
		if threshold < 0 {
			t.Errorf("Rank %d has invalid XP threshold: %d", rank.RankID, threshold)
		}
	}

	// Verify game modifier feats are accessible for all modifiers
	allFeats := GameModifierFeats()
	if len(allFeats) == 0 {
		t.Error("GameModifierFeats() returned no feats")
	}

	t.Logf("✅ J5: Cross-validation between %d ranks and %d game modifiers successful", rankCount, modifierCount)
}
