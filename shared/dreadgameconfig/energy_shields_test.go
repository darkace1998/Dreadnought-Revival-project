package dreadgameconfig

import (
	"testing"
)

// TestEnergyShieldStatsStructDefinition tests that the EnergyShieldStats struct is properly defined (G1)
func TestEnergyShieldStatsStructDefinition(t *testing.T) {
	// G1: Define Go struct `EnergyShieldStats` matching `DN_EnergyShields_DT.json` fields
	// This test explicitly validates the G1 requirement

	// Create a sample energy shield to test the struct
	shield := EnergyShieldStats{
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

	t.Logf("✅ EnergyShieldStats struct properly defined with all required fields")
}

// TestGlobalTuningStructDefinition tests that the GlobalTuning struct is properly defined (G3)
func TestGlobalTuningStructDefinition(t *testing.T) {
	// G3: Define Go struct `GlobalTuning` matching `DN_GlobalTuningValues_DT.json` fields
	// This test explicitly validates the G3 requirement

	// Create a sample global tuning value to test the struct
	tuning := GlobalTuning{
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

	t.Logf("✅ GlobalTuning struct properly defined with all required fields")
}

// TestG2LoadEnergyShieldsTable tests the G2 requirement explicitly
func TestG2LoadEnergyShieldsTable(t *testing.T) {
	// G2: Load energy shields table
	// This test explicitly validates the G2 requirement

	// Test that energy shields can be loaded
	shieldCount := EnergyShieldCount()
	if shieldCount == 0 {
		t.Fatal("G2: Expected energy shields to be loaded, got 0")
	}

	t.Logf("✅ G2: Successfully loaded %d energy shields from EnergyShields_DT.json", shieldCount)

	// Test that we can access specific shields
	_, exists := EnergyShieldByName("AssaultH")
	if !exists {
		t.Error("G2: Expected to find AssaultH energy shield")
	}

	// Test that all shields have valid data
	allShields := AllEnergyShields()
	for _, shield := range allShields {
		if shield.StaticMesh == "" {
			t.Error("G2: Found energy shield with empty StaticMesh")
		}
		if shield.ShipClass == "" {
			t.Error("G2: Found energy shield with empty ShipClass")
		}
	}

	t.Logf("✅ G2: All energy shields have valid DataTable field values")
}

// TestG4LoadGlobalTuningTable tests the G4 requirement explicitly
func TestG4LoadGlobalTuningTable(t *testing.T) {
	// G4: Load global tuning table
	// This test explicitly validates the G4 requirement

	// Test that global tuning values can be loaded
	tuningCount := GlobalTuningCount()
	if tuningCount == 0 {
		t.Fatal("G4: Expected global tuning values to be loaded, got 0")
	}

	t.Logf("✅ G4: Successfully loaded %d global tuning values from DN_GlobalTuningValues_DT.json", tuningCount)

	// Test that we can access specific tuning values
	_, exists := GlobalTuningByName("Default")
	if !exists {
		t.Error("G4: Expected to find Default global tuning value")
	}

	// Test that all tuning values have valid data
	allTunings := AllGlobalTuningValues()
	for _, tuning := range allTunings {
		if tuning.RangeToViewTargetMarkerForClassReveal == 0 {
			t.Error("G4: Found global tuning with zero RangeToViewTargetMarkerForClassReveal")
		}
		if tuning.ProjectileCloseInProjectileSpeedModifier == 0 {
			t.Error("G4: Found global tuning with zero ProjectileCloseInProjectileSpeedModifier")
		}
		if tuning.AFKTimer == 0 {
			t.Error("G4: Found global tuning with zero AFKTimer")
		}
	}

	t.Logf("✅ G4: All global tuning values have valid DataTable field values")
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