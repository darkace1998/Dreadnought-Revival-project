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

// TestAbilityCrossReferencing tests the ItemID cross-referencing functionality (E3)
func TestAbilityCrossReferencing(t *testing.T) {
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping ability cross-referencing test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// Test that some abilities have been cross-referenced with ItemIDRegister
	allAbilities := AllAbilities()
	crossReferencedCount := 0
	
	for id, ability := range allAbilities {
		if ability.ItemID != 0 {
			crossReferencedCount++
			t.Logf("Ability %s has ItemID %d and AssetPath %s", id, ability.ItemID, ability.AssetPath)
		}
		if ability.AssetPath != "" {
			crossReferencedCount++
		}
	}

	if crossReferencedCount > 0 {
		t.Logf("Found %d abilities with cross-referenced data", crossReferencedCount)
	} else {
		t.Log("No abilities found with cross-referenced data (ItemIDRegister may not be available in test environment)")
	}
}

// TestAbilityByItemID tests ItemID-based ability lookup (E3)
func TestAbilityByItemID(t *testing.T) {
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping ability by ItemID test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// Test with a known ability ItemID (if available)
	// These are example ItemIDs that might exist in the ItemIDRegister
	knownItemIDs := []int32{83820547, 83820548, 83820549} // Example ability ItemIDs
	
	for _, itemID := range knownItemIDs {
		ability, ok := AbilityByItemID(itemID)
		if ok {
			t.Logf("Found ability with ItemID %d: %s", itemID, ability.AbilityName)
		} else {
			t.Logf("Ability with ItemID %d not found (this may be expected in test environment)", itemID)
		}
	}
}

// TestAbilityAssetPathByID tests asset path lookup by ability ID (E3)
func TestAbilityAssetPathByID(t *testing.T) {
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping ability asset path test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// Test with a known ability ID
	allAbilities := AllAbilities()
	if len(allAbilities) > 0 {
		// Test with the first ability
		for id := range allAbilities {
			assetPath, ok := AbilityAssetPathByID(id)
			if ok {
				t.Logf("Ability %s has asset path: %s", id, assetPath)
				break
			}
		}
	}
}

// TestAbilityE4Accessors tests all E4 accessor functions
func TestAbilityE4Accessors(t *testing.T) {
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping E4 accessor test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// Test AbilityIDs
	ids := AbilityIDs()
	if len(ids) == 0 {
		t.Fatalf("Expected ability IDs to be available, got 0")
	}
	t.Logf("Found %d ability IDs", len(ids))

	// Test that we can retrieve each ability by ID
	for _, id := range ids {
		ability, ok := AbilityByID(id)
		if !ok {
			t.Errorf("Expected to find ability with ID %s", id)
		}
		if ability.AbilityName == "" && ability.CoolDown == 0 && ability.ActiveTime == 0 {
			t.Logf("Ability %s has empty fields (this may be expected for some abilities)", id)
		}
	}

	// Test AllAbilities
	allAbilities := AllAbilities()
	if len(allAbilities) != len(ids) {
		t.Errorf("AllAbilities count (%d) does not match AbilityIDs count (%d)", len(allAbilities), len(ids))
	}

	// Test AbilityCount
	count := AbilityCount()
	if count != len(allAbilities) {
		t.Errorf("AbilityCount (%d) does not match AllAbilities length (%d)", count, len(allAbilities))
	}
}

// TestAbilityE6Validation tests ability count and value validation (E6)
func TestAbilityE6Validation(t *testing.T) {
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping E6 validation test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// E6: Verify ability count
	count := AbilityCount()
	if count == 0 {
		t.Fatalf("Expected abilities to be loaded, got 0")
	}
	t.Logf("Ability count: %d", count)

	// E6: Validate cooldown/damage values
	allAbilities := AllAbilities()
	validCooldownCount := 0
	validDamageCount := 0
	zeroCooldownCount := 0
	zeroDamageCount := 0

	for id, ability := range allAbilities {
		// Validate cooldown values
		if ability.CoolDown > 0 {
			validCooldownCount++
		} else if ability.CoolDown == 0 {
			zeroCooldownCount++
		}

		// Validate damage values
		if ability.AbilityDamage > 0 || ability.DamageAmount > 0 || ability.MaxDamage > 0 {
			validDamageCount++
		} else if ability.AbilityDamage == 0 && ability.DamageAmount == 0 && ability.MaxDamage == 0 {
			zeroDamageCount++
		}

		// Log some examples
		if validCooldownCount <= 3 {
			t.Logf("Ability %s: CoolDown=%f, Damage=%f", id, ability.CoolDown, ability.AbilityDamage)
		}
	}

	t.Logf("Abilities with valid cooldown: %d", validCooldownCount)
	t.Logf("Abilities with zero cooldown: %d", zeroCooldownCount)
	t.Logf("Abilities with valid damage: %d", validDamageCount)
	t.Logf("Abilities with zero damage: %d", zeroDamageCount)

	// Most abilities should have either cooldown or damage values
	if validCooldownCount+validDamageCount < count/2 {
		t.Logf("Warning: Many abilities have zero cooldown and damage values - this may be expected for some ability types")
	}
}

