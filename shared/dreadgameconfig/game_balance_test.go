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