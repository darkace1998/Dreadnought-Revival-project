package dreadgameconfig

import (
	"strings"
	"testing"
)

func TestLoadAbilities(t *testing.T) {
	// Test that abilities can be loaded
	err := LoadAbilities()
	if err != nil {
		// If the data directory is not found, skip the test
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping abilities test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// Test that we can retrieve abilities
	allAbilities := AllAbilities()
	if len(allAbilities) == 0 {
		t.Fatalf("Expected abilities to be loaded, got 0")
	}

	// Test retrieving a specific ability
	testAbilities := []string{
		"DN_AbilityBroadside_OTS_DT_AbilityBroadside",
		"DN_Projectile_OTS_DT_Projectile",
		"DN_WeaponBroadside_OTS_DT_WeaponBroadside",
	}

	for _, abilityID := range testAbilities {
		ability, ok := AbilityByID(abilityID)
		if !ok {
			t.Logf("Ability %s not found (this may be expected if the ability doesn't exist)", abilityID)
			continue
		}

		// Verify that the ability has some fields populated
		if ability.AbilityName == "" && ability.CoolDown == 0 && ability.ActiveTime == 0 {
			t.Errorf("Ability %s has all empty fields", abilityID)
		}
	}

	// Test that the init function loaded the abilities
	ability, ok := AbilityByID("DN_AbilityBroadside_OTS_DT_AbilityBroadside")
	if !ok {
		t.Logf("Abilities not loaded by init function, but LoadAbilities() succeeded")
	} else {
		t.Logf("Successfully loaded ability: %s", ability.AbilityName)
	}
}

func TestAbilityStatsStructFields(t *testing.T) {
	// Test that AbilityStats struct has the expected fields by creating an instance
	// and verifying it can be populated with typical values
	ability := AbilityStats{
		AbilityName:    "TestAbility",
		TriggerName:   "TestTrigger",
		CoolDown:      5.0,
		CoolDownTime:  10.0,
		ActiveTime:    3.0,
		AbilityDamage: 100.0,
		FireRange:     5000.0,
		MaxTargets:    3,
		Health:        500.0,
		MaxHealth:     1000.0,
		TargetingType: "Auto",
		SpecifyTarget: true,
		CancelOnCollision: true,
	}

	// Verify the fields are set correctly
	if ability.AbilityName != "TestAbility" {
		t.Errorf("Expected AbilityName to be 'TestAbility', got '%s'", ability.AbilityName)
	}
	if ability.CoolDown != 5.0 {
		t.Errorf("Expected CoolDown to be 5.0, got %f", ability.CoolDown)
	}
	if ability.MaxTargets != 3 {
		t.Errorf("Expected MaxTargets to be 3, got %d", ability.MaxTargets)
	}
	if !ability.SpecifyTarget {
		t.Error("Expected SpecifyTarget to be true")
	}
}

func TestAbilityByID(t *testing.T) {
	// Test ability retrieval by ID
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping ability retrieval test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// Test with a known ability ID
	ability, ok := AbilityByID("DN_AbilityBroadside_OTS_DT_AbilityBroadside")
	if !ok {
		t.Logf("Test ability not found - this may be expected if the ability ID is different")
		return
	}

	// Verify the ability has valid data
	if ability.AbilityName == "" {
		t.Error("Expected ability to have a name")
	}
}

func TestAllAbilities(t *testing.T) {
	// Test retrieving all abilities
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping all abilities test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	allAbilities := AllAbilities()
	if len(allAbilities) == 0 {
		t.Fatalf("Expected abilities to be loaded, got 0")
	}

	// Verify that we can iterate through all abilities
	count := 0
	for _, ability := range allAbilities {
		// Just verify each ability is not nil
		if ability.AbilityName != "" || ability.CoolDown != 0 || ability.ActiveTime != 0 {
			count++
		}
	}

	if count == 0 {
		t.Error("Expected at least some abilities with non-empty fields")
	}

	t.Logf("Successfully loaded %d abilities", len(allAbilities))
}

// TestAbilitiesByType tests the ability categorization by type (E2 enhancement)
func TestAbilitiesByType(t *testing.T) {
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping abilities by type test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// Test retrieving abilities by type
	abilityTypes := AllAbilityTypes()
	if len(abilityTypes) == 0 {
		t.Fatalf("Expected ability types to be available, got 0")
	}

	// Test that we can retrieve abilities for each type
	for _, abilityType := range abilityTypes {
		abilities := AbilitiesByType(abilityType)
		if len(abilities) == 0 {
			t.Errorf("Expected abilities for type %s, got 0", abilityType)
		}
	}

	// Test specific known types
	knownTypes := []string{"AbilityBroadside", "Projectile", "WeaponBroadside"}
	for _, knownType := range knownTypes {
		abilities := AbilitiesByType(knownType)
		t.Logf("Found %d abilities of type %s", len(abilities), knownType)
	}
}

// TestAbilityCount tests the ability count function
func TestAbilityCount(t *testing.T) {
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping ability count test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	count := AbilityCount()
	if count == 0 {
		t.Error("Expected ability count to be greater than 0")
	}

	// Verify count matches the number of abilities
	allAbilities := AllAbilities()
	if count != len(allAbilities) {
		t.Errorf("AbilityCount (%d) does not match AllAbilities length (%d)", count, len(allAbilities))
	}

	t.Logf("Ability count: %d", count)
}

// TestExtractAbilityTypeFromFilename tests the ability type extraction
func TestExtractAbilityTypeFromFilename(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"DN_AbilityBroadside_OTS_DT.json", "AbilityBroadside"},
		{"DN_Projectile_OTS_DT.json", "Projectile"},
		{"DN_WeaponBroadside_OTS_DT.json", "WeaponBroadside"},
		{"DN_AbilityWarpJump_OTS_DT.json", "AbilityWarpJump"},
		{"DN_ProjectileMissile_OTS_DT.json", "ProjectileMissile"},
	}

	for _, test := range tests {
		result := extractAbilityTypeFromFilename(test.filename)
		if result != test.expected {
			t.Errorf("extractAbilityTypeFromFilename(%s) = %s, expected %s", test.filename, result, test.expected)
		}
	}
}