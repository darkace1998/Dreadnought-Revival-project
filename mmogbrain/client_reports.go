package main

import (
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

// YA_LogSpecial is the client reporting a problem to the backend. It was
// answered "Unknown command" and its contents thrown away until 2026-09-28.
//
// Request (0x2A36A20): RT, Type, Name, Desc -- all strings. Sent from
// (strings at the call sites, verified in the exe):
//
//   - 0x34137B / 0x3415DB, Type "GameplayLogicFailure", Name "ShipXpError":
//     "Ship XP pools were not gathered correctly. PID {0}. Ship ID {1}" and
//     "Free XP pools for heroship were not gathered correctly. ...".
//   - 0x54A9B3 / 0x54EA0D, Type "NetworkFailureClient": "Network failure
//     event." with a "LIMBO ... Info Dump" -- a call stack and the client's
//     mmog log.
//
// Stored in client_reports; the log line carries a short excerpt.
func recordClientReport(playerPID string, payload []byte) {
	typ := protocol.FirstNonEmptyString(payload, "Type", "type")
	name := protocol.FirstNonEmptyString(payload, "Name", "name")
	desc := protocol.FirstNonEmptyString(payload, "Desc", "desc")
	pid := normalizedPlayerStatePID(playerPID)

	excerpt := desc
	if len(excerpt) > 200 {
		excerpt = excerpt[:200] + "..."
	}
	logrus.WithFields(logrus.Fields{"player": pid, "type": typ, "name": name, "desc_bytes": len(desc)}).
		Warn("client report: " + excerpt)

	database := currentMmogPlayerStateDB()
	if database == nil {
		return
	}
	if _, err := database.Exec(`INSERT INTO client_reports(user_id,type,name,desc) VALUES(?,?,?,?)`,
		pid, typ, name, desc); err != nil {
		logrus.WithError(err).Warn("client report: not stored")
	}
}
