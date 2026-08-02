package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// readyStatus is a match that has been up long enough for YA_Connect to fire.
func readyStatus() mmogMatchmakingStatus {
	return mmogMatchmakingStatus{
		state:      "matched",
		matchID:    "match-123",
		serverIP:   "10.0.0.73",
		serverPort: 7777,
		gameMode:   "BC",
		mapName:    "Amirani",
		team:       1,
		createdAt:  time.Now().Add(-2 * mmogConnectPushDelay),
	}
}

// The client's YA_Connect arm (0x142a271f5) reads exactly these five fields, in
// this order, before building its travel URL. A missing one is not a partial
// failure -- it is a client that travels to the wrong place or nowhere.
func TestConnectPushCarriesEveryFieldTheClientReads(t *testing.T) {
	payload := buildMmogConnectPushPayload(readyStatus())

	if !bytes.Contains(payload, []byte("YA_Connect")) {
		t.Fatal("payload is not a YA_Connect frame")
	}
	for _, field := range []string{"Connect", "Team", "DediID", "Room", "PVEEvent"} {
		if !bytes.Contains(payload, []byte(field)) {
			t.Errorf("payload is missing the %q field the client reads", field)
		}
	}
}

// Connect is consumed directly as a travel URL -- the client runs
// "TRAVEL <Connect>?TEAM=<Team>" -- so it has to be bare host:port. A scheme or
// a trailing option would corrupt the URL the client builds.
func TestConnectPushAddressIsBareHostPort(t *testing.T) {
	payload := buildMmogConnectPushPayload(readyStatus())

	if !bytes.Contains(payload, protocol.AppendStringField(nil, "Connect", "10.0.0.73:7777")) {
		t.Error("Connect is not the bare host:port the client travels to")
	}
	for _, bad := range []string{"http://", "://", "?listen"} {
		if bytes.Contains(payload, []byte(bad)) {
			t.Errorf("Connect must not contain %q -- it is used verbatim as a travel URL", bad)
		}
	}
}

// Team is appended to the travel URL as "?TEAM=<Team>", and every value on this
// protocol has to be a string: the client's value union takes only
// double/int64/string, so an int32 field reads back as 0 and would silently put
// every player on team 0.
func TestConnectPushTeamIsANumericString(t *testing.T) {
	payload := buildMmogConnectPushPayload(readyStatus())

	if !bytes.Contains(payload, protocol.AppendStringField(nil, "Team", "1")) {
		t.Error("Team is not carried as the numeric string \"1\"")
	}
}

// A match with no address must not produce a travel push that sends the client
// to ":0" or to a bare port.
func TestConnectPushLeavesAddressEmptyWhenUnknown(t *testing.T) {
	payload := buildMmogConnectPushPayload(mmogMatchmakingStatus{
		state:     "matched",
		matchID:   "match-123",
		createdAt: time.Now().Add(-2 * mmogConnectPushDelay),
	})

	if !bytes.Contains(payload, protocol.AppendStringField(nil, "Connect", "")) {
		t.Error("Connect should be empty when the match has no server address")
	}
	if bytes.Contains(payload, []byte(":0")) {
		t.Error("Connect must not be built from a zero port")
	}
}

// DediID and Room are what the client echoes back in its own log line, so they
// are the verification signal that the payload shape reached it at all.
func TestConnectPushIdentifiesTheInstance(t *testing.T) {
	payload := buildMmogConnectPushPayload(readyStatus())

	for _, field := range []string{"DediID", "Room"} {
		if !bytes.Contains(payload, protocol.AppendStringField(nil, field, "match-123")) {
			t.Errorf("%s does not carry the match id", field)
		}
	}
}

// The push is held back until the battle server has had time to load, because
// YA_Connect makes the client travel immediately. Firing it the moment the
// match row appears points the client at a process still loading its map.
func TestConnectPushDelayIsLongEnoughForTheEngineToLoad(t *testing.T) {
	// Asserts the DEFAULT, not the package var, which DN_CONNECT_PUSH_DELAY can
	// legitimately override to anything an operator wants.
	t.Setenv("DN_CONNECT_PUSH_DELAY", "")
	// Observed under Wine: launch to "Match State Changed from EnteringMap to
	// WaitingToStart" takes roughly a minute cold on this hardware.
	if got := connectPushDelayFromEnv(); got < 60*time.Second {
		t.Errorf("default connect push delay = %s, too short for the engine to reach WaitingToStart", got)
	}
}

func TestConnectPushDelayIsOverridableFromTheEnvironment(t *testing.T) {
	t.Setenv("DN_CONNECT_PUSH_DELAY", "20s")
	if got := connectPushDelayFromEnv(); got != 20*time.Second {
		t.Errorf("delay = %s, want the 20s override", got)
	}
}

// A malformed value must not silently become zero, which would push YA_Connect
// instantly and travel the client into a server that has not loaded.
func TestConnectPushDelayIgnoresGarbage(t *testing.T) {
	t.Setenv("DN_CONNECT_PUSH_DELAY", "soon")
	if got := connectPushDelayFromEnv(); got != 75*time.Second {
		t.Errorf("delay = %s, want the 75s default when the value does not parse", got)
	}
}

// The travel push has two gates and they are not interchangeable: "ready" is a
// fact reported by the battle server, "delay" is a guess. A match that has just
// formed must open neither.
func TestConnectPushGateWaitsWithoutEvidence(t *testing.T) {
	open, gate := connectPushGateOpen(mmogMatchmakingStatus{
		state:     "matched",
		matchID:   "match-123",
		serverIP:  "10.0.0.73",
		createdAt: time.Now(),
	})
	if open {
		t.Fatalf("gate %q opened for a match formed a moment ago", gate)
	}
}

// The point of the readiness poll: when the control plane says the engine is
// hosting, the client travels then, not after DN_CONNECT_PUSH_DELAY. Everything
// between those two moments used to be dead time on "Battle server starting".
func TestConnectPushGateOpensImmediatelyOnReportedReadiness(t *testing.T) {
	open, gate := connectPushGateOpen(mmogMatchmakingStatus{
		state:       "matched",
		matchID:     "match-123",
		serverIP:    "10.0.0.73",
		createdAt:   time.Now(),
		serverReady: true,
	})
	if !open {
		t.Fatal("gate stayed shut although the battle server reported ready")
	}
	if gate != "ready" {
		t.Errorf("gate = %q, want %q", gate, "ready")
	}
}

// The fallback has to survive: game-manager implements no readiness route, so
// serverReady is false forever there and only the elapsed delay can let the
// player travel.
func TestConnectPushGateStillFallsBackToTheDelay(t *testing.T) {
	open, gate := connectPushGateOpen(readyStatus())
	if !open {
		t.Fatal("gate stayed shut after the delay elapsed")
	}
	if gate != "delay" {
		t.Errorf("gate = %q, want %q", gate, "delay")
	}
}

// Readiness is about the engine, not about having an address. A match with no
// server address must never produce a travel push, however ready it claims to
// be -- the client would travel to ":0".
func TestConnectPushGateRejectsAnAddresslessMatch(t *testing.T) {
	open, _ := connectPushGateOpen(mmogMatchmakingStatus{
		state:       "matched",
		matchID:     "match-123",
		createdAt:   time.Now().Add(-2 * mmogConnectPushDelay),
		serverReady: true,
	})
	if open {
		t.Fatal("gate opened for a match with no server address")
	}
}
