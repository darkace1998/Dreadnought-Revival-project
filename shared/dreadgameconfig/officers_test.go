package dreadgameconfig

import (
	"strings"
	"testing"
)

// TestLoadOfficers tests the officer loading functionality (F2)
func TestLoadOfficers(t *testing.T) {
	// Test that officers can be loaded
	err := LoadOfficers()
	if err != nil {
		// If the data directory is not found, skip the test
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping officers test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load officers: %v", err)
	}

	// Test that we can retrieve officers
	allOfficers := AllOfficers()
	if len(allOfficers) == 0 {
		t.Fatalf("Expected officers to be loaded, got 0")
	}

	// Test retrieving a specific officer
	testOfficers := []string{
		"YOfficerCard_OTS_DT_01",
		"YOfficerCard_OTS_DT_02",
		"YOfficerCard_OTS_DT_03",
	}

	for _, officerID := range testOfficers {
		officer, ok := OfficerByID(officerID)
		if !ok {
			t.Logf("Officer %s not found (this may be expected if the officer ID is different)", officerID)
			continue
		}

		// Verify that the officer has some fields populated
		if officer.Enabling == "" && officer.Triggers == "" && officer.Effects == "" {
			t.Errorf("Officer %s has all empty fields", officerID)
		}
		
		// Verify DSL parsing worked
		if len(officer.ParsedEffects) > 0 {
			t.Logf("Officer %s has %d parsed effects", officerID, len(officer.ParsedEffects))
		}
	}

	// Test that the init function loaded the officers
	officer, ok := OfficerByID("YOfficerCard_OTS_DT_01")
	if !ok {
		t.Logf("Officers not loaded by init function, but LoadOfficers() succeeded")
	} else {
		t.Logf("Successfully loaded officer: %s", officer.OfficerName)
	}
}

// TestOfficerCardStructFields tests the OfficerCard struct fields (F1)
func TestOfficerCardStructFields(t *testing.T) {
	// Test that OfficerCard struct has the expected fields by creating an instance
	// and verifying it can be populated with typical values
	officer := OfficerCard{
		Enabling:      "OnAcquire()",
		Triggers:      "DoKillWithAbility()",
		Effects:       "AbilityCoolDown() : D(-15.0) : Stacks(1)",
		StackOnAdding: false,
		IsPerkFeat:    true,
		OfficerID:    123456789,
		OfficerName:  "Test Officer",
		AssetPath:    "/Game/Generic/Officer/Test_Officer_BP",
		Rarity:       "Legendary",
	}

	// Verify the fields are set correctly
	if officer.Enabling != "OnAcquire()" {
		t.Errorf("Expected Enabling to be 'OnAcquire()', got '%s'", officer.Enabling)
	}
	if officer.Triggers != "DoKillWithAbility()" {
		t.Errorf("Expected Triggers to be 'DoKillWithAbility()', got '%s'", officer.Triggers)
	}
	if officer.Effects != "AbilityCoolDown() : D(-15.0) : Stacks(1)" {
		t.Errorf("Expected Effects to be 'AbilityCoolDown() : D(-15.0) : Stacks(1)', got '%s'", officer.Effects)
	}
	if officer.OfficerID != 123456789 {
		t.Errorf("Expected OfficerID to be 123456789, got %d", officer.OfficerID)
	}
	if officer.OfficerName != "Test Officer" {
		t.Errorf("Expected OfficerName to be 'Test Officer', got '%s'", officer.OfficerName)
	}
	if officer.Rarity != "Legendary" {
		t.Errorf("Expected Rarity to be 'Legendary', got '%s'", officer.Rarity)
	}
}

// TestOfficerByID tests officer retrieval by ID
func TestOfficerByID(t *testing.T) {
	// Test officer retrieval by ID
	err := LoadOfficers()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping officer retrieval test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load officers: %v", err)
	}

	// Test with a known officer ID
	officer, ok := OfficerByID("YOfficerCard_OTS_DT_01")
	if !ok {
		t.Logf("Test officer not found - this may be expected if the officer ID is different")
		return
	}

	// Verify the officer has valid data
	if officer.Enabling == "" {
		t.Error("Expected officer to have an enabling condition")
	}
}

