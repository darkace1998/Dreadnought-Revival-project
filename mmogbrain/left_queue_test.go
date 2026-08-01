package main

import (
	"bytes"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// Cancelling matchmaking only completes if the client is told so out of band.
// The interpreter sets matchmaking state 7 ("awaiting a cancellation response")
// the moment it sends YA_LeaveMatchmaking, and the ONLY thing that unwinds it is
// the OnLeftMatchmakingQueue delegate (interface +0x2590), broadcast exclusively
// by the dispatcher's "YA_LeftQueue" arm at 0x142a2dfc8.
func TestLeftQueuePushIsAYALeftQueueFrame(t *testing.T) {
	payload := buildMmogLeftQueuePayload("650dd79476a1484b8adcd01ac2f17354")

	if !bytes.Contains(payload, []byte("YA_LeftQueue")) {
		t.Fatal("payload is not a YA_LeftQueue frame -- no other message clears state 7")
	}
}

// PID is the one field that arm reads (0x142a2e037).
func TestLeftQueuePushCarriesThePID(t *testing.T) {
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	payload := buildMmogLeftQueuePayload(pid)

	if !bytes.Contains(payload, protocol.AppendStringField(nil, "PID", pid)) {
		t.Error("payload does not carry PID, the only field the client's arm reads")
	}
}

// PIDs on the binary protocol are UUIDs with the hyphens stripped. A hyphenated
// one would not match the player the client is tracking.
func TestLeftQueuePushNormalisesThePID(t *testing.T) {
	payload := buildMmogLeftQueuePayload("650dd794-76a1-484b-8adc-d01ac2f17354")

	if bytes.Contains(payload, []byte("650dd794-76a1")) {
		t.Error("PID went out hyphenated; mmog PIDs are 32 hex chars with no hyphens")
	}
	if !bytes.Contains(payload, []byte("650dd79476a1484b8adcd01ac2f17354")) {
		t.Error("PID was not normalised to the stripped form")
	}
}
