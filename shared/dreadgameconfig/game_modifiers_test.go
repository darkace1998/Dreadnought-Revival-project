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
