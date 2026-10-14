package main

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// The YA_UpdateShipLoadout reply must carry ROOT result "ok" (a string) and
// ROOT "loadout" -- the saved loadout, which the client copies over its own
// (0x2A2F34F). Without it the editor reloaded the login copy when switching
// between LOADOUT A and B and the edit was lost.
func TestUpdateShipLoadoutReplyCarriesTheSavedLoadout(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "0123456789abcdef0123456789abcdef"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	var a mmogShipLoadoutSeed
	for _, l := range withLoadoutVariants(pid, ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(pid), pid)) {
		if l.variant == 0 && shipLoadoutHasVariants(l) {
			a = l
			break
		}
	}
	if a.precastLoadoutID == 0 {
		t.Fatal("no starter loadout with a LOADOUT B")
	}
	save := func(slot int32, perimeter int32) []byte {
		req := protocol.AppendStringField(nil, "RT", "YA_UpdateShipLoadout")
		req = protocol.AppendInt32Field(req, "ShipID", a.loadoutID())
		req = protocol.AppendInt32Field(req, "AbilityPerimeter", perimeter)
		req = protocol.AppendInt32Field(req, "LoadoutSlotNum", slot)
		req = protocol.AppendRootEnd(req)
		if err := persistUpdateShipLoadout(database, pid, req); err != nil {
			t.Fatal(err)
		}
		return buildMmogUpdateShipLoadoutPayload(pid, req)
	}
	t.Setenv("DN_LOADOUT_OWNERSHIP", "off")
	// The stored value, not the request's: the reply is what the server kept.
	aPerimeter := a.abilityIDs[2]
	replyA := save(1, aPerimeter)
	if !bytes.Contains(replyA, protocol.AppendStringField(nil, "result", "ok")) {
		t.Fatal("result is not the string ok")
	}
	if !bytes.Contains(replyA, protocol.AppendStringField(nil, "ID", a.entryID())) {
		t.Errorf("A's reply does not name LOADOUT A (%s)", a.entryID())
	}
	replyB := save(2, aPerimeter)
	if !bytes.Contains(replyB, protocol.AppendStringField(nil, "ID", a.entryID()+"_B")) {
		t.Errorf("B's reply does not name LOADOUT B")
	}
	if bytes.Contains(replyB, protocol.AppendStringField(nil, "ID", a.entryID())) {
		t.Error("B's reply names LOADOUT A")
	}
	if !bytes.Contains(replyA, []byte("abilityPerimeter")) || !bytes.Contains(replyA,
		protocol.AppendStringField(nil, "abilityPerimeter", strconv.Itoa(int(a.abilityItemID(2))))) {
		t.Error("A's reply lacks its stored perimeter module")
	}
	missing := protocol.AppendRootEnd(protocol.AppendInt32Field(nil, "ShipID", 12345))
	if !bytes.Contains(buildMmogUpdateShipLoadoutPayload(pid, missing), protocol.AppendStringField(nil, "result", "failed")) {
		t.Error("a save of an unknown loadout was not answered failed")
	}
}
