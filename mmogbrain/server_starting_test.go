package main

import (
	"bytes"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// The match-ready push has to carry a connectable address, or the client is
// told a match is ready but not where to go.
func TestServerStartingPayloadCarriesAddress(t *testing.T) {
	payload := buildMmogServerStartingPayload(mmogMatchmakingStatus{
		state:      "matched",
		matchID:    "match-123",
		serverIP:   "10.0.0.73",
		serverPort: 7777,
		gameMode:   "BC",
		mapName:    "Amirani",
	})

	if !bytes.Contains(payload, []byte("YA_ServerStarting")) {
		t.Fatal("payload is not a YA_ServerStarting frame")
	}
	if !bytes.Contains(payload, []byte("10.0.0.73")) {
		t.Error("payload does not carry the server IP")
	}
	// The port must be present as a numeric STRING: the client's mmog reader
	// only accepts double/int64/string through its value union, so an int32-only
	// port reads as 0.
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "Port", "7777")) {
		t.Error("payload does not carry the port as a numeric string under \"Port\"")
	}
	// The IP must reach the client under a field its connection struct exposes.
	// "Host" is the primary candidate; assert at least one known name carries it.
	hostForms := [][]byte{
		protocol.AppendStringField(nil, "Host", "10.0.0.73"),
		protocol.AppendStringField(nil, "serverHost", "10.0.0.73"),
		protocol.AppendStringField(nil, "ServerIP", "10.0.0.73"),
	}
	found := false
	for _, form := range hostForms {
		if bytes.Contains(payload, form) {
			found = true
			break
		}
	}
	if !found {
		t.Error("payload carries the IP under none of Host/serverHost/ServerIP")
	}
	if !bytes.Contains(payload, []byte("match-123")) {
		t.Error("payload does not carry the match id")
	}
}

// A push with no address must not claim a server is ready.
func TestServerStartingOmitsAddressWhenAbsent(t *testing.T) {
	payload := buildMmogServerStartingPayload(mmogMatchmakingStatus{state: "matched"})
	// No IP/port strings should appear; the connection handler also gates on
	// serverIP != "" before pushing at all, so this is belt and braces.
	if bytes.Contains(payload, protocol.AppendStringField(nil, "Host", "")) {
		t.Error("payload emitted an empty Host field")
	}
}

// currentMmogMatchmakingStatus must report "matched" with the battle-server
// address once the matchmaker has recorded an active match for the player --
// that transition is what the connection handler pushes on.
func TestMatchmakingStatusBecomesMatched(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "player-provingground"

	// A queued player reads back as waiting, with no address to connect to.
	if _, err := database.Exec(
		`INSERT INTO queue_entries(id,user_id,game_mode,tier_min,tier_max,status) VALUES(?,?,?,?,?,'waiting')`,
		"q1", pid, "BC", 1, 5); err != nil {
		t.Fatalf("insert queue entry: %v", err)
	}
	if got := currentMmogMatchmakingStatus(pid); got.state != "waiting" {
		t.Fatalf("queued player status = %q, want waiting", got.state)
	}

	// The matchmaker forms the match: an active row plus a slot for the player.
	if _, err := database.Exec(
		`INSERT INTO matches(id,game_mode,map,server_ip,server_port,status,created_at,started_at)
		 VALUES(?,?,?,?,?,'active',datetime('now'),datetime('now'))`,
		"m1", "BC", "Amirani", "10.0.0.73", 7777); err != nil {
		t.Fatalf("insert match: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO match_slots(match_id,user_id,team) VALUES(?,?,0)`, "m1", pid); err != nil {
		t.Fatalf("insert match slot: %v", err)
	}

	got := currentMmogMatchmakingStatus(pid)
	if got.state != "matched" {
		t.Fatalf("matched player status = %q, want matched", got.state)
	}
	if got.serverIP != "10.0.0.73" || got.serverPort != 7777 {
		t.Errorf("match address = %s:%d, want 10.0.0.73:7777", got.serverIP, got.serverPort)
	}
	if got.mapName != "Amirani" || got.gameMode != "BC" {
		t.Errorf("match = %s on %s, want BC on Amirani", got.gameMode, got.mapName)
	}

	// The push built from that status must be connectable.
	payload := buildMmogServerStartingPayload(got)
	if !bytes.Contains(payload, []byte("10.0.0.73")) || !bytes.Contains(payload, []byte("7777")) {
		t.Error("server-starting push built from a matched status is missing the address")
	}
}
