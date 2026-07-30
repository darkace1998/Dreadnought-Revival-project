package main

import (
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

func TestLoadPlayerSeasonProgress(t *testing.T) {
	pid := defaultMmogPlayerPID

	// Initially should have no season progress
	progress := loadPlayerSeasonProgress(pid)
	if len(progress) != 0 {
		t.Fatalf("expected 0 season progress initially, got %d", len(progress))
	}

	// Manually insert some season progress for testing
	db := currentMmogPlayerStateDB()
	if db == nil {
		t.Skip("no database available")
	}

	// Insert test season progress
	_, _ = db.Exec(`INSERT OR REPLACE INTO player_season_progress(user_id, season_id, xp, level) VALUES(?, ?, ?, ?)`,
		pid, "season_1", 2500, 1)
	_, _ = db.Exec(`INSERT OR REPLACE INTO player_season_progress(user_id, season_id, xp, level) VALUES(?, ?, ?, ?)`,
		pid, "season_2", 7500, 2)

	progress = loadPlayerSeasonProgress(pid)
	if len(progress) != 2 {
		t.Fatalf("expected 2 season progress entries, got %d", len(progress))
	}

	// Verify the progress was loaded correctly
	progressMap := make(map[string]playerSeasonProgress)
	for _, p := range progress {
		progressMap[p.seasonID] = p
	}

	if p, ok := progressMap["season_1"]; !ok {
		t.Error("expected season_1 progress to be loaded")
	} else {
		if p.xp != 2500 {
			t.Errorf("expected season_1 xp=2500, got %d", p.xp)
		}
		if p.level != 1 {
			t.Errorf("expected season_1 level=1, got %d", p.level)
		}
	}

	if p, ok := progressMap["season_2"]; !ok {
		t.Error("expected season_2 progress to be loaded")
	} else {
		if p.xp != 7500 {
			t.Errorf("expected season_2 xp=7500, got %d", p.xp)
		}
		if p.level != 2 {
			t.Errorf("expected season_2 level=2, got %d", p.level)
		}
	}
}

func TestBuildMmogSeasonProgressPayload(t *testing.T) {
	pid := defaultMmogPlayerPID

	// Insert test season progress
	db := currentMmogPlayerStateDB()
	if db != nil {
		_, _ = db.Exec(`INSERT OR REPLACE INTO player_season_progress(user_id, season_id, xp, level) VALUES(?, ?, ?, ?)`,
			pid, "season_1", 3000, 1)
	}

	payload := buildMmogSeasonProgressPayloadForPlayer(pid)
	if len(payload) == 0 {
		t.Fatal("expected non-empty season progress payload")
	}

	// Verify RT field is present
	rt := protocol.ExtractStringField(payload, "RT")
	if rt != "YA_GetSeasonProgress" {
		t.Errorf("expected RT=YA_GetSeasonProgress, got %s", rt)
	}
}

func TestAppendMmogEventScoreEntry(t *testing.T) {
	progress := playerSeasonProgress{
		seasonID: "season_1",
		xp:       5000,
		level:    2,
	}

	var b []byte
	var stack []int
	b, _ = appendMmogEventScoreEntry(b, stack, progress)

	if len(b) == 0 {
		t.Fatal("expected non-empty event score entry")
	}

	// issue #47: the client's per-entry parser (FUN_142a6bdc0) only reads
	// EventID/FleetType/Score — never SeasonID/Level, which are rejected
	// silently. One entry per fleet type (1-3), each carrying the season ID
	// as EventID since we don't track per-event score separately yet.
	eventID := protocol.ExtractStringField(b, "EventID")
	if eventID != "season_1" {
		t.Errorf("expected EventID=season_1, got %s", eventID)
	}
	if protocol.ExtractStringField(b, "SeasonID") != "" {
		t.Error("SeasonID should not be sent — the client parser never reads it")
	}

	scoreMarker := appendFieldMarker("Score", 0x56)
	if !bytesContains(b, scoreMarker) {
		t.Error("expected Score field (int32) to be present in payload")
	}

	fleetTypeMarker := appendFieldMarker("FleetType", 0x56)
	if !bytesContains(b, fleetTypeMarker) {
		t.Error("expected FleetType field (int32) to be present in payload")
	}

	if bytesContains(b, appendFieldMarker("Level", 0x56)) {
		t.Error("Level should not be sent — the client parser never reads it")
	}
}

func TestAppendMmogSeasonProgressEntry(t *testing.T) {
	progress := playerSeasonProgress{
		seasonID: "season_2",
		xp:       8000,
		level:    3,
	}

	var b []byte
	var stack []int
	b, _ = appendMmogSeasonProgressEntry(b, stack, progress)

	if len(b) == 0 {
		t.Fatal("expected non-empty season progress entry")
	}

	// Verify the season ID is in the payload
	seasonID := protocol.ExtractStringField(b, "SeasonID")
	if seasonID != "season_2" {
		t.Errorf("expected SeasonID=season_2, got %s", seasonID)
	}

	// Verify XP field is present (numeric string — see appendMmogSeasonProgressEntry)
	xpMarker := appendFieldMarker("XP", 0x09)
	if !bytesContains(b, xpMarker) {
		t.Error("expected XP field (string) to be present in payload")
	}

	// Verify level field is present (numeric string — see appendMmogSeasonProgressEntry)
	levelMarker := appendFieldMarker("Level", 0x09)
	if !bytesContains(b, levelMarker) {
		t.Error("expected Level field (string) to be present in payload")
	}
}

func TestSeasonProgressInPlayerGet(t *testing.T) {
	pid := defaultMmogPlayerPID

	// Insert test season progress
	db := currentMmogPlayerStateDB()
	if db != nil {
		_, _ = db.Exec(`INSERT OR REPLACE INTO player_season_progress(user_id, season_id, xp, level) VALUES(?, ?, ?, ?)`,
			pid, "season_1", 4000, 1)
	}

	payload := buildMmogPlayerGetPayload(pid)
	if len(payload) == 0 {
		t.Fatal("expected non-empty player get payload")
	}

	// Verify SeasonProgress array is present
	seasonProgressMarker := appendFieldMarker("SeasonProgress", 0x0d) // 0x0d = array type
	if !bytesContains(payload, seasonProgressMarker) {
		t.Error("expected SeasonProgress array to be present in YA_PlayerGet payload")
	}
}

func TestSeasonDataPayload(t *testing.T) {
	payload := buildMmogSeasonDataPayload()
	if len(payload) == 0 {
		t.Fatal("expected non-empty season data payload")
	}

	// Verify RT field
	rt := protocol.ExtractStringField(payload, "RT")
	if rt != "YA_GetSeasonData" {
		t.Errorf("expected RT=YA_GetSeasonData, got %s", rt)
	}

	// Verify Seasons field is present
	seasons := protocol.ExtractStringField(payload, "Seasons")
	if seasons == "" {
		t.Error("expected Seasons field to be present")
	}

	// Verify Events field is present
	events := protocol.ExtractStringField(payload, "Events")
	if events == "" {
		t.Error("expected Events field to be present")
	}
}
