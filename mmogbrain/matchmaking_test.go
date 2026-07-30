package main

import (
	"bytes"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/matchmaker"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// realEnterMatchmakingRequest is a verbatim capture of the client's quick-play
// request, taken from the mmogbrain frame log. The important part is
// GameType="ANY": the server used to reject that as an unsupported mode, so the
// player never entered the queue.
func realEnterMatchmakingRequest() []byte {
	var b []byte
	b = protocol.AppendStringField(b, "RT", "YA_EnterMatchmaking")
	b = protocol.AppendStringField(b, "Name", "*matchmaking")
	b = protocol.AppendStringField(b, "Version", "1.0001.4.13.1-1001076689+main")
	b = protocol.AppendStringField(b, "MapName", "ANY")
	b = protocol.AppendStringField(b, "GameType", "ANY")
	b = protocol.AppendStringField(b, "FleetID", "96df9f85c31747f8946682ff21b1eaf8")
	b = protocol.AppendStringField(b, "Cluster", "")
	b = protocol.AppendInt32Field(b, "MaintenanceCost", 42)
	return b
}

func TestQuickPlayEntersTheQueueRatherThanErroring(t *testing.T) {
	useTempMmogPlayerStateDB(t)

	const playerPID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	response := buildMmogEnterMatchmakingPayload("YA_EnterMatchmaking", playerPID, realEnterMatchmakingRequest())

	if bytes.Contains(response, []byte("unsupported game mode")) {
		t.Fatal(`quick play was rejected as an unsupported mode; GameType="ANY" means "any mode", not a mode named ANY`)
	}
	if !bytes.Contains(response, protocol.AppendStringField(nil, "state", "waiting")) {
		t.Fatalf("quick play did not enter the queue: %q", response)
	}

	// It has to land on a concrete mode, because the queue groups by exact mode.
	if !bytes.Contains(response, protocol.AppendStringField(nil, "gameMode", matchmaker.DefaultGameMode)) {
		t.Errorf("quick play did not resolve to %s", matchmaker.DefaultGameMode)
	}

	database := currentMmogPlayerStateDB()
	var mode, status string
	if err := database.QueryRow(`SELECT game_mode,status FROM queue_entries WHERE user_id=?`, playerPID).Scan(&mode, &status); err != nil {
		t.Fatalf("no queue row was written: %v", err)
	}
	if mode != matchmaker.DefaultGameMode || status != "waiting" {
		t.Errorf("queue row = (%s,%s), want (%s,waiting)", mode, status, matchmaker.DefaultGameMode)
	}
}

func TestWildcardGameModes(t *testing.T) {
	for _, mode := range []string{"", "ANY", "any", "*matchmaking", "ALL", "  ANY  "} {
		if !matchmaker.IsWildcardGameMode(mode) {
			t.Errorf("%q should be a wildcard", mode)
		}
	}
	for _, mode := range []string{"TDM", "Onslaught", "BC"} {
		if matchmaker.IsWildcardGameMode(mode) {
			t.Errorf("%q is a real mode, not a wildcard", mode)
		}
	}
}
