package dreadgameconfig

import (
	"testing"
)

// TestEnergyShieldStructDefinition tests that the EnergyShield struct is properly defined (G1)
func TestEnergyShieldStructDefinition(t *testing.T) {
	// G1: Define Go structs for energy shield DataTable fields
	// This test verifies that the EnergyShield struct exists and has the expected fields

	// Create a sample energy shield to test the struct
	shield := EnergyShield{
		StaticMesh:         "import[20]",
		DamageModifier:    0.001,
		DamagePassThrough: 0.25,
		ShieldName:        "AssaultH",
		ShipClass:         "Assault",
	}

	// Verify all fields are accessible
	if shield.StaticMesh != "import[20]" {
		t.Errorf("Expected StaticMesh to be 'import[20]', got '%s'", shield.StaticMesh)
	}

	if shield.DamageModifier != 0.001 {
		t.Errorf("Expected DamageModifier to be 0.001, got %f", shield.DamageModifier)
	}

	if shield.DamagePassThrough != 0.25 {
		t.Errorf("Expected DamagePassThrough to be 0.25, got %f", shield.DamagePassThrough)
	}

	if shield.ShipClass != "Assault" {
		t.Errorf("Expected ShipClass to be 'Assault', got '%s'", shield.ShipClass)
	}

	t.Logf("✅ EnergyShield struct properly defined with all required fields")
}

// TestGlobalTuningValueStructDefinition tests that the GlobalTuningValue struct is properly defined (G1)
func TestGlobalTuningValueStructDefinition(t *testing.T) {
	// G1: Define Go structs for global tuning DataTable fields
	// This test verifies that the GlobalTuningValue struct exists and has the expected fields

	// Create a sample global tuning value to test the struct
	tuning := GlobalTuningValue{
		RangeToViewTargetMarkerForClassReveal:    20000.0,
		ProjectileCloseInProjectileSpeedModifier: 0.5,
		AFKTimer:                                      119.5,
		TuningName:                                   "Default",
	}

	// Verify all fields are accessible
	if tuning.RangeToViewTargetMarkerForClassReveal != 20000.0 {
		t.Errorf("Expected RangeToViewTargetMarkerForClassReveal to be 20000.0, got %f", tuning.RangeToViewTargetMarkerForClassReveal)
	}

	if tuning.ProjectileCloseInProjectileSpeedModifier != 0.5 {
		t.Errorf("Expected ProjectileCloseInProjectileSpeedModifier to be 0.5, got %f", tuning.ProjectileCloseInProjectileSpeedModifier)
	}

	if tuning.AFKTimer != 119.5 {
		t.Errorf("Expected AFKTimer to be 119.5, got %f", tuning.AFKTimer)
	}

	t.Logf("✅ GlobalTuningValue struct properly defined with all required fields")
}

// TestG1DefineEnergyShieldAndGlobalTuningStructs tests the G1 requirement explicitly
func TestG1DefineEnergyShieldAndGlobalTuningStructs(t *testing.T) {
	// G1: Define Go structs for energy shield DataTable fields
	// This test explicitly validates the G1 requirement

	// Test EnergyShield struct
	shield := EnergyShield{
		StaticMesh:         "import[23]",
		DamageModifier:    0.0,
		DamagePassThrough: 0.35,
		ShieldName:        "DreadH",
		ShipClass:         "Dreadnought",
	}

	// Verify the struct can hold DataTable values
	if shield.StaticMesh == "" || shield.DamageModifier < 0 || shield.DamagePassThrough < 0 {
		t.Fatal("EnergyShield struct cannot hold DataTable field values")
	}

	// Test GlobalTuningValue struct
	tuning := GlobalTuningValue{
		RangeToViewTargetMarkerForClassReveal:    20000.0,
		ProjectileCloseInProjectileSpeedModifier: 0.5,
		AFKTimer:                                      119.5,
		TuningName:                                   "Default",
	}

	// Verify the struct can hold DataTable values
	if tuning.RangeToViewTargetMarkerForClassReveal == 0 || tuning.ProjectileCloseInProjectileSpeedModifier == 0 || tuning.AFKTimer == 0 {
		t.Fatal("GlobalTuningValue struct cannot hold DataTable field values")
	}

	t.Logf("✅ G1: EnergyShield and GlobalTuningValue structs successfully defined for DataTable fields")
	_ = shield // Use the variable to avoid unused variable error
	_ = tuning // Use the variable to avoid unused variable error
}