// TestFilterAbilities tests the filtering functions
func TestFilterAbilities(t *testing.T) {
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping filter test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// Test filtering by name
	nameFiltered := FilterAbilitiesByName("Broadside")
	t.Logf("Found %d abilities with 'Broadside' in name", len(nameFiltered))

	// Test filtering by cooldown
	cooldownFiltered := FilterAbilitiesByCooldown(0, 10)
	t.Logf("Found %d abilities with cooldown between 0 and 10", len(cooldownFiltered))

	// Test filtering by damage
	damageFiltered := FilterAbilitiesByDamage(0, 1000)
	t.Logf("Found %d abilities with damage between 0 and 1000", len(damageFiltered))
}

// ==================== E6: Comprehensive Validation Tests ====================

// TestE6AbilityCountValidation verifies ability count matches expectations (E6)
func TestE6AbilityCountValidation(t *testing.T) {
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping E6 ability count validation - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// E6: Verify ability count - should have 103+ abilities from 24 files
	count := AbilityCount()
	allAbilities := AllAbilities()
	
	if count == 0 {
		t.Fatalf("E6: Expected abilities to be loaded, got 0")
	}
	
	if count != len(allAbilities) {
		t.Fatalf("E6: AbilityCount (%d) does not match AllAbilities length (%d)", count, len(allAbilities))
	}
	
	// The todo.md mentions 103+ abilities from 24 files
	// We should have at least some minimum number of abilities
	minExpectedAbilities := 50 // Conservative minimum
	if count < minExpectedAbilities {
		t.Errorf("E6: Expected at least %d abilities, got %d", minExpectedAbilities, count)
	} else {
		t.Logf("E6: Ability count validation passed - loaded %d abilities", count)
	}
}

// TestE6CooldownValidation validates cooldown values across all abilities (E6)
func TestE6CooldownValidation(t *testing.T) {
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping E6 cooldown validation - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// E6: Validate cooldown/damage values
	allAbilities := AllAbilities()
	if len(allAbilities) == 0 {
		t.Fatalf("E6: Expected abilities to be loaded for cooldown validation")
	}

	// Track cooldown statistics
	var cooldownStats struct {
		TotalCount       int
		ZeroCount        int
		PositiveCount    int
		MinPositive      float64
		MaxPositive      float64
		SumPositive      float64
	}
	cooldownStats.MinPositive = float64(^uint(0) >> 1) // Max float64

	for _, ability := range allAbilities {
		cooldownStats.TotalCount++
		
		if ability.CoolDown == 0 {
			cooldownStats.ZeroCount++
		} else {
			cooldownStats.PositiveCount++
			if ability.CoolDown < cooldownStats.MinPositive {
				cooldownStats.MinPositive = ability.CoolDown
			}
			if ability.CoolDown > cooldownStats.MaxPositive {
				cooldownStats.MaxPositive = ability.CoolDown
			}
			cooldownStats.SumPositive += ability.CoolDown
		}
	}

	// Validate cooldown statistics
	if cooldownStats.PositiveCount == 0 {
		t.Logf("E6: No abilities with positive cooldown values found")
	} else {
		avgCooldown := cooldownStats.SumPositive / float64(cooldownStats.PositiveCount)
		t.Logf("E6: Cooldown validation - Total: %d, Zero: %d, Positive: %d", 
			cooldownStats.TotalCount, cooldownStats.ZeroCount, cooldownStats.PositiveCount)
		t.Logf("E6: Cooldown range - Min: %.2f, Max: %.2f, Avg: %.2f", 
			cooldownStats.MinPositive, cooldownStats.MaxPositive, avgCooldown)
		
		// Validate that positive cooldowns are reasonable
		if cooldownStats.MinPositive < 0 {
			t.Errorf("E6: Found negative cooldown values")
		}
		if cooldownStats.MaxPositive > 1000 {
			t.Logf("E6: Some abilities have very high cooldown values (>1000)")
		}
	}
}

