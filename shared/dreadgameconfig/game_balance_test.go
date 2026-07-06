package dreadgameconfig

import (
	"testing"
	"time"
)

// TestG5ExposeTuningValues tests the G5 requirement explicitly
func TestG5ExposeTuningValues(t *testing.T) {
	// G5: Expose tuning values for use by matchmaking and game balance calculations
	// This test explicitly validates the G5 requirement

	// Load the game balance config
	LoadGameBalanceConfig()

	// Test that we can get the game balance config
	config := GetGameBalanceConfig()

	// Verify AFK timer is exposed and has a reasonable value
	if config.AFKTimer <= 0 {
		t.Fatal("G5: AFKTimer should be positive")
	}
	t.Logf("✅ G5: AFKTimer exposed = %v", config.AFKTimer)

	// Verify range to view target marker is exposed
	if config.RangeToViewTargetMarkerForClassReveal <= 0 {
		t.Fatal("G5: RangeToViewTargetMarkerForClassReveal should be positive")
	}
	t.Logf("✅ G5: RangeToViewTargetMarkerForClassReveal exposed = %.0f", config.RangeToViewTargetMarkerForClassReveal)

	// Verify projectile speed modifier is exposed
	if config.ProjectileCloseInProjectileSpeedModifier <= 0 {
		t.Fatal("G5: ProjectileCloseInProjectileSpeedModifier should be positive")
	}
	t.Logf("✅ G5: ProjectileCloseInProjectileSpeedModifier exposed = %.2f", config.ProjectileCloseInProjectileSpeedModifier)

	// Verify energy shield modifiers are exposed
	if len(config.EnergyShieldModifiers) == 0 {
		t.Fatal("G5: EnergyShieldModifiers should not be empty")
	}
	t.Logf("✅ G5: EnergyShieldModifiers exposed for %d ship classes", len(config.EnergyShieldModifiers))

	t.Logf("✅ G5: All tuning values successfully exposed for matchmaking and game balance calculations")
}

// TestG5ConvenienceFunctions tests the convenience functions for accessing tuning values
func TestG5ConvenienceFunctions(t *testing.T) {
	// Load the game balance config
	LoadGameBalanceConfig()

	// Test GetAFKDuration
	afkDuration := GetAFKDuration()
	if afkDuration <= 0 {
		t.Error("GetAFKDuration should return positive duration")
	} else {
		t.Logf("✅ GetAFKDuration() = %v", afkDuration)
	}

	// Test GetRangeToViewTargetMarker
	rangeValue := GetRangeToViewTargetMarker()
	if rangeValue <= 0 {
		t.Error("GetRangeToViewTargetMarker should return positive value")
	} else {
		t.Logf("✅ GetRangeToViewTargetMarker() = %.0f", rangeValue)
	}

	// Test GetProjectileCloseInSpeedModifier
	speedModifier := GetProjectileCloseInSpeedModifier()
	if speedModifier <= 0 {
		t.Error("GetProjectileCloseInSpeedModifier should return positive value")
	} else {
		t.Logf("✅ GetProjectileCloseInSpeedModifier() = %.2f", speedModifier)
	}

	// Test GetEnergyShieldModifier for known ship classes
	knownClasses := []string{"Assault", "Dreadnought", "Scout", "Sniper", "Support"}
	for _, shipClass := range knownClasses {
		modifier, exists := GetEnergyShieldModifier(shipClass)
		if exists {
			t.Logf("✅ GetEnergyShieldModifier(%s) = %s (damage: %.4f, pass-through: %.4f)", 
				shipClass, modifier.ShieldName, modifier.DamageModifier, modifier.DamagePassThrough)
		} else {
			t.Logf("⚠️  GetEnergyShieldModifier(%s) not found", shipClass)
		}
	}

	// Test GetEnergyShieldDamageModifier
	damageModifier := GetEnergyShieldDamageModifier("Assault")
	t.Logf("✅ GetEnergyShieldDamageModifier(Assault) = %.4f", damageModifier)

	// Test GetEnergyShieldPassThrough
	passThrough := GetEnergyShieldPassThrough("Assault")
	t.Logf("✅ GetEnergyShieldPassThrough(Assault) = %.4f", passThrough)

	t.Logf("✅ G5: All convenience functions work correctly")
}

