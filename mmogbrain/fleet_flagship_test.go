package main

import (
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// flagshipRequest is YA_SetFleetFlagship as the client sends it (0x142A496D0):
// fleet as a 16-byte GUID, shipId as int32 (tag 0x56), loadoutindex as a
// 1-byte int (tag 0x16).
func flagshipRequest(fleetGUID string, shipID int32, loadoutIndex byte) []byte {
	raw, _ := hex.DecodeString(fleetGUID)
	b := protocol.AppendStringField(nil, "RT", "YA_SetFleetFlagship")
	b = append(b, 5)
	b = append(b, "fleet"...)
	b = append(b, 0x02)
	b = append(b, raw...)
	b = append(b, 6)
	b = append(b, "shipId"...)
	b = append(b, 0x56)
	b = binary.LittleEndian.AppendUint32(b, uint32(shipID))
	b = append(b, 12)
	b = append(b, "loadoutindex"...)
	b = append(b, 0x16, loadoutIndex)
	return protocol.AppendRootEnd(b)
}

// Setting a flagship stores it on THAT fleet, by the ship's loadout id (what
// the client sends), with its place in the fleet; no other fleet changes and
// the active fleet stays. The reply carries what the client's handler reads
// before it applies the change: result "ok", fleet, shipId, loadoutindex.
func TestSetFleetFlagship(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	fleet := mmogPlayerStateForPID(pid).activeFleet()
	if len(fleet.shipLoadouts) < 3 {
		t.Fatalf("starter fleet has %d ships", len(fleet.shipLoadouts))
	}
	target := fleet.shipLoadouts[2]
	req := flagshipRequest(fleetFID(pid, fleet.fleetID), target.precastLoadoutID, 0)

	if err := persistSetFleetFlagship(database, pid, req); err != nil {
		t.Fatal(err)
	}
	after := mmogPlayerStateForPID(pid).activeFleet()
	if after.fleetID != fleet.fleetID {
		t.Errorf("the active fleet changed: %d -> %d", fleet.fleetID, after.fleetID)
	}
	if after.flagshipShipID != target.precastLoadoutID || after.flagshipIndex() != 2 {
		t.Errorf("flagship %d at index %d, want %d at index 2", after.flagshipShipID, after.flagshipIndex(), target.precastLoadoutID)
	}

	// FlagShipLoadoutIndex is the flagship SHIP's own loadout slot (0: every
	// ship has one), never its fleet place: the client indexes the ship's
	// loadout array with it unchecked (FUN_140340050), and the fleet place
	// (2 here) crashed clients live (2026-10-04/05).
	var stored int32
	if err := database.QueryRow(`SELECT flagship_loadout_index FROM player_fleets WHERE user_id=? AND fleet_id=?`, pid, fleet.fleetID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 || after.flagshipShipLoadoutSlot() != 0 {
		t.Errorf("flagship loadout slot stored %d, sent %d; want 0 (the ship's only loadout)", stored, after.flagshipShipLoadoutSlot())
	}
	// A bad stored value (as the 2026-10-03 build wrote) never reaches the wire.
	if _, err := database.Exec(`UPDATE player_fleets SET flagship_loadout_index=4 WHERE user_id=? AND fleet_id=?`, pid, fleet.fleetID); err != nil {
		t.Fatal(err)
	}
	fleets := buildMmogPlayerFleetsPayload(pid)
	if got := protocol.ExtractStringField(fleets, "FlagShipLoadoutIndex"); got != "0" {
		t.Errorf("YA_PlayerFleets FlagShipLoadoutIndex = %q with 4 stored, want 0", got)
	}

	reply := buildMmogRequestResponsePayload("YA_SetFleetFlagship", pid, req)
	for field, want := range map[string]string{"fleet": fleetFID(pid, fleet.fleetID), "loadoutindex": "0"} {
		if got := protocol.ExtractStringField(reply, field); got != want {
			t.Errorf("reply %s = %q, want %q", field, got, want)
		}
	}
	if got := protocol.ExtractStringField(reply, "shipId"); got == "" {
		t.Error("reply carries no shipId")
	}

	// A ship not in the fleet changes nothing.
	if err := persistSetFleetFlagship(database, pid, flagshipRequest(fleetFID(pid, fleet.fleetID), 33489299, 0)); err != nil {
		t.Fatal(err)
	}
	if got := mmogPlayerStateForPID(pid).activeFleet().flagshipShipID; got != target.precastLoadoutID {
		t.Errorf("a ship outside the fleet became flagship %d", got)
	}
}