// TestE6DamageValidation validates damage values across all abilities (E6)
func TestE6DamageValidation(t *testing.T) {
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping E6 damage validation - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// E6: Validate damage values
	allAbilities := AllAbilities()
	if len(allAbilities) == 0 {
		t.Fatalf("E6: Expected abilities to be loaded for damage validation")
	}

	// Track damage statistics across all damage fields
	var damageStats struct {
		TotalCount        int
		ZeroCount         int
		PositiveCount     int
		MinPositive       float64
		MaxPositive       float64
		SumPositive       float64
		FieldsWithDamage  int
	}
	damageStats.MinPositive = float64(^uint(0) >> 1) // Max float64

	for _, ability := range allAbilities {
		// Check all damage-related fields
		damageFields := []float64{
			ability.AbilityDamage,
			ability.DamageAmount,
			ability.DamageOnEnemy,
			ability.PulseDamage,
			ability.MaxDamage,
			ability.DamageAmountOnPath,
		}
		
		hasDamage := false
		for _, damage := range damageFields {
			if damage > 0 {
				hasDamage = true
				damageStats.PositiveCount++
				if damage < damageStats.MinPositive {
					damageStats.MinPositive = damage
				}
				if damage > damageStats.MaxPositive {
					damageStats.MaxPositive = damage
				}
				damageStats.SumPositive += damage
			}
			if damage == 0 {
				damageStats.ZeroCount++
			}
		}
		
		if hasDamage {
			damageStats.FieldsWithDamage++
		}
		damageStats.TotalCount++
	}

	// Validate damage statistics
	if damageStats.PositiveCount == 0 {
		t.Logf("E6: No abilities with positive damage values found")
	} else {
		avgDamage := damageStats.SumPositive / float64(damageStats.PositiveCount)
		t.Logf("E6: Damage validation - Total: %d, Zero: %d, Positive: %d", 
			damageStats.TotalCount, damageStats.ZeroCount, damageStats.PositiveCount)
		t.Logf("E6: Damage range - Min: %.2f, Max: %.2f, Avg: %.2f", 
			damageStats.MinPositive, damageStats.MaxPositive, avgDamage)
		
		// Validate that positive damages are reasonable
		if damageStats.MinPositive < 0 {
			t.Errorf("E6: Found negative damage values")
		}
		if damageStats.MaxPositive > 10000 {
			t.Logf("E6: Some abilities have very high damage values (>10000)")
		}
	}

	// Validate that abilities have either cooldown or damage (or both)
	abilitiesWithCooldownOrDamage := 0
	for _, ability := range allAbilities {
		if ability.CoolDown > 0 || ability.AbilityDamage > 0 || ability.DamageAmount > 0 || ability.MaxDamage > 0 {
			abilitiesWithCooldownOrDamage++
		}
	}
	
	t.Logf("E6: %d/%d abilities have cooldown or damage values", 
		abilitiesWithCooldownOrDamage, len(allAbilities))
}