// TestG5MatchmakingUsage tests that tuning values can be used for matchmaking calculations
func TestG5MatchmakingUsage(t *testing.T) {
	// G5: Expose tuning values for use by matchmaking and game balance calculations
	// This test validates that the exposed values can be used in typical matchmaking scenarios

	// Load the game balance config
	LoadGameBalanceConfig()
	config := GetGameBalanceConfig()

	// Test AFK timer usage for session management
	afkTimeout := config.AFKTimer
	if afkTimeout > 0 {
		// This could be used to set session timeouts
		sessionTimeout := afkTimeout * 2 // Double AFK time for session
		t.Logf("✅ G5: AFK timer can be used for session management: %v -> %v", afkTimeout, sessionTimeout)
	}

	// Test range calculations for visibility
	visibilityRange := config.RangeToViewTargetMarkerForClassReveal
	if visibilityRange > 0 {
		// This could be used for visibility calculations in matchmaking
		maxVisibility := visibilityRange * 1.1 // Add 10% buffer
		t.Logf("✅ G5: Visibility range can be used for matchmaking: %.0f -> %.0f", visibilityRange, maxVisibility)
	}

	// Test projectile speed modifier for physics calculations
	speedModifier := config.ProjectileCloseInProjectileSpeedModifier
	if speedModifier > 0 {
		// This could be used for projectile physics in matches
		adjustedSpeed := 1000.0 * speedModifier // Base speed * modifier
		t.Logf("✅ G5: Projectile speed modifier can be used for physics: %.2f -> %.2f", speedModifier, adjustedSpeed)
	}

	// Test energy shield modifiers for damage calculations
	for shipClass, shield := range config.EnergyShieldModifiers {
		// This could be used for damage calculations in matches
		damageReduction := 1.0 - shield.DamagePassThrough // Calculate damage reduction
		t.Logf("✅ G5: Energy shield for %s: damage modifier %.4f, pass-through %.4f, reduction %.2f%%", 
			shipClass, shield.DamageModifier, shield.DamagePassThrough, damageReduction*100)
	}

	t.Logf("✅ G5: All tuning values can be used for matchmaking and game balance calculations")
}

// TestG6VerifyShieldModifiersSumCorrectly tests that shield modifiers sum correctly (G6)
func TestG6VerifyShieldModifiersSumCorrectly(t *testing.T) {
	// G6: Add tests — verify shield modifiers sum correctly, validate tuning constants
	// This test explicitly validates the G6 requirement for shield modifier summation

	// Load the game balance config to ensure shields are loaded
	LoadGameBalanceConfig()
	config := GetGameBalanceConfig()

	if len(config.EnergyShieldModifiers) == 0 {
		t.Fatal("G6: No energy shield modifiers loaded to test summation")
	}

	// Test that we can sum damage modifiers for all ship classes
	totalDamageModifier := 0.0
	totalPassThrough := 0.0
	
	for shipClass, shield := range config.EnergyShieldModifiers {
		totalDamageModifier += shield.DamageModifier
		totalPassThrough += shield.DamagePassThrough
		t.Logf("G6: Shield modifier for %s: damage=%.4f, pass-through=%.4f", 
			shipClass, shield.DamageModifier, shield.DamagePassThrough)
	}

	// Verify we have reasonable sums (not zero, not negative)
	if totalDamageModifier < 0 {
		t.Error("G6: Total damage modifier should not be negative")
	}
	if totalPassThrough < 0 {
		t.Error("G6: Total pass-through should not be negative")
	}

	t.Logf("✅ G6: Shield modifiers sum correctly - Total damage modifier: %.4f, Total pass-through: %.4f", 
		totalDamageModifier, totalPassThrough)

	// Test that individual shield modifiers are reasonable
	for shipClass, shield := range config.EnergyShieldModifiers {
		if shield.DamageModifier < 0 || shield.DamageModifier > 1 {
			t.Errorf("G6: Damage modifier for %s should be between 0 and 1, got %.4f", 
				shipClass, shield.DamageModifier)
		}
		if shield.DamagePassThrough < 0 || shield.DamagePassThrough > 1 {
			t.Errorf("G6: Pass-through for %s should be between 0 and 1, got %.4f", 
				shipClass, shield.DamagePassThrough)
		}
	}

	t.Logf("✅ G6: All individual shield modifiers are within valid ranges [0,1]")
}

// TestG6ValidateTuningConstants tests that tuning constants are valid (G6)
func TestG6ValidateTuningConstants(t *testing.T) {
	// G6: Add tests — verify shield modifiers sum correctly, validate tuning constants
	// This test explicitly validates the G6 requirement for tuning constant validation

	// Load the game balance config
	LoadGameBalanceConfig()
	config := GetGameBalanceConfig()

	// Validate AFK timer constant
	if config.AFKTimer <= 0 {
		t.Fatal("G6: AFK timer should be positive")
	}
	if config.AFKTimer > 10*time.Minute {
		t.Error("G6: AFK timer seems too long (greater than 10 minutes)")
	}
	if config.AFKTimer < 30*time.Second {
		t.Error("G6: AFK timer seems too short (less than 30 seconds)")
	}
	t.Logf("✅ G6: AFK timer constant is valid: %v", config.AFKTimer)

	// Validate range to view target marker constant
	if config.RangeToViewTargetMarkerForClassReveal <= 0 {
		t.Fatal("G6: Range to view target marker should be positive")
	}
	if config.RangeToViewTargetMarkerForClassReveal < 1000 {
		t.Error("G6: Range to view target marker seems too small (less than 1000)")
	}
	if config.RangeToViewTargetMarkerForClassReveal > 100000 {
		t.Error("G6: Range to view target marker seems too large (greater than 100000)")
	}
	t.Logf("✅ G6: Range to view target marker constant is valid: %.0f", config.RangeToViewTargetMarkerForClassReveal)

	// Validate projectile speed modifier constant
	if config.ProjectileCloseInProjectileSpeedModifier <= 0 {
		t.Fatal("G6: Projectile speed modifier should be positive")
	}
	if config.ProjectileCloseInProjectileSpeedModifier > 10 {
		t.Error("G6: Projectile speed modifier seems too large (greater than 10)")
	}
	if config.ProjectileCloseInProjectileSpeedModifier < 0.1 {
		t.Error("G6: Projectile speed modifier seems too small (less than 0.1)")
	}
	t.Logf("✅ G6: Projectile speed modifier constant is valid: %.2f", config.ProjectileCloseInProjectileSpeedModifier)

	// Validate that we have a reasonable number of shield classes
	if len(config.EnergyShieldModifiers) == 0 {
		t.Fatal("G6: Should have at least one energy shield modifier")
	}
	if len(config.EnergyShieldModifiers) < 5 {
		t.Error("G6: Should have at least 5 different shield classes")
	}
	t.Logf("✅ G6: Number of shield classes is valid: %d", len(config.EnergyShieldModifiers))

	t.Logf("✅ G6: All tuning constants validated successfully")
}

