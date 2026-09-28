package main

import (
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

func TestClientReportIsStoredAndAcknowledged(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	req := protocol.AppendStringField(nil, "Type", "GameplayLogicFailure")
	req = protocol.AppendStringField(req, "Name", "ShipXpError")
	req = protocol.AppendStringField(req, "Desc", "Ship XP pools were not gathered correctly. PID 1. Ship ID 2")

	reply := string(buildMmogRequestResponsePayload("YA_LogSpecial", pid, req))
	if countWireStringField(reply, "RT", "YA_LogSpecial") == 0 {
		t.Errorf("reply %q does not acknowledge YA_LogSpecial", reply)
	}
	var typ, name, desc string
	if err := database.QueryRow(`SELECT type, name, desc FROM client_reports WHERE user_id=?`, pid).Scan(&typ, &name, &desc); err != nil {
		t.Fatal(err)
	}
	if typ != "GameplayLogicFailure" || name != "ShipXpError" || desc == "" {
		t.Errorf("stored %q %q %q", typ, name, desc)
	}
}
