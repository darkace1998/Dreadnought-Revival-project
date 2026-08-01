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
	// Observed under Wine: launch to "Match State Changed from EnteringMap to
	// WaitingToStart" takes roughly a minute on this hardware.
	if mmogConnectPushDelay < 60*time.Second {
		t.Errorf("mmogConnectPushDelay = %s, too short for the engine to reach WaitingToStart",
			mmogConnectPushDelay)
	}
}
