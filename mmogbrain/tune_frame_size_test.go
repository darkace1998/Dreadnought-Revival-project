package main

import "testing"

// The ceiling on YA_Tune is the client's 32768-byte mmog RECEIVE RING, not the
// 65535 the 16-bit frame delimiter allows. This test previously budgeted 60000
// on the frame limit alone, which is how a 40,316-byte YA_Tune reached a live
// client on 2026-08-15: it passed every test here and then hung the client.
// The client logged "Requesting tuning values from mmog (version: 0.0.0)" and
// then produced no further YTuneManager line -- no sync, no fallback, no error.
// An oversized frame does not degrade here; it stops the stream.
//
// Two things changed after that, and both matter for what this file measures:
//
//  1. The response is now RT + one zlib blob named "packed" -- the only field
//     the client reads. So the budget applies to the COMPRESSED frame.
//  2. Compression runs about 10:1 on tuning JSON, so the ring stopped being the
//     binding constraint. The truncated real weapons table went from 20,129
//     bytes on the wire to 1,918.
//
// The budget stays well under the ring anyway: a response that is *nearly*
// fatal is not a safe place to sit when the failure mode is a silent hang.
const (
	clientReceiveRingBytes = 32768
	maxTuneFrameBytes      = 24000
)

func TestTunePayloadStaysWellUnderTheReceiveRing(t *testing.T) {
	payload := buildMmogTunePayload()
	t.Logf("YA_TuneReturn payload = %d bytes (budget %d, ring %d)",
		len(payload), maxTuneFrameBytes, clientReceiveRingBytes)
	if len(payload) > maxTuneFrameBytes {
		t.Fatalf("YA_TuneReturn is %d bytes, over the %d budget. At %d it stops "+
			"the client's stream and the hangar never loads.",
			len(payload), maxTuneFrameBytes, clientReceiveRingBytes)
	}
}

// With DN_TUNE_REAL_WEAPONS=1 the real WeaponsTune goes out, truncated on
// element boundaries to tuneTableByteBudget BEFORE compression.
func TestRealWeaponsTuneFitsInOneFrame(t *testing.T) {
	t.Setenv("DN_TUNE_REAL_WEAPONS", "1")
	payload := buildMmogTunePayload()
	document := buildMmogTuneDocument()
	t.Logf("with real WeaponsTune: document = %d bytes, compressed frame = %d "+
		"bytes (budget %d)", len(document), len(payload), maxTuneFrameBytes)

	if len(payload) > maxTuneFrameBytes {
		t.Fatalf("real WeaponsTune makes the frame %d bytes, over the %d budget",
			len(payload), maxTuneFrameBytes)
	}
	// Measure the DOCUMENT, not the frame. The old version of this check
	// required the frame to exceed 10000 bytes, which was a fine proxy while the
	// tables were sent raw and became a false alarm the moment they were
	// compressed -- 1918 bytes of blob is ~20KB of tuning data. If this ever
	// stops growing, the switch is wired to nothing and the experiment would
	// report a false negative.
	if len(document) < 10000 {
		t.Errorf("document is only %d bytes with the switch on; real tuning "+
			"data is not being included", len(document))
	}
}

// truncateJSONArray must cut on element boundaries. A prefix-of-bytes cut would
// produce invalid JSON, which the client would reject in a way indistinguishable
// from an empty table.
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

// And the default must not change. The real tables are opt-in.
func TestTuneDefaultIsUnchanged(t *testing.T) {
	t.Setenv("DN_TUNE_REAL_WEAPONS", "")
	if payload := buildMmogTunePayload(); len(payload) > 1000 {
		t.Errorf("default YA_TuneReturn is %d bytes; it should still be the "+
			"small empty-tables payload", len(payload))
	}
}
