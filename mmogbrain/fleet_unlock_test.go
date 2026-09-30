package main

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// The client treats every fleet it is sent as unlocked (UpdatePlayerFleetData
// 0x35FDF0 sets each entry's lock count +0x3C to 0), so a fleet unlocks by
// being sent. The game's rule: "Unlock one ship of Tiers III-IV / IV-V to
// access this Fleet" (Veteran / Legendary). Empty fleets were never sent, so
// both stayed locked forever ("the others are locked", operator 2026-09-30).
func TestFleetsUnlockByOwnedShipTier(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	fleetCount := func() int {
		return bytes.Count(buildMmogPlayerFleetsPayload(pid), appendFieldMarker("m_fleetId", 0x56))
	}
	if got := fleetCount(); got != 1 {
		t.Fatalf("new player: %d fleets sent, want 1 (Recruit)", got)
	}
	hullOfTier := func(tier int32) int32 {
		for _, h := range baseShipLoadouts {
			if h.tier == tier {
				return h.loadoutID
			}
		}
		t.Fatalf("no tier-%d hull", tier)
		return 0
	}
	grant := func(id int32) {
		tx, err := database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := grantUnlockedShipLoadout(tx, pid, id); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	grant(hullOfTier(2))
	if got := fleetCount(); got != 1 {
		t.Errorf("owning a tier-2 ship: %d fleets, want 1 (tier II unlocks nothing new)", got)
	}
	grant(hullOfTier(3))
	if got := fleetCount(); got != 2 {
		t.Errorf("owning a tier-3 ship: %d fleets, want 2 (Recruit + Veteran)", got)
	}
	grant(hullOfTier(4))
	if got := fleetCount(); got != 3 {
		t.Errorf("owning a tier-4 ship: %d fleets, want 3 (all)", got)
	}
}

// Fleets are identified by FID on the client (fleet manager lookup, and the
// "fleet" GUID every fleet edit carries back). All fleets shared the player's
// id, so selecting Veteran opened Recruit and edits landed on Recruit
// (operator 2026-09-30). Each fleet needs its own non-zero GUID, and an edit
// naming a fleet's FID must reach that fleet.
func TestEachFleetHasItsOwnFIDAndEditsFollowIt(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int32{}
	for id := int32(1); id <= 3; id++ {
		fid := fleetFID(pid, id)
		raw, err := hex.DecodeString(fid)
		if err != nil || len(raw) != 16 || bytes.Equal(raw, make([]byte, 16)) {
			t.Fatalf("fleet %d FID %q is not a non-zero 16-byte GUID", id, fid)
		}
		if other, dup := seen[fid]; dup {
			t.Fatalf("fleets %d and %d share FID %s", other, id, fid)
		}
		seen[fid] = id
	}
	if fleetFID(pid, 1) != pid {
		t.Error("the Recruit fleet must keep the player's id as its FID")
	}
	edit := func(fleetID int32) []byte {
		raw, _ := hex.DecodeString(fleetFID(pid, fleetID))
		b := protocol.AppendStringField(nil, "RT", "YA_AddToFleet")
		b = append(b, 5)
		b = append(b, "fleet"...)
		b = append(b, 0x02)
		b = append(b, raw...)
		return protocol.AppendRootEnd(b)
	}
	for id := int32(1); id <= 3; id++ {
		if got := fleetEditTargetFleetID(database, pid, edit(id)); got != id {
			t.Errorf("an edit naming fleet %d's FID went to fleet %d", id, got)
		}
	}
}
