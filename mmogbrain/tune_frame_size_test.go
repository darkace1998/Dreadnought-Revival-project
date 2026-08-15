package main

import "testing"

// The ceiling on YA_Tune is the client's 32768-byte mmog RECEIVE RING, not the
// 65535 the 16-bit frame delimiter allows. This test previously budgeted 60000
// on the frame limit alone, which is how a 40,316-byte YA_Tune reached a live
// client on 2026-08-15: it passed every test here and then hung the client.
//
// What that measurement showed, and why the budget is where it is now: the
// client logged "RequestUpdateFromServer(): Requesting tuning values from mmog
// (version: 0.0.0)" and then produced NO further YTuneManager line -- no sync,
// no "backup-data" fallback, no error. The hangar never became interactive.
// An oversized frame here does not degrade; it stops the stream, exactly as the
// tech tree comments in response_builders.go have said all along and as
// main_test.go already asserts for YA_GetTechTree.
//
// The budget is deliberately well under the ring: the frame carries protocol
// overhead on top of the payload, and a response that is *nearly* fatal is not
// a safe place to sit when the failure mode is a silent hang.
const (
	clientReceiveRingBytes = 32768
	maxTuneFrameBytes      = 24000
)

func TestTunePayloadStaysWellUnderTheReceiveRing(t *testing.T) {
	payload := buildMmogTunePayload()
	t.Logf("YA_Tune payload = %d bytes (budget %d, ring %d)",
		len(payload), maxTuneFrameBytes, clientReceiveRingBytes)
	if len(payload) > maxTuneFrameBytes {
		t.Fatalf("YA_Tune is %d bytes, over the %d budget. At %d it stops the "+
			"client's stream and the hangar never loads.",
			len(payload), maxTuneFrameBytes, clientReceiveRingBytes)
	}
}

// The Stage 0 experiment: with DN_TUNE_REAL_WEAPONS=1 as much of the real
// WeaponsTune as fits goes out. The full table is ~40KB and does NOT fit, so
// truncateJSONArray cuts it on element boundaries. A partial table is still a
// valid answer to the only question being asked -- whether a non-empty table
// changes the version the client reports back.
func TestRealWeaponsTuneIsTruncatedToFit(t *testing.T) {
	t.Setenv("DN_TUNE_REAL_WEAPONS", "1")
	payload := buildMmogTunePayload()
	t.Logf("YA_Tune with real WeaponsTune = %d bytes (budget %d)",
		len(payload), maxTuneFrameBytes)

	if len(payload) > maxTuneFrameBytes {
		t.Fatalf("real WeaponsTune makes YA_Tune %d bytes, over the %d budget",
			len(payload), maxTuneFrameBytes)
	}
	// If this ever stops growing, the switch is wired to nothing and the
	// experiment reports a false negative -- the client would say "backup-data"
	// because we sent empty tables again, and we would wrongly conclude that
	// emptiness was not the cause.
	if len(payload) < 10000 {
		t.Errorf("payload is only %d bytes with the switch on; real tuning data "+
			"is not being included", len(payload))
	}
}

// truncateJSONArray must cut on element boundaries. A prefix-of-bytes cut would
// produce invalid JSON, which the client would reject in a way indistinguishable
// from the emptiness we are testing for.
func TestTruncateJSONArrayCutsOnElementBoundaries(t *testing.T) {
	const src = `[{"a":1},{"b":2},{"c":3}]`

	if got := truncateJSONArray(src, 1000); got != src {
		t.Errorf("under budget the input must be untouched, got %q", got)
	}
	got := truncateJSONArray(src, 20)
	if len(got) > 20 {
		t.Errorf("truncated to %d bytes, over the 20 budget: %q", len(got), got)
	}
	if got != `[{"a":1},{"b":2}]` {
		t.Errorf("expected two whole elements, got %q", got)
	}
	if got := truncateJSONArray(`not json`, 4); got != `[]` {
		t.Errorf("unparseable input must degrade to an empty array, got %q", got)
	}
}

// And the default must not change. Stage 0 is opt-in; a normal server keeps
// sending what it sends today.
func TestTuneDefaultIsUnchanged(t *testing.T) {
	t.Setenv("DN_TUNE_REAL_WEAPONS", "")
	if payload := buildMmogTunePayload(); len(payload) > 1000 {
		t.Errorf("default YA_Tune is %d bytes; it should still be the small "+
			"empty-tables payload", len(payload))
	}
}