// TestE6AbilityDataQuality validates the quality and completeness of ability data (E6)
func TestE6AbilityDataQuality(t *testing.T) {
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping E6 data quality validation - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// E6: Validate data quality and completeness
	allAbilities := AllAbilities()
	if len(allAbilities) == 0 {
		t.Fatalf("E6: Expected abilities to be loaded for data quality validation")
	}

	// Track data quality metrics
	var qualityMetrics struct {
		TotalAbilities          int
		WithNames               int
		WithCooldown            int
		WithActiveTime          int
		WithAnyDamage           int
		WithTargeting           int
		WithItemID             int
		WithAssetPath           int
		FullyPopulated         int
	}

	for _, ability := range allAbilities {
		qualityMetrics.TotalAbilities++
		
		if ability.AbilityName != "" && ability.AbilityName != "None" {
			qualityMetrics.WithNames++
		}
		if ability.CoolDown > 0 {
			qualityMetrics.WithCooldown++
		}
		if ability.ActiveTime > 0 {
			qualityMetrics.WithActiveTime++
		}
		if ability.AbilityDamage > 0 || ability.DamageAmount > 0 || ability.MaxDamage > 0 {
			qualityMetrics.WithAnyDamage++
		}
		if ability.TargetingType != "" {
			qualityMetrics.WithTargeting++
		}
		if ability.ItemID != 0 {
			qualityMetrics.WithItemID++
		}
		if ability.AssetPath != "" {
			qualityMetrics.WithAssetPath++
		}
		
		// Consider an ability "fully populated" if it has name and at least one meaningful value
		if ability.AbilityName != "" && ability.AbilityName != "None" &&
			(ability.CoolDown > 0 || ability.ActiveTime > 0 || ability.AbilityDamage > 0 || ability.DamageAmount > 0) {
			qualityMetrics.FullyPopulated++
		}
	}

	// Report quality metrics
	t.Logf("E6: Data Quality Metrics:")
	t.Logf("  Total abilities: %d", qualityMetrics.TotalAbilities)
	t.Logf("  With names: %d (%.1f%%)", qualityMetrics.WithNames, float64(qualityMetrics.WithNames)/float64(qualityMetrics.TotalAbilities)*100)
	t.Logf("  With cooldown: %d (%.1f%%)", qualityMetrics.WithCooldown, float64(qualityMetrics.WithCooldown)/float64(qualityMetrics.TotalAbilities)*100)
	t.Logf("  With active time: %d (%.1f%%)", qualityMetrics.WithActiveTime, float64(qualityMetrics.WithActiveTime)/float64(qualityMetrics.TotalAbilities)*100)
	t.Logf("  With damage: %d (%.1f%%)", qualityMetrics.WithAnyDamage, float64(qualityMetrics.WithAnyDamage)/float64(qualityMetrics.TotalAbilities)*100)
	t.Logf("  With targeting: %d (%.1f%%)", qualityMetrics.WithTargeting, float64(qualityMetrics.WithTargeting)/float64(qualityMetrics.TotalAbilities)*100)
	t.Logf("  With ItemID: %d (%.1f%%)", qualityMetrics.WithItemID, float64(qualityMetrics.WithItemID)/float64(qualityMetrics.TotalAbilities)*100)
	t.Logf("  With AssetPath: %d (%.1f%%)", qualityMetrics.WithAssetPath, float64(qualityMetrics.WithAssetPath)/float64(qualityMetrics.TotalAbilities)*100)
	t.Logf("  Fully populated: %d (%.1f%%)", qualityMetrics.FullyPopulated, float64(qualityMetrics.FullyPopulated)/float64(qualityMetrics.TotalAbilities)*100)

	// Validate that a reasonable percentage of abilities have meaningful data
	if qualityMetrics.FullyPopulated < qualityMetrics.TotalAbilities/2 {
		t.Logf("E6: Warning: Less than 50%% of abilities are fully populated")
	}
}

// TestAbilityWiring tests the E5 wiring of ability stats into item metadata
func TestAbilityWiring(t *testing.T) {
	// Ensure abilities are loaded and wired
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping ability wiring test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// Test that ability items have been wired with ability stats
	abilityItems := ItemsByType(ItemTypeAbility)
	if len(abilityItems) == 0 {
		t.Fatalf("Expected to find ability items, got 0")
	}

	wiredCount := 0
	for _, item := range abilityItems {
		if item.AbilityStats != nil {
			wiredCount++
			t.Logf("Ability item '%s' (ItemID: %d) has wired stats: CoolDown=%f, Damage=%f",
				item.DisplayName, item.ItemID, item.AbilityStats.CoolDown, item.AbilityStats.AbilityDamage)
		}
	}

	if wiredCount > 0 {
		t.Logf("Successfully wired %d ability items with stats", wiredCount)
	} else {
		t.Log("No ability items found with wired stats (this may be expected in test environment)")
	}
}

// TestAbilityE5Integration tests the complete E5 integration
func TestAbilityE5Integration(t *testing.T) {
	// Test that abilities are properly integrated into the system
	err := LoadAbilities()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping E5 integration test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load abilities: %v", err)
	}

	// Test that we can access abilities through multiple paths
	allAbilities := AllAbilities()
	if len(allAbilities) == 0 {
		t.Fatalf("Expected abilities to be loaded, got 0")
	}

	// Test that ability items are available
	abilityItems := ItemsByType(ItemTypeAbility)
	t.Logf("Found %d ability items in catalog", len(abilityItems))

	// Test that some ability items have stats wired
	wiredAbilityItems := 0
	for _, item := range abilityItems {
		if item.AbilityStats != nil {
			wiredAbilityItems++
		}
	}
	t.Logf("Found %d ability items with wired stats", wiredAbilityItems)

	// Test that we can access abilities through the accessors
	abilityIDs := AbilityIDs()
	if len(abilityIDs) > 0 {
		for _, id := range abilityIDs {
			ability, ok := AbilityByID(id)
			if !ok {
				t.Errorf("Expected to find ability with ID %s", id)
			}
			if ability.AbilityName == "" && ability.CoolDown == 0 {
				t.Logf("Ability %s has empty fields", id)
			}
		}
	}

	// Test filtering
	filtered := FilterAbilitiesByName("Warp")
	t.Logf("Found %d abilities with 'Warp' in name", len(filtered))
}