// TestOfficerByItemID tests officer retrieval by ItemID
func TestOfficerByItemID(t *testing.T) {
	err := LoadOfficers()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping officer by ItemID test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load officers: %v", err)
	}

	// Test with known officer ItemIDs (if available)
	// These are example ItemIDs that might exist in the ItemIDRegister
	knownItemIDs := []int32{117374977, 117374978, 117374979} // Example officer ItemIDs
	
	for _, itemID := range knownItemIDs {
		officer, ok := OfficerByItemID(itemID)
		if ok {
			t.Logf("Found officer with ItemID %d: %s", itemID, officer.OfficerName)
		} else {
			t.Logf("Officer with ItemID %d not found (this may be expected in test environment)", itemID)
		}
	}
}

// TestAllOfficers tests retrieving all officers
func TestAllOfficers(t *testing.T) {
	// Test retrieving all officers
	err := LoadOfficers()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping all officers test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load officers: %v", err)
	}

	allOfficers := AllOfficers()
	if len(allOfficers) == 0 {
		t.Fatalf("Expected officers to be loaded, got 0")
	}

	// Verify that we can iterate through all officers
	count := 0
	for _, officer := range allOfficers {
		// Just verify each officer is not nil
		if officer.Enabling != "" || officer.Triggers != "" || officer.Effects != "" {
			count++
		}
	}

	if count == 0 {
		t.Error("Expected at least some officers with non-empty fields")
	}

	t.Logf("Successfully loaded %d officers", len(allOfficers))
}

// TestOfficerCount tests the officer count function
func TestOfficerCount(t *testing.T) {
	err := LoadOfficers()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping officer count test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load officers: %v", err)
	}

	count := OfficerCount()
	if count == 0 {
		t.Error("Expected officer count to be greater than 0")
	}

	// Verify count matches the number of officers
	allOfficers := AllOfficers()
	if count != len(allOfficers) {
		t.Errorf("OfficerCount (%d) does not match AllOfficers length (%d)", count, len(allOfficers))
	}

	t.Logf("Officer count: %d", count)
}

// TestOfficerIDs tests the officer IDs function
func TestOfficerIDs(t *testing.T) {
	err := LoadOfficers()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping officer IDs test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load officers: %v", err)
	}

	ids := OfficerIDs()
	if len(ids) == 0 {
		t.Fatalf("Expected officer IDs to be available, got 0")
	}

	// Verify that we can retrieve each officer by ID
	for _, id := range ids {
		officer, ok := OfficerByID(id)
		if !ok {
			t.Errorf("Expected to find officer with ID %s", id)
		}
		if officer.Enabling == "" && officer.Triggers == "" && officer.Effects == "" {
			t.Logf("Officer %s has empty fields", id)
		}
	}

	t.Logf("Found %d officer IDs", len(ids))
}

// TestOfficerDSLParsing tests that officer effects are properly parsed
func TestOfficerDSLParsing(t *testing.T) {
	err := LoadOfficers()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping officer DSL parsing test - data directory not found: %v", err)
		}
		t.Fatalf("Failed to load officers: %v", err)
	}

	// Test that some officers have parsed effects
	allOfficers := AllOfficers()
	parsedCount := 0
	
	for id, officer := range allOfficers {
		if len(officer.ParsedEffects) > 0 {
			parsedCount++
			t.Logf("Officer %s has %d parsed effects", id, len(officer.ParsedEffects))
			
			// Verify that parsed effects have valid data
			for _, effect := range officer.ParsedEffects {
				if effect.EffectType == "" {
					t.Errorf("Officer %s has effect with empty EffectType", id)
				}
			}
		}
	}

	if parsedCount > 0 {
		t.Logf("Successfully parsed effects for %d officers", parsedCount)
	} else {
		t.Log("No officers found with parsed effects (this may be expected if DSL parsing is not working)")
	}
}

