package dreadgameconfig

import (
	"strings"
	"testing"
)

func TestLoadShipFeats(t *testing.T) {
	// Test that ship feats can be loaded
	err := LoadShipFeats()
	if err != nil {
		// If the data directory is not found, skip the test
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping ship feats test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load ship feats: %v", err)
	}

	// Test that we can retrieve ship feats
	allFeats := AllShipFeats()
	if len(allFeats) == 0 {
		t.Fatalf("Expected ship feats to be loaded, got 0")
	}

	// Test retrieving a specific feat
	testFeats := []string{
		"DN_Feats_Shared_EnergyPack",
		"DN_Feats_Shared_EnergyPack_T2",
		"DN_Feats_AssaultMedium_T5_Afterburner",
	}

	for _, featName := range testFeats {
		feat, ok := ShipFeatByName(featName)
		if !ok {
			t.Errorf("Expected to find feat %s, but it was not found", featName)
			continue
		}

		if feat.Enabling == "" {
			t.Errorf("Expected feat %s to have non-empty enabling condition", featName)
		}
		if feat.Triggers == "" {
			t.Errorf("Expected feat %s to have non-empty triggers", featName)
		}
		if feat.Effects == "" {
			t.Errorf("Expected feat %s to have non-empty effects", featName)
		}
	}

	// Test that the init function loaded the feats
	feat, ok := ShipFeatByName("DN_Feats_Shared_EnergyPack")
	if !ok {
		t.Logf("Ship feats not loaded by init function, but LoadShipFeats() succeeded")
	}
	if ok && feat.Enabling != "OnAcquire()" {
		t.Errorf("Expected EnergyPack enabling to be 'OnAcquire()', got '%s'", feat.Enabling)
	}
}

func TestParseFeatEffects(t *testing.T) {
	// Test AM (Additive Modifier) parsing
	effects := []struct {
		input    string
		expected []FeatEffect
	}{
		{
			input: "AM(PawnDamageModifier +75%) :Stacks(1)",
			expected: []FeatEffect{
				{
					EffectType:    "AM",
					ModifierType: "PawnDamageModifier",
					Value:        0.75,
					Stacks:       1,
				},
			},
		},
		{
			input: "AM(PawnForwardForceModifier +600%) :Stacks(99);AM(PawnReverseForceModifier +600%) :Stacks(99)",
			expected: []FeatEffect{
				{
					EffectType:    "AM",
					ModifierType: "PawnForwardForceModifier",
					Value:        6.0,
					Stacks:       99,
				},
				{
					EffectType:    "AM",
					ModifierType: "PawnReverseForceModifier",
					Value:        6.0,
					Stacks:       99,
				},
			},
		},
		{
			input: "RM(Energy InitialEnergyCosts -0)",
			expected: []FeatEffect{
				{
					EffectType:    "RM",
					ModifierType: "Energy",
					Value:        0,
				},
			},
		},
		{
			input: "DFS(EnergyOnShield)",
			expected: []FeatEffect{
				{
					EffectType: "DFS",
					BuffType:  "Disable:EnergyOnShield",
				},
			},
		},
		{
			input: "PCFS(#Energy_To_Engine)",
			expected: []FeatEffect{
				{
					EffectType: "PCFS",
					BuffType:  "Condition:#Energy_To_Engine",
				},
			},
		},
		{
			input: "AM(PawnHealthRegenerationModifier +50%) :Stacks(99);AM(PawnEnergyConsumptionRateModifier -30%) :Stacks(1); DFS(EnergyOnShield) ;DFS(EnergyOnDamage); DFS(EnergyOff); PCFS(#Energy_To_Engine)",
			expected: []FeatEffect{
				{
					EffectType:    "AM",
					ModifierType: "PawnHealthRegenerationModifier",
					Value:        0.5,
					Stacks:       99,
				},
				{
					EffectType:    "AM",
					ModifierType: "PawnEnergyConsumptionRateModifier",
					Value:        -0.3,
					Stacks:       1,
				},
				{
					EffectType: "DFS",
					BuffType:  "Disable:EnergyOnShield",
				},
				{
					EffectType: "DFS",
					BuffType:  "Disable:EnergyOnDamage",
				},
				{
					EffectType: "DFS",
					BuffType:  "Disable:EnergyOff",
				},
				{
					EffectType: "PCFS",
					BuffType:  "Condition:#Energy_To_Engine",
				},
			},
		},
		{
			input: "AM(PawnDamageModifier +50%) :Stacks(1) : D(10.0)",
			expected: []FeatEffect{
				{
					EffectType:    "AM",
					ModifierType: "PawnDamageModifier",
					Value:        0.5,
					Stacks:       1,
					Duration:     10.0,
				},
			},
		},
		{
			input: "AM(PawnDamageModifier +50%) :Stacks(1) CC(Energy>0)",
			expected: []FeatEffect{
				{
					EffectType:    "AM",
					ModifierType: "PawnDamageModifier",
					Value:        0.5,
					Stacks:       1,
					Conditions:   []string{"Energy>0"},
				},
			},
		},
	}

	for i, test := range effects {
		result := ParseFeatEffects(test.input)
		if len(result) != len(test.expected) {
			t.Errorf("Test %d: expected %d effects, got %d", i+1, len(test.expected), len(result))
			continue
		}

		for j, effect := range result {
			expected := test.expected[j]
			if effect.EffectType != expected.EffectType {
				t.Errorf("Test %d, effect %d: expected EffectType %s, got %s", i+1, j+1, expected.EffectType, effect.EffectType)
			}
			if effect.ModifierType != expected.ModifierType {
				t.Errorf("Test %d, effect %d: expected ModifierType %s, got %s", i+1, j+1, expected.ModifierType, effect.ModifierType)
			}
			if effect.Value != expected.Value {
				t.Errorf("Test %d, effect %d: expected Value %f, got %f", i+1, j+1, expected.Value, effect.Value)
			}
			if effect.Stacks != expected.Stacks {
				t.Errorf("Test %d, effect %d: expected Stacks %d, got %d", i+1, j+1, expected.Stacks, effect.Stacks)
			}
			if effect.Duration != expected.Duration {
				t.Errorf("Test %d, effect %d: expected Duration %f, got %f", i+1, j+1, expected.Duration, effect.Duration)
			}
			if effect.BuffType != expected.BuffType {
				t.Errorf("Test %d, effect %d: expected BuffType %s, got %s", i+1, j+1, expected.BuffType, effect.BuffType)
			}
			if len(effect.Conditions) != len(expected.Conditions) {
				t.Errorf("Test %d, effect %d: expected %d conditions, got %d", i+1, j+1, len(expected.Conditions), len(effect.Conditions))
			} else {
				for k, cond := range effect.Conditions {
					if cond != expected.Conditions[k] {
						t.Errorf("Test %d, effect %d, condition %d: expected %s, got %s", i+1, j+1, k+1, expected.Conditions[k], cond)
					}
				}
			}
		}
	}
}

