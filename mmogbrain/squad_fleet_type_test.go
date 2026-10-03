package main

import (
	"encoding/hex"
	"testing"
)

// The client sends a squad's fleet as FleetType with the 1-byte tag 0x16.
// The parser had no case for that tag, so FleetType read as absent and every
// squad queued as Recruit -- the squad could never play Veteran or Legendary
// (2026-10-02). The payload is a live capture of a Legendary queue.
func TestSquadQueuesWithTheChosenFleet(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	h, a, b := formTestSquad(t)
	req, err := hex.DecodeString("025254091800000059415f5371756164456e7465724d617463686d616b696e6709466c656574547970651603074d61704e616d650903000000414e590847616d6554797065090300000054444d000e00000000")
	if err != nil {
		t.Fatal(err)
	}
	if reply := h.enterSquadMatchmaking(a, req); !squadField(reply, "result", "ok") {
		t.Fatalf("reply %q, want result=ok", reply)
	}
	for _, p := range []string{a, b} {
		var fleetType int
		if err := database.QueryRow(`SELECT fleet_type FROM queue_entries WHERE user_id=? AND status='waiting'`, p).Scan(&fleetType); err != nil {
			t.Fatal(err)
		}
		if fleetType != 3 {
			t.Errorf("%s queued with fleet type %d, want 3 (Legendary, as sent)", p[:4], fleetType)
		}
	}
}