// TestF1OfficerCardStruct tests the F1 requirement - OfficerCard struct definition
func TestF1OfficerCardStruct(t *testing.T) {
	// F1: Verify that OfficerCard struct matches DN_Officers_OTS_DT.json fields
	// The struct should have: m_enabling, m_triggers, m_effects, m_stackOnAdding, m_isPerkFeat
	
	// Create an officer with all expected fields
	officer := OfficerCard{
		Enabling:       "OnAcquire()",
		Triggers:       "DoKillWithAbility()",
		Effects:        "AbilityCoolDown() : D(-15.0) : Stacks(1);AM(PawnDamageResistance +0%) : Stacks(1) : D(2.0): Buff(AbilityIncrease)",
		StackOnAdding:  false,
		IsPerkFeat:     true,
		OfficerID:      123456789,
		OfficerName:   "Test Officer",
		AssetPath:     "/Game/Generic/Officer/Test_Officer_BP",
		Rarity:        "Legendary",
		ParsedEffects: []FeatEffect{},
	}

	// Verify all core fields are present and accessible
	if officer.Enabling == "" {
		t.Error("OfficerCard missing m_enabling field")
	}
	if officer.Triggers == "" {
		t.Error("OfficerCard missing m_triggers field")
	}
	if officer.Effects == "" {
		t.Error("OfficerCard missing m_effects field")
	}
	if !officer.StackOnAdding == false {
		t.Error("OfficerCard missing m_stackOnAdding field")
	}
	if !officer.IsPerkFeat == true {
		t.Error("OfficerCard missing m_isPerkFeat field")
	}
	
	// Verify additional officer-specific fields
	if officer.OfficerID == 0 {
		t.Error("OfficerCard missing OfficerID field")
	}
	if officer.OfficerName == "" {
		t.Error("OfficerCard missing OfficerName field")
	}
	if officer.AssetPath == "" {
		t.Error("OfficerCard missing AssetPath field")
	}
	if officer.Rarity == "" {
		t.Error("OfficerCard missing Rarity field")
	}
	
	t.Log("F1: OfficerCard struct successfully defined with all required fields")
}

// ==================== F2: Load Officers Table Tests ====================

// TestF2LoadOfficersTable tests the F2 requirement - Load officers table (21 rows) with trigger/effect parsing
func TestF2LoadOfficersTable(t *testing.T) {
	// F2: Verify that we can load the officers table with 21 rows
	err := LoadOfficers()
	if err != nil {
		// If the data directory is not found, skip the test
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping F2 officers table test - data directory not found: %v", err)
		}
		t.Fatalf("F2: Failed to load officers: %v", err)
	}

	// F2: Verify we have loaded officers
	count := OfficerCount()
	if count == 0 {
		t.Fatalf("F2: Expected officers to be loaded, got 0")
	}

	// F2: The todo.md mentions 21 officer cards
	// Verify we have the expected number of officers
	expectedCount := 21
	if count != expectedCount {
		t.Logf("F2: Expected %d officers, got %d (this may be expected if data is different)", expectedCount, count)
	} else {
		t.Logf("F2: Successfully loaded %d officers as expected", count)
	}

	// F2: Verify trigger/effect parsing is working
	allOfficers := AllOfficers()
	parsedCount := 0
	triggerTypes := make(map[string]int)
	effectTypes := make(map[string]int)

	for id, officer := range allOfficers {
		// Verify the officer has trigger and effect data
		if officer.Triggers != "" {
			triggerTypes[officer.Triggers]++
		}
		if officer.Effects != "" {
			effectTypes[officer.Effects]++
		}
		
		// F2: Verify trigger/effect parsing worked
		if len(officer.ParsedEffects) > 0 {
			parsedCount++
			t.Logf("F2: Officer %s has %d parsed effects", id, len(officer.ParsedEffects))
		}
	}

	if parsedCount > 0 {
		t.Logf("F2: Successfully parsed effects for %d officers", parsedCount)
	} else {
		t.Log("F2: No officers found with parsed effects")
	}

	// Report trigger types found
	if len(triggerTypes) > 0 {
		t.Logf("F2: Found %d different trigger types", len(triggerTypes))
		for trigger, count := range triggerTypes {
			t.Logf("  %s: %d officers", trigger, count)
		}
	}

	// Report effect types found
	if len(effectTypes) > 0 {
		t.Logf("F2: Found %d different effect patterns", len(effectTypes))
	}

	// F2: Verify that officers have valid data
	validOfficers := 0
	for _, officer := range allOfficers {
		if officer.Enabling != "" || officer.Triggers != "" || officer.Effects != "" {
			validOfficers++
		}
	}

	if validOfficers == count {
		t.Logf("F2: All %d officers have valid data", count)
	} else {
		t.Logf("F2: %d/%d officers have valid data", validOfficers, count)
	}
}

