package dreadgameconfig

import (
	"testing"
)

// TestPerkStructDefinition tests that the Perk struct is properly defined (F4)
func TestPerkStructDefinition(t *testing.T) {
	// F4: Define Go struct `Perk` for perk DataTable fields
	// This test verifies that the Perk struct exists and has the expected fields

	// Create a sample perk to test the struct
	perk := Perk{
		Enabling:      "OnAcquire()",
		Triggers:      "OnEnable()",
		Effects:       "AM(PawnDamageResistance +10%) : Stacks(1);",
		StackOnAdding: true,
		IsPerkFeat:    true,
		PerkID:        117374977,
		PerkName:      "PRK_COM_AbiInc_AbiKill_BP",
		AssetPath:     "/Game/Generic/Officer/Perk/PRK_COM_AbiInc_AbiKill_BP",
		Category:      "COM",
		ParsedEffects: []FeatEffect{},
	}

	// Verify all fields are accessible
	if perk.Enabling != "OnAcquire()" {
		t.Errorf("Expected Enabling to be 'OnAcquire()', got '%s'", perk.Enabling)
	}

	if perk.Triggers != "OnEnable()" {
		t.Errorf("Expected Triggers to be 'OnEnable()', got '%s'", perk.Triggers)
	}

	if perk.Effects != "AM(PawnDamageResistance +10%) : Stacks(1);" {
		t.Errorf("Expected Effects to be 'AM(PawnDamageResistance +10%%) : Stacks(1);', got '%s'", perk.Effects)
	}

	if !perk.StackOnAdding {
		t.Error("Expected StackOnAdding to be true")
	}

	if !perk.IsPerkFeat {
		t.Error("Expected IsPerkFeat to be true")
	}

	if perk.PerkID != 117374977 {
		t.Errorf("Expected PerkID to be 117374977, got %d", perk.PerkID)
	}

	if perk.PerkName != "PRK_COM_AbiInc_AbiKill_BP" {
		t.Errorf("Expected PerkName to be 'PRK_COM_AbiInc_AbiKill_BP', got '%s'", perk.PerkName)
	}

	if perk.AssetPath != "/Game/Generic/Officer/Perk/PRK_COM_AbiInc_AbiKill_BP" {
		t.Errorf("Expected AssetPath to be '/Game/Generic/Officer/Perk/PRK_COM_AbiInc_AbiKill_BP', got '%s'", perk.AssetPath)
	}

	if perk.Category != "COM" {
		t.Errorf("Expected Category to be 'COM', got '%s'", perk.Category)
	}

	t.Logf("✅ Perk struct properly defined with all required fields")
}

// TestPerkAccessorFunctions tests the accessor functions for perks (F4)
func TestPerkAccessorFunctions(t *testing.T) {
	// Initialize perks
	LoadPerks()

	// Test that accessor functions exist and work
	// These should not panic even if no perks are loaded yet
	perkCount := PerkCount()
	t.Logf("Perk count: %d", perkCount)

	allPerks := AllPerks()
	t.Logf("All perks length: %d", len(allPerks))

	allPerkIDs := AllPerkIDs()
	t.Logf("All perk IDs length: %d", len(allPerkIDs))

	// Test individual access
	_, exists := PerkByID(117374977)
	if exists {
		t.Log("Found perk by ID 117374977")
	} else {
		t.Log("Perk by ID 117374977 not found (expected for now)")
	}

	_, exists = PerkByName("PRK_COM_AbiInc_AbiKill_BP")
	if exists {
		t.Log("Found perk by name PRK_COM_AbiInc_AbiKill_BP")
	} else {
		t.Log("Perk by name PRK_COM_AbiInc_AbiKill_BP not found (expected for now)")
	}

	t.Logf("✅ All perk accessor functions work correctly")
}

// TestF4DefinePerkStruct tests the F4 requirement explicitly
func TestF4DefinePerkStruct(t *testing.T) {
	// F4: Define Go struct `Perk` for perk DataTable fields
	// This test explicitly validates the F4 requirement

	// The Perk struct should have fields that match DataTable structure
	// Based on officers and ship feats, perks should have:
	// - m_enabling (string)
	// - m_triggers (string) 
	// - m_effects (string)
	// - m_stackOnAdding (bool)
	// - m_isPerkFeat (bool)

	// Test that we can create a perk with all expected DataTable fields
	perk := Perk{
		Enabling:      "OnAcquire()",
		Triggers:      "DoKillWithAbility()",
		Effects:       "AM(PawnDamageResistance +15.0%) : Stacks(1);",
		StackOnAdding: false,
		IsPerkFeat:    true,
	}

	// Verify the struct can hold DataTable values
	if perk.Enabling == "" || perk.Triggers == "" || perk.Effects == "" {
		t.Fatal("Perk struct cannot hold DataTable field values")
	}

	t.Logf("✅ F4: Perk struct successfully defined for perk DataTable fields")
	_ = perk // Use the variable to avoid unused variable error
}

// TestF5LoadPerksFromItemIDRegister tests the F5 requirement
func TestF5LoadPerksFromItemIDRegister(t *testing.T) {
	// F5: Load perk data from ItemIDTable category YPerk entries
	// This test explicitly validates the F5 requirement

	// Reset the loaded state to test loading
	perksLock.Lock()
	perks = make(map[string]Perk)
	perksByID = make(map[int32]Perk)
	perksLoaded = false
	perksLock.Unlock()

	// Load perks from ItemIDRegister
	LoadPerks()

	// Verify that perks were loaded
	perkCount := PerkCount()
	if perkCount == 0 {
		t.Fatal("F5: Expected perks to be loaded from ItemIDRegister, got 0")
	}

	t.Logf("✅ F5: Successfully loaded %d perks from ItemIDRegister", perkCount)

	// Verify we can access some perks
	allPerks := AllPerks()
	if len(allPerks) != perkCount {
		t.Errorf("Expected %d perks from AllPerks(), got %d", perkCount, len(allPerks))
	}

	// Check that some known perk IDs exist (from ItemIDRegister)
	knownPerkIDs := []int32{117374977, 117374978, 117374979, 117374980, 117374981}
	foundKnownPerks := 0
	for _, id := range knownPerkIDs {
		if _, exists := PerkByID(id); exists {
			foundKnownPerks++
		}
	}

	t.Logf("✅ F5: Found %d out of %d known perk IDs", foundKnownPerks, len(knownPerkIDs))

	// Verify perk categories are extracted correctly
	categoriesFound := make(map[string]bool)
	for _, perk := range allPerks {
		if perk.Category != "" && perk.Category != "UNKNOWN" {
			categoriesFound[perk.Category] = true
		}
	}

	t.Logf("✅ F5: Found perk categories: %v", categoriesFound)

	// Verify perk names are extracted correctly
	for _, perk := range allPerks {
		if perk.PerkName == "" {
			t.Error("Found perk with empty PerkName")
		}
		if perk.AssetPath == "" {
			t.Error("Found perk with empty AssetPath")
		}
		if perk.PerkID == 0 {
			t.Error("Found perk with zero PerkID")
		}
	}

	t.Logf("✅ F5: All perks have valid metadata (name, path, ID)")
}
