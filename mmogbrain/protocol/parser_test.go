package protocol

import (
	"encoding/hex"
	"testing"
)

// The squad queue request as the client sends it (captured live 2026-10-07):
// FleetType is a 1-byte int (0x16) BEFORE GameType, and the string scan used
// to stop there -- every squad queued for the default mode.
func TestStringFieldAfterOneByteInt(t *testing.T) {
	b, err := hex.DecodeString("025254091800000059415f5371756164456e7465724d617463686d616b696e6709466c656574547970651601074d61704e616d650903000000414e590847616d655479706509090000004f6e736c6175676874000e00000000")
	if err != nil {
		t.Fatal(err)
	}
	if got := FirstNonEmptyString(b, "GameType", "GameTypes", "GameMode"); got != "Onslaught" {
		t.Errorf("GameType = %q, want Onslaught", got)
	}
	if got := FirstInt32(b, "FleetType"); got != 1 {
		t.Errorf("FleetType = %d, want 1", got)
	}
}
