package main

import "testing"

// An mmog frame is delimited by a 16-bit size field, so a response over 65535
// bytes does not merely fail -- it desyncs the WHOLE stream. Every frame after
// it is read at the wrong offset, YA_PlayerGet included, so player data never
// arrives and the hangar stalls. That is what happened when the full tuning
// tables (~368KB) were embedded here, and it is why they were emptied.
//
// So any change that puts real data back into YA_Tune has to be bounded by a
// test rather than by care. The budget is deliberately below the hard limit:
// the frame carries protocol overhead on top of the payload, and a response
// that is *nearly* fatal is not a safe place to sit.
const maxTuneFrameBytes = 60000

func TestTunePayloadStaysWellUnderTheFrameLimit(t *testing.T) {
	payload := buildMmogTunePayload()
	t.Logf("YA_Tune payload = %d bytes (budget %d, hard limit 65535)",
		len(payload), maxTuneFrameBytes)
	if len(payload) > maxTuneFrameBytes {
		t.Fatalf("YA_Tune is %d bytes, over the %d budget. Sending this would "+
			"desync the entire mmog stream, not just break tuning.",
			len(payload), maxTuneFrameBytes)
	}
}

// The Stage 0 experiment: with DN_TUNE_REAL_WEAPONS=1 the real WeaponsTune goes
// out. It is ~40KB, which fits -- but only just, and only alone. This asserts
// both halves: that it actually grows, and that it still fits.
func TestRealWeaponsTuneFitsInOneFrame(t *testing.T) {
	t.Setenv("DN_TUNE_REAL_WEAPONS", "1")
	payload := buildMmogTunePayload()
	t.Logf("YA_Tune with real WeaponsTune = %d bytes (budget %d)",
		len(payload), maxTuneFrameBytes)

	if len(payload) > maxTuneFrameBytes {
		t.Fatalf("real WeaponsTune makes YA_Tune %d bytes, over the %d budget",
			len(payload), maxTuneFrameBytes)
	}
	// If this ever stops growing, the switch is wired to nothing and the
	// experiment would report a false negative -- the client would say
	// "backup-data" because we sent empty tables again, and we would conclude
	// emptiness was not the cause.
	if len(payload) < 10000 {
		t.Errorf("payload is only %d bytes with the switch on; the real "+
			"WeaponsTune is ~40KB, so it is not being included", len(payload))
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
