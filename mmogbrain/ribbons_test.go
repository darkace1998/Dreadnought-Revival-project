package main

import (
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

func TestLoadPlayerRibbons(t *testing.T) {
	pid := defaultMmogPlayerPID

	// Initially should have no ribbons
	ribbons := loadPlayerRibbons(pid)
	if len(ribbons) != 0 {
		t.Fatalf("expected 0 ribbons initially, got %d", len(ribbons))
	}

	// Manually insert some ribbons for testing
	db := currentMmogPlayerStateDB()
	if db == nil {
		t.Skip("no database available")
	}

	// Insert test ribbons directly
	_, _ = db.Exec(`INSERT OR REPLACE INTO player_ribbons(user_id, ribbon_type, count) VALUES(?, ?, ?)`, pid, "first_blood", 1)
	_, _ = db.Exec(`INSERT OR REPLACE INTO player_ribbons(user_id, ribbon_type, count) VALUES(?, ?, ?)`, pid, "combat_efficiency", 2)

	ribbons = loadPlayerRibbons(pid)
	if len(ribbons) != 2 {
		t.Fatalf("expected 2 ribbons, got %d", len(ribbons))
	}

	// Verify the ribbons were loaded correctly
	ribbonMap := make(map[string]int32)
	for _, r := range ribbons {
		ribbonMap[r.ribbonType] = r.count
	}

	if ribbonMap["first_blood"] != 1 {
		t.Errorf("expected first_blood count=1, got %d", ribbonMap["first_blood"])
	}
	if ribbonMap["combat_efficiency"] != 2 {
		t.Errorf("expected combat_efficiency count=2, got %d", ribbonMap["combat_efficiency"])
	}
}

func TestBuildMmogRibbonsPayload(t *testing.T) {
	pid := defaultMmogPlayerPID

	// Insert test ribbons
	db := currentMmogPlayerStateDB()
	if db != nil {
		_, _ = db.Exec(`INSERT OR REPLACE INTO player_ribbons(user_id, ribbon_type, count) VALUES(?, ?, ?)`, pid, "kill_streak", 3)
	}

	payload := buildMmogRibbonsPayload(pid)
	if len(payload) == 0 {
		t.Fatal("expected non-empty ribbons payload")
	}

	// Verify RT field is present
	rt := protocol.ExtractStringField(payload, "RT")
	if rt != "YA_GetRibbons" {
		t.Errorf("expected RT=YA_GetRibbons, got %s", rt)
	}
}

func TestAppendMmogRibbonEntry(t *testing.T) {
	ribbon := playerRibbon{
		ribbonType: "combat_efficiency",
		count:      5,
	}

	var b []byte
	var stack []int
	b, _ = appendMmogRibbonEntry(b, stack, ribbon)

	if len(b) == 0 {
		t.Fatal("expected non-empty ribbon entry")
	}

	// Verify the ribbon type is in the payload
	ribbonType := protocol.ExtractStringField(b, "Type")
	if ribbonType != "combat_efficiency" {
		t.Errorf("expected Type=combat_efficiency, got %s", ribbonType)
	}

	// Verify count field is present by checking for the field marker
	// (field name "Count" followed by type 0x56 for int32)
	countMarker := appendFieldMarker("Count", 0x56)
	if !bytesContains(b, countMarker) {
		t.Error("expected Count field (int32) to be present in payload")
	}

	// Verify name is included from ribbonThresholds
	name := protocol.ExtractStringField(b, "Name")
	if name != "Combat Efficiency" {
		t.Errorf("expected Name='Combat Efficiency', got %s", name)
	}
}

func bytesContains(haystack, needle []byte) bool {
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}

func TestRibbonThresholds(t *testing.T) {
	// Verify all 12 ribbon types are defined
	if len(ribbonThresholds) != 12 {
		t.Errorf("expected 12 ribbon types, got %d", len(ribbonThresholds))
	}

	// Verify specific ribbons exist
	expectedRibbons := []string{
		"combat_efficiency",
		"kill_streak",
		"unstoppable",
		"survivor",
		"first_blood",
		"avenger",
		"team_player",
		"marksman",
		"close_quarters",
		"support_star",
		"defender",
		"berserker",
	}

	for _, expected := range expectedRibbons {
		if _, ok := ribbonThresholds[expected]; !ok {
			t.Errorf("expected ribbon type %q to be defined", expected)
		}
	}

	// Verify each ribbon has a name
	for key, ribbon := range ribbonThresholds {
		if ribbon.name == "" {
			t.Errorf("ribbon %q has empty name", key)
		}
	}
}
