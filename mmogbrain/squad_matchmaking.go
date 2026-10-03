package main

// Squad matchmaking.
//
// The client's YA_SquadInfo handler (0x142A2BB64) acts on a CHANGE of the
// squad's State (record +0x10; stored copy at player +0x3A28), verified from
// the transition code at 0x142A2BEF6:
//
//	new State 1  searching: looks up the player's fleet for FleetType
//	             (0x142A0ECA0) and calls OnSquadPulledIntoMatchmaking
//	             (0x142A156F0), flagging whether this client started it --
//	             "Game type %s; Matchmaking initiator name %s; Fleet to use %s"
//	new State 2  match found: "Squad Match has been found, battle server..."
//	new State 0  idle; with CancellingPlayerId/Name set, "Player %s canceled
//	             match making!"
//
// So: the leader's YA_SquadEnterMatchmaking queues EVERY member as one party
// (queue_entries.party_id; the matchmaker places a party whole and on one
// team) and pushes State 1 to all; a formed match pushes State 2; any
// member's YA_LeaveMatchmaking, or a member leaving the squad, dequeues the
// whole party and pushes State 0 naming them. The reply is read at the root:
// result "ok", or "squad_not_in_waiting_state" (0x142A2B933).

import (
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/matchmaker"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

func buildSquadEnterMatchmakingReply(result string) []byte {
	b := protocol.AppendStringField(nil, "RT", "YA_SquadEnterMatchmaking")
	return protocol.AppendStringField(b, "result", result)
}

// enterSquadMatchmaking handles YA_SquadEnterMatchmaking.
func (h *squadHub) enterSquadMatchmaking(playerPID string, payload []byte) []byte {
	pid := squadPID(normalizedPlayerStatePID(playerPID))
	h.mu.Lock()
	sq := h.squadOf(pid)
	if sq == nil {
		h.mu.Unlock()
		// Not in a squad (the client should not send this then): queue alone,
		// as before squads existed.
		return buildMmogEnterMatchmakingPayload("YA_SquadEnterMatchmaking", playerPID, payload)
	}
	if sq.state != 0 {
		h.mu.Unlock()
		return buildSquadEnterMatchmakingReply("squad_not_in_waiting_state")
	}
	h.mu.Unlock()

	// The same mode resolution as a solo queue (buildMmogEnterMatchmakingPayload).
	gameMode := protocol.FirstNonEmptyString(payload, "GameType", "GameTypes", "GameMode")
	if matchmaker.IsWildcardGameMode(gameMode) {
		gameMode = matchmaker.DefaultGameMode
	}
	if !matchmaker.ValidGameMode(gameMode) {
		return buildSquadEnterMatchmakingReply("invalid_gametype")
	}
	gameMode = matchmaker.NormalizeGameMode(gameMode)
	database := currentMmogPlayerStateDB()
	fleetType := protocol.FirstInt32(payload, "FleetType")
	if fleetType < 1 || fleetType > 3 {
		fleetType = 1
		if database != nil {
			fleetType = queuedFleetType(database, pid, payload)
		}
	}

	h.mu.Lock()
	if sq = h.squadOf(pid); sq == nil || sq.state != 0 {
		h.mu.Unlock()
		return buildSquadEnterMatchmakingReply("squad_not_in_waiting_state")
	}
	members := append([]string(nil), sq.members...)
	if database != nil {
		for _, m := range members {
			_, _ = database.Exec(`DELETE FROM queue_entries WHERE user_id=? AND status='waiting'`, m)
			if _, err := database.Exec(`INSERT INTO queue_entries(id,user_id,game_mode,tier_min,tier_max,fleet_type,status,party_id) VALUES(?,?,?,?,?,?,'waiting',?)`,
				uuid.New().String(), m, gameMode, 1, 5, fleetType, sq.id); err != nil {
				_, _ = database.Exec(`DELETE FROM queue_entries WHERE party_id=?`, sq.id)
				h.mu.Unlock()
				logrus.WithError(err).WithField("squad", sq.id).Warn("squad: queue insert failed")
				return buildSquadEnterMatchmakingReply("queue_failed")
			}
		}
	}
	sq.state, sq.gameMode, sq.fleetType = 1, gameMode, int(fleetType)
	record := *sq
	record.members = members
	for _, m := range members {
		h.armed[m] = true
	}
	h.mu.Unlock()

	for _, m := range members {
		h.push(m, buildSquadInfoPayload(&record))
	}
	logrus.WithFields(logrus.Fields{"player": pid, "squad": record.id, "members": len(members),
		"mode": gameMode, "fleet_type": fleetType}).Info("squad: queued for matchmaking")
	announceQueueStarted(gameMode, fleetType, len(members))
	return buildSquadEnterMatchmakingReply("ok")
}

// cancelSquadMatchmaking dequeues pid's whole squad if it is searching, and
// tells every member who cancelled. Reports whether it did.
func (h *squadHub) cancelSquadMatchmaking(playerPID string) bool {
	pid := squadPID(normalizedPlayerStatePID(playerPID))
	h.mu.Lock()
	sq := h.squadOf(pid)
	if sq == nil || sq.state != 1 {
		h.mu.Unlock()
		return false
	}
	record := h.dequeueLocked(sq)
	h.mu.Unlock()
	for _, m := range record.members {
		h.push(m, buildSquadInfoPayloadCancelled(&record, pid))
	}
	logrus.WithFields(logrus.Fields{"player": pid, "squad": record.id}).Info("squad: matchmaking cancelled")
	return true
}

// dequeueLocked removes a searching squad's queue entries and disarms its
// members. h.mu must be held. Returns a copy of the squad, now idle.
func (h *squadHub) dequeueLocked(sq *squad) squad {
	if database := currentMmogPlayerStateDB(); database != nil {
		_, _ = database.Exec(`DELETE FROM queue_entries WHERE party_id=?`, sq.id)
	}
	sq.state = 0
	for _, m := range sq.members {
		h.armed[m] = false
	}
	record := *sq
	record.members = append([]string(nil), sq.members...)
	return record
}

// matchFound is called when a player's match-ready push goes out. The first
// member of a searching squad to get there flips the squad to State 2 for
// everyone. The server side goes back to idle at once so the squad can queue
// again; the client keeps 2 until the next State it is sent.
func (h *squadHub) matchFound(playerPID string) {
	pid := squadPID(normalizedPlayerStatePID(playerPID))
	h.mu.Lock()
	sq := h.squadOf(pid)
	if sq == nil || sq.state != 1 {
		h.mu.Unlock()
		return
	}
	sq.state = 2
	record := *sq
	record.members = append([]string(nil), sq.members...)
	sq.state = 0
	h.mu.Unlock()
	for _, m := range record.members {
		h.push(m, buildSquadInfoPayload(&record))
	}
	logrus.WithFields(logrus.Fields{"squad": record.id, "members": len(record.members)}).Info("squad: match found")
}

// takeArm reports whether a member's connection should start (1) or stop (-1)
// watching for a match, or neither (0).
func (h *squadHub) takeArm(playerPID string) int {
	pid := squadPID(playerPID)
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.armed[pid]
	if !ok {
		return 0
	}
	delete(h.armed, pid)
	if v {
		return 1
	}
	return -1
}
