package main

import (
	"bytes"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/matchmaker"
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

// GameModes must be a DIRECT child of the YA_GetGameConfigData document, not
// only nested inside "result".
//
// That response's own handler calls GetGameModesData (FUN_142a4ca40) on the
// document it parsed, and that function resolves the array as a direct child
// (FUN_140237c30, then the child count at +0x20). It does not descend into
// "result" the way the scalar reader used for MaxSquadSize does. With the array
// nested only, the client logged "GetGameModesData: Game modes list contains <0>
// items" and the player had NO selectable game mode -- Play could not start a
// match at all.
func TestGameConfigCarriesGameModesAtRoot(t *testing.T) {
	payload := buildMmogGameConfigDataPayload()

	// The root-level array must appear BEFORE the "result" object opens, which
	// is what makes it a sibling of RT rather than a child of result.
	modesAt := bytes.Index(payload, appendFieldMarker("GameModes", 0x0d))
	resultAt := bytes.Index(payload, appendFieldMarker("result", 0x0c))
	if modesAt < 0 {
		t.Fatal("YA_GetGameConfigData has no GameModes array at all")
	}
	if resultAt < 0 {
		t.Fatal("YA_GetGameConfigData has no result object")
	}
	if modesAt > resultAt {
		t.Error("GameModes appears only inside result; the config handler reads it as a DIRECT child and will see zero modes")
	}

	// Every configured mode has to be present, or it cannot be picked.
	for _, mode := range matchmaker.GameModeConfigs() {
		if !bytes.Contains(payload, protocol.AppendStringField(nil, "Name", mode.Name)) {
			t.Errorf("game config is missing mode %q", mode.Name)
		}
	}
	if len(matchmaker.GameModeConfigs()) == 0 {
		t.Error("no game modes configured; the client would have an empty Play list")
	}
}

// The proving ground is Bootcamp, and it must be offered and be solo-startable.
func TestProvingGroundModeIsOfferedAndSolo(t *testing.T) {
	var bc *matchmaker.GameModeConfig
	for _, mode := range matchmaker.GameModeConfigs() {
		if mode.Name == "BC" {
			m := mode
			bc = &m
		}
	}
	if bc == nil {
		t.Fatal("BC (Bootcamp / proving ground) is not offered to the client")
	}
	if bc.TeamSize != 1 {
		t.Errorf("BC TeamSize = %d, want 1 so it can start solo", bc.TeamSize)
	}
	if !matchmaker.ValidGameMode("BC") {
		t.Error("BC is not accepted by the matchmaker, so entering its queue would be rejected")
	}
}

// The queue response must tell the client it SUCCEEDED, or the client treats
// registration as failed and drops straight back to Idle.
//
// Live evidence: "Failed to register for matchmaking. Reason: []" followed by
// SetMatchmakingState | Idle, for every mode tried. The empty bracket is the
// tell -- the interpreter maps a reason token onto a UI message and had nothing
// to map, because neither Success nor Reason was being sent.
func TestMatchmakingResponseReportsSuccess(t *testing.T) {
	payload := buildMmogMatchmakingPayload("YA_EnterMatchmaking", mmogMatchmakingStatus{
		entryID:  "q-1",
		state:    "waiting",
		gameMode: "BC",
	})

	// Numeric string, not a bool: both readers this client uses accept that
	// form, and where a bool node keeps its payload is not established.
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "Success", "1")) {
		t.Error("queue response does not report Success=1; the client will treat it as a failed registration")
	}
	if !bytes.Contains(payload, appendFieldMarker("Reason", 0x09)) {
		t.Error("queue response has no Reason field")
	}
	if !bytes.Contains(payload, appendFieldMarker("WaitTime", 0x09)) {
		t.Error("queue response has no WaitTime field")
	}
}

// A refusal must name a reason the client understands, or the player is shown a
// generic error that tells them nothing.
func TestMatchmakingErrorUsesAKnownReasonToken(t *testing.T) {
	known := map[string]bool{
		"invalid_version": true, "invalid_player": true, "invalid_fleet": true,
		"invalid_gametype": true, "fleet_on_maintenance": true, "map_unavailable": true,
	}
	payload := buildMmogMatchmakingErrorPayload("YA_EnterMatchmaking", 2, "invalid_gametype", "unsupported game mode")

	if !bytes.Contains(payload, protocol.AppendStringField(nil, "Success", "0")) {
		t.Error("error response does not report Success=0")
	}
	found := ""
	for token := range known {
		if bytes.Contains(payload, protocol.AppendStringField(nil, "Reason", token)) {
			found = token
		}
	}
	if found == "" {
		t.Error("error response carries no reason token the client's interpreter can map")
	}
}
