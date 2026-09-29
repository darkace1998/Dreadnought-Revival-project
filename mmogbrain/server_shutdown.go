package main

// YA_ServerShutdown: telling players their battle server is gone.
//
// The client handles it (dispatcher 0x142A2E202): "YA_ServerShutdown -
// MMOGBrain reports the battle server was shut down. Mmog RoomState: %d". It
// reads one field, roomState, and acts only when it is above 1; then the
// player controller's HandleOnBattleServerShutDown travels back to the outpost
// when the end-of-match flow is stuck. EYRoomState, in registration order
// (0x2A9D8EE): Created 0, ReadyToStart 1, ServerFinishedLoading 2,
// LateJoiningDisabled 3, MatchOver 4, PostMatch 5, Closed 6.
//
// We never sent it, so when a battle server died (the end-of-match stack
// overflow, 7 matches on 2026-09-29) every player sat in the dead match until
// their connection timed out 180 s later (client_reports ConnectionTimeout,
// each 180 s after the host's crash). The matchmaker notices the host is gone
// (endMatchWithNoHost) and this pushes Closed to its players.
//
// Only players with NO result for that match are told: a match that ended
// normally reported every player's result (/battle/result) at the end-of-match
// transition, and those players are already on their way home; the crash
// happens before that point.

import (
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

func buildServerShutdownPayload() []byte {
	b := protocol.AppendStringField(nil, "RT", "YA_ServerShutdown")
	// Numeric string: the scalar union reads an int32 node as 0.
	return protocol.AppendStringField(b, "roomState", "6")
}

// notifyHostLost is the matchmaker's OnHostLost.
func notifyHostLost(battleMatchID string, players []string) {
	database := currentMmogPlayerStateDB()
	for _, raw := range players {
		pid := normalizedPlayerStatePID(raw)
		if database != nil && battleMatchID != "" {
			var n int
			_ = database.QueryRow(`SELECT count(*) FROM battle_results WHERE user_id=? AND (match_id=? OR match_id LIKE ?)`,
				pid, battleMatchID, battleMatchID+"-r%").Scan(&n)
			if n > 0 {
				continue // ended normally for them
			}
		}
		squadHubInstance.push(pid, buildServerShutdownPayload())
		logrus.WithFields(logrus.Fields{"player": pid, "match": battleMatchID}).
			Info("battle server lost: pushed YA_ServerShutdown")
	}
}