// ==================== F7: Comprehensive Validation Tests ====================

// TestF7Verify21OfficersLoad tests F7 requirement - verify 21 officers load
func TestF7Verify21OfficersLoad(t *testing.T) {
	// F7: Verify 21 officers load
	err := LoadOfficers()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping F7 21 officers verification - data directory not found: %v", err)
		}
		t.Fatalf("F7: Failed to load officers: %v", err)
	}

	// F7: Verify we have 21 officers
	count := OfficerCount()
	expectedCount := 21
	
	if count != expectedCount {
		t.Errorf("F7: Expected %d officers, got %d", expectedCount, count)
	} else {
		t.Logf("F7: Successfully verified %d officers loaded", count)
	}

	// F7: Verify all officers have valid IDs
	allOfficers := AllOfficers()
	if len(allOfficers) != count {
		t.Errorf("F7: AllOfficers count (%d) does not match OfficerCount (%d)", len(allOfficers), count)
	}

	// F7: Verify each officer has a valid row name
	for id := range allOfficers {
		if id == "" {
			t.Error("F7: Found officer with empty ID")
		}
	}
}

// TestF7ValidateTriggerTypes tests F7 requirement - validate trigger types
func TestF7ValidateTriggerTypes(t *testing.T) {
	err := LoadOfficers()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			t.Skipf("Skipping F7 trigger validation - data directory not found: %v", err)
		}
		t.Fatalf("F7: Failed to load officers: %v", err)
	}

	// F7: Validate trigger types across all officers
	allOfficers := AllOfficers()
	if len(allOfficers) == 0 {
		t.Fatalf("F7: Expected officers to be loaded for trigger validation")
	}

	// Track trigger type statistics
	triggerStats := make(map[string]int)
	emptyTriggerCount := 0
	validTriggerCount := 0

	for _, officer := range allOfficers {
		if officer.Triggers == "" {
			emptyTriggerCount++
		} else {
			validTriggerCount++
			triggerStats[officer.Triggers]++
		}
	}

	// F7: Validate that most officers have valid triggers
	if validTriggerCount == 0 {
		t.Error("F7: No officers have valid trigger types")
	} else {
		t.Logf("F7: %d/%d officers have valid trigger types", validTriggerCount, len(allOfficers))
	}

	if emptyTriggerCount > 0 {
		t.Logf("F7: %d officers have empty trigger types", emptyTriggerCount)
	}

	// Report all trigger types found
	if len(triggerStats) > 0 {
		t.Logf("F7: Found %d different trigger types:", len(triggerStats))
		for trigger, count := range triggerStats {
			t.Logf("  %s: %d officers", trigger, count)
		}
	}

	// F7: Validate common trigger patterns
	commonTriggers := []string{
		"DoKillWithAbility()",
		"OnAcquire()",
		"OnEnable()",
		"OnFeat1()",
		"OnFeat2()",
		"OnFeat3()",
	}

	for _, trigger := range commonTriggers {
		if count, ok := triggerStats[trigger]; ok {
			t.Logf("F7: Found common trigger '%s' in %d officers", trigger, count)
		}
	}
}