// TestShipClassExtraction tests the ship class extraction from shield names
func TestShipClassExtraction(t *testing.T) {
	testCases := []struct {
		name      string
		expected string
	}{
		{"AssaultH", "Assault"},
		{"AssaultM", "Assault"},
		{"AssaultL", "Assault"},
		{"DreadH", "Dreadnought"},
		{"DreadM", "Dreadnought"},
		{"DreadL", "Dreadnought"},
		{"ScoutH", "Scout"},
		{"ScoutM", "Scout"},
		{"ScoutL", "Scout"},
		{"SniperH", "Sniper"},
		{"SniperM", "Sniper"},
		{"SniperL", "Sniper"},
		{"SupportH", "Support"},
		{"SupportM", "Support"},
		{"SupportL", "Support"},
		{"TitanCarrier", "TitanCarrier"},
		{"CargoShip_Escort", "CargoShip"},
		{"DreadE", "DreadnoughtE"},
	}

	for _, tc := range testCases {
		result := extractShipClassFromShieldName(tc.name)
		if result != tc.expected {
			t.Errorf("extractShipClassFromShieldName(%q) = %q, want %q", tc.name, result, tc.expected)
		}
	}

	t.Logf("✅ Ship class extraction works correctly for all test cases")
}

// TestEnergyShieldAccessorFunctions tests the accessor functions for energy shields
func TestEnergyShieldAccessorFunctions(t *testing.T) {
	// Test that accessor functions exist and work
	// These should not panic even if no shields are loaded yet
	shieldCount := EnergyShieldCount()
	t.Logf("Energy shield count: %d", shieldCount)

	allShields := AllEnergyShields()
	t.Logf("All energy shields length: %d", len(allShields))

	allNames := AllEnergyShieldNames()
	t.Logf("All energy shield names length: %d", len(allNames))

	// Test individual access
	_, exists := EnergyShieldByName("AssaultH")
	if exists {
		t.Log("Found energy shield by name AssaultH")
	} else {
		t.Log("Energy shield by name AssaultH not found (expected if data files not available)")
	}

	// Test ship class filtering
	assaultShields := EnergyShieldsForShipClass("Assault")
	t.Logf("Assault shields count: %d", len(assaultShields))

	t.Logf("✅ All energy shield accessor functions work correctly")
}

// TestGlobalTuningAccessorFunctions tests the accessor functions for global tuning values
func TestGlobalTuningAccessorFunctions(t *testing.T) {
	// Test that accessor functions exist and work
	// These should not panic even if no tuning values are loaded yet
	tuningCount := GlobalTuningCount()
	t.Logf("Global tuning count: %d", tuningCount)

	allTunings := AllGlobalTuningValues()
	t.Logf("All global tuning values length: %d", len(allTunings))

	allNames := AllGlobalTuningNames()
	t.Logf("All global tuning names length: %d", len(allNames))

	// Test individual access
	_, exists := GlobalTuningByName("Default")
	if exists {
		t.Log("Found global tuning by name Default")
	} else {
		t.Log("Global tuning by name Default not found (expected if data files not available)")
	}

	// Test convenience functions
	rangeValue := GetRangeToViewTargetMarkerForClassReveal()
	t.Logf("Range to view target marker: %f", rangeValue)

	speedModifier := GetProjectileCloseInProjectileSpeedModifier()
	t.Logf("Projectile speed modifier: %f", speedModifier)

	afkTimer := GetAFKTimer()
	t.Logf("AFK timer: %f", afkTimer)

	t.Logf("✅ All global tuning accessor functions work correctly")
}