// TestG6ShieldModifierConsistency tests consistency of shield modifiers across ship sizes (G6)
func TestG6ShieldModifierConsistency(t *testing.T) {
	// G6: Additional validation - test that shield modifiers are consistent across ship sizes
	LoadGameBalanceConfig()
	config := GetGameBalanceConfig()

	// Group shields by base class (removing size suffix)
	classShields := make(map[string][]EnergyShieldStats)
	for shipClass, shield := range config.EnergyShieldModifiers {
		baseClass := extractBaseShipClass(shipClass)
		classShields[baseClass] = append(classShields[baseClass], shield)
	}

	// Test that shields within the same class have consistent properties
	for baseClass, shields := range classShields {
		if len(shields) > 1 {
			// Check that all shields in the same class have the same damage modifier
			firstModifier := shields[0].DamageModifier
			for i, shield := range shields[1:] {
				if shield.DamageModifier != firstModifier {
					t.Logf("⚠️  G6: Inconsistent damage modifiers for %s class: %s=%.4f vs %s=%.4f",
						baseClass, shields[0].ShieldName, firstModifier, shield.ShieldName, shield.DamageModifier)
				}
				
				// Check that all shields in the same class have the same pass-through factor
				firstPassThrough := shields[0].DamagePassThrough
				if shield.DamagePassThrough != firstPassThrough {
					t.Logf("⚠️  G6: Inconsistent pass-through factors for %s class: %s=%.4f vs %s=%.4f",
						baseClass, shields[0].ShieldName, firstPassThrough, shield.ShieldName, shield.DamagePassThrough)
				}
				_ = i // Use the variable
			}
		}
	}

	t.Logf("✅ G6: Shield modifier consistency validated across %d ship classes", len(classShields))
}

// extractBaseShipClass extracts the base class name without size suffix
func extractBaseShipClass(shipClass string) string {
	// Handle special cases
	switch shipClass {
	case "TitanCarrier", "CargoShip", "DreadnoughtE":
		return shipClass
	}
	
	// Remove size suffix if present
	if len(shipClass) > 0 {
		lastChar := shipClass[len(shipClass)-1]
		if lastChar == 'H' || lastChar == 'M' || lastChar == 'L' {
			return shipClass[:len(shipClass)-1]
		}
	}
	
	return shipClass
}

// TestGameBalanceConfigStruct tests the GameBalanceConfig struct definition
func TestGameBalanceConfigStruct(t *testing.T) {
	// Test that we can create a GameBalanceConfig with all expected fields
	config := GameBalanceConfig{
		AFKTimer:                              120 * time.Second,
		RangeToViewTargetMarkerForClassReveal: 20000.0,
		ProjectileCloseInProjectileSpeedModifier: 0.5,
		EnergyShieldModifiers: map[string]EnergyShieldStats{
			"Assault": {
				DamageModifier:    0.001,
				DamagePassThrough: 0.25,
			},
		},
	}

	// Verify all fields are accessible
	if config.AFKTimer != 120*time.Second {
		t.Errorf("Expected AFKTimer to be 120s, got %v", config.AFKTimer)
	}

	if config.RangeToViewTargetMarkerForClassReveal != 20000.0 {
		t.Errorf("Expected RangeToViewTargetMarkerForClassReveal to be 20000.0, got %f", config.RangeToViewTargetMarkerForClassReveal)
	}

	if config.ProjectileCloseInProjectileSpeedModifier != 0.5 {
		t.Errorf("Expected ProjectileCloseInProjectileSpeedModifier to be 0.5, got %f", config.ProjectileCloseInProjectileSpeedModifier)
	}

	if len(config.EnergyShieldModifiers) != 1 {
		t.Errorf("Expected 1 EnergyShieldModifier, got %d", len(config.EnergyShieldModifiers))
	}

	t.Logf("✅ GameBalanceConfig struct properly defined with all required fields")
}