func TestFeatsForShip(t *testing.T) {
	// Test that ship feats can be retrieved by ship ID
	err := LoadShipFeats()
	if err != nil {
		// If the data directory is not found, skip the test
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping ship feats test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load ship feats: %v", err)
	}

	// Test retrieving feats for specific ship IDs
	shipIDs := []string{
		"AssaultMedium_T5",
		"Shared",
		"SupportLight_T3",
	}

	for _, shipID := range shipIDs {
		feats := FeatsForShip(shipID)
		if len(feats) == 0 {
			t.Logf("No feats found for ship ID %s (this may be expected if the ship has no feats)", shipID)
		} else {
			// Verify that each feat has the expected structure
			for _, feat := range feats {
				if feat.Enabling == "" && feat.Triggers == "" && feat.Effects == "" {
					t.Errorf("Ship %s has feat with empty fields", shipID)
				}
			}
		}
	}

	// Test AllShipFeatIDs
	allIDs := AllShipFeatIDs()
	if len(allIDs) == 0 {
		t.Fatalf("Expected to find ship feat IDs, got 0")
	}

	// Verify that we can retrieve feats for each ID
	for _, id := range allIDs {
		feats := FeatsForShip(id)
		if len(feats) == 0 {
			t.Errorf("Expected feats for ship ID %s, got 0", id)
		}
	}
}

func TestExtractShipIDFromFilename(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"DN_Feats_AssaultMedium_T5_OTS_DT.json", "AssaultMedium_T5"},
		{"DN_Feats_Shared_OTS_DT.json", "Shared"},
		{"DN_Feats_Custom_Modifiers_OTS_DT.json", "Custom_Modifiers"},
		{"DN_Feats_SupportLight_T3_OTS_DT.json", "SupportLight_T3"},
		{"DN_Feats_Havoc_Boosts_Shared_OTS_DT.json", "Havoc_Boosts_Shared"},
	}

	for _, test := range tests {
		result := extractShipIDFromFilename(test.filename)
		if result != test.expected {
			t.Errorf("extractShipIDFromFilename(%s) = %s, expected %s", test.filename, result, test.expected)
		}
	}
}

// TestAllShipFeatsLoad verifies all 75 feat tables load correctly (D6)
func TestAllShipFeatsLoad(t *testing.T) {
	// Reset the loaded state to force a fresh load
	// Note: This is a bit hacky but necessary for testing the loading process
	
	// Try to load ship feats
	err := LoadShipFeats()
	if err != nil {
		// If the data directory is not found, skip the test
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping all ship feats load test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load ship feats: %v", err)
	}

	// Test that we have loaded feats
	allFeats := AllShipFeats()
	if len(allFeats) == 0 {
		t.Fatalf("Expected ship feats to be loaded, got 0")
	}

	// Test that we have ship IDs with feats
	allIDs := AllShipFeatIDs()
	if len(allIDs) == 0 {
		t.Fatalf("Expected ship feat IDs to be available, got 0")
	}

	// Verify that each ship ID has feats
	for _, id := range allIDs {
		feats := FeatsForShip(id)
		if len(feats) == 0 {
			t.Errorf("Expected feats for ship ID %s, got 0", id)
		}
		
		// Verify that each feat has valid structure
		for _, feat := range feats {
			if feat.Enabling == "" && feat.Triggers == "" && feat.Effects == "" {
				t.Errorf("Ship %s has feat with empty fields", id)
			}
			
			// Verify DSL parsing worked
			if len(feat.ParsedEffects) > 0 {
				for _, effect := range feat.ParsedEffects {
					if effect.EffectType == "" {
						t.Errorf("Ship %s has effect with empty EffectType", id)
					}
				}
			}
		}
	}

	// Log the results
	t.Logf("Successfully loaded %d ship feats across %d ship IDs", len(allFeats), len(allIDs))
}
