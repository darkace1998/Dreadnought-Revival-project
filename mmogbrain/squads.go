package main

// Squads: invite, accept, leave and the squad state pushes.
//
// Everything below is read from the client binary (2026-09-29); there is no
// live capture of the original service's squad traffic.
//
// The client SENDS (sender functions, field names from their string refs):
//
//	YA_SquadInvite            0x2A49250  pid (the invitee), Version, FMPeerID
//	YA_SquadAccept            0x2A14C70  inv, Version, FMPeerID
//	YA_SquadLeave             0x2A36590  SquadID (GUID, tag 0x02; all-zero when
//	                                     not in a squad -- sent after every match)
//	YA_SquadEnterMatchmaking  0x2A4A536  FleetType, MapName, GameTypes[]
//
// The client HANDLES (dispatcher 0x2A236C2-0x2A31A32):
//
//	YA_SquadInvite            pid, name (0x2A76DA0) -- an INCOMING invite
//	YA_SquadInviteInvalidate  reason (int)
//	YA_SquadJoined            the squad record at the root (0x2A76850),
//	                          stored on the player (+0x3A18)
//	YA_SquadInfo              the squad record + PidLeader, StartTime,
//	                          CancellingPlayerId, CancellingPlayerName
//	YA_SquadLeave             pid, squadId
//	YA_SquadCreateAndJoin     result == "success", else logs the reason
//	YA_SquadJoin              result == "success", else logs the reason
//
// Squad record (0x2A76850): ID, PIDLeader (GUID strings, parsed by
// 0x2A1D450 from the node's STRING form), Users[] of {PID, Name} (0x2A77020),
// GameMode (string), State (int, clamped to <= 2), FleetType (int, <= 3).
//
// Nothing in the client sends YA_SquadCreateAndJoin or YA_SquadJoin, yet it
// has reply handlers for both; and a reply to YA_SquadInvite named
// YA_SquadInvite would land in the INCOMING-invite handler (the old stub made
// an inviter receive an invite from themselves). So they are taken to be the
// reply names of invite and accept -- GUESS, the same pattern as
// YA_Tune -> YA_TuneReturn.
//
// Squads live in memory: they are a session construct, and a restart drops
// every connection anyway.

import (
	"strconv"
	"strings"
	"sync"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

type squad struct {
	id        string   // 32 hex, no hyphens (the client's GUID string form)
	leader    string   // player id
	members   []string // player ids, leader first
	gameMode  string
	state     int // GUESS: 0 idle (EYMatchMakingState squad values untraced)
	fleetType int
}

type squadInvite struct {
	squadID string
	inviter string
}

type squadHub struct {
	mu       sync.Mutex
	squads   map[string]*squad        // by squad id
	bySquad  map[string]string        // player id -> squad id
	invites  map[string][]squadInvite // invitee -> pending invites
	online   map[string]int           // player id -> open game connections
	outbound map[string][][]byte      // player id -> queued push payloads
	// armed tells a member's connection to start (true) or stop (false)
	// watching for a formed match -- a squad member is queued by the LEADER's
	// request, so their own connection never saw YA_EnterMatchmaking.
	armed map[string]bool
}

var squadHubInstance = newSquadHub()

func newSquadHub() *squadHub {
	return &squadHub{
		squads:   map[string]*squad{},
		bySquad:  map[string]string{},
		invites:  map[string][]squadInvite{},
		online:   map[string]int{},
		outbound: map[string][][]byte{},
		armed:    map[string]bool{},
	}
}

// squadPID is a player id in the form the squad code keys on (32 lowercase
// hex), from whatever the client sent: a dashed or undashed string, or a
// GUID field already rendered as hex.
func squadPID(raw string) string {
	s := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), "-", ""))
	if len(s) != 32 {
		return ""
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return ""
		}
	}
	return s
}

// --- connections and the push queue -----------------------------------------

func (h *squadHub) connected(pid string) {
	if pid = squadPID(pid); pid == "" {
		return
	}
	h.mu.Lock()
	h.online[pid]++
	h.mu.Unlock()
}

// disconnected drops a player's last game connection: they leave their squad
// (the others are told) and their pending invites go.
func (h *squadHub) disconnected(pid string) {
	if pid = squadPID(pid); pid == "" {
		return
	}
	h.mu.Lock()
	h.online[pid]--
	last := h.online[pid] <= 0
	if last {
		delete(h.online, pid)
		delete(h.outbound, pid)
		delete(h.invites, pid)
	}
	h.mu.Unlock()
	if last {
		h.leave(pid)
	}
}

func (h *squadHub) isOnline(pid string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.online[pid] > 0
}

// push queues a server-initiated message for a player's game connection; the
// connection's own loop writes it (drainPushes), within one client ping (5 s).
func (h *squadHub) push(pid string, payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.online[pid] > 0 {
		h.outbound[pid] = append(h.outbound[pid], payload)
	}
}

func (h *squadHub) drainPushes(pid string) [][]byte {
	if pid = squadPID(pid); pid == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.outbound[pid]
	delete(h.outbound, pid)
	return out
}

// --- squad operations ---------------------------------------------------------

// squadIDOf is the id of pid's squad, or "".
func (h *squadHub) squadIDOf(pid string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bySquad[squadPID(pid)]
}

func (h *squadHub) squadOf(pid string) *squad {
	if id, ok := h.bySquad[pid]; ok {
		return h.squads[id]
	}
	return nil
}

// invite: the inviter's squad (created on the first invite, with the inviter
// as leader) invites target. Returns the reply payload.
func (h *squadHub) invite(inviter, rawTarget string) []byte {
	inviter = squadPID(inviter)
	target := squadPID(rawTarget)
	fail := func(reason string) []byte {
		logrus.WithFields(logrus.Fields{"player": inviter, "target": target, "reason": reason}).Info("squad: invite refused")
		return buildSquadResultPayload("YA_SquadCreateAndJoin", reason)
	}
	if inviter == "" || target == "" || target == inviter {
		return fail("invalid_target")
	}
	if !h.isOnline(target) {
		return fail("user_offline")
	}
	h.mu.Lock()
	if other := h.squadOf(target); other != nil {
		h.mu.Unlock()
		return fail("user_in_squad")
	}
	sq := h.squadOf(inviter)
	created := false
	if sq == nil {
		sq = &squad{id: strings.ReplaceAll(uuid.New().String(), "-", ""), leader: inviter, members: []string{inviter}}
		h.squads[sq.id] = sq
		h.bySquad[inviter] = sq.id
		created = true
	}
	pending := h.invites[target][:0]
	for _, inv := range h.invites[target] {
		if inv.inviter != inviter {
			pending = append(pending, inv)
		}
	}
	h.invites[target] = append(pending, squadInvite{squadID: sq.id, inviter: inviter})
	record := *sq
	record.members = append([]string(nil), sq.members...)
	h.mu.Unlock()

	if created {
		h.push(inviter, buildSquadJoinedPayload(&record))
		socialHubInstance.joinSquadChannel(inviter, record.id)
	}
	h.push(target, buildSquadInvitePushPayload(inviter))
	logrus.WithFields(logrus.Fields{"player": inviter, "target": target, "squad": record.id, "created": created}).Info("squad: invite sent")
	return buildSquadResultPayload("YA_SquadCreateAndJoin", "")
}

// accept: target accepts the invite from rawInviter (the "inv" field; GUESS:
// the inviter's id, the only id the invite push carried). Without it, the
// newest pending invite.
func (h *squadHub) accept(target, rawInviter string) []byte {
	target = squadPID(target)
	inviter := squadPID(rawInviter)
	fail := func(reason string) []byte {
		logrus.WithFields(logrus.Fields{"player": target, "inviter": inviter, "raw": rawInviter, "reason": reason}).Info("squad: accept refused")
		return buildSquadResultPayload("YA_SquadJoin", reason)
	}
	h.mu.Lock()
	var chosen *squadInvite
	for i := len(h.invites[target]) - 1; i >= 0; i-- {
		inv := h.invites[target][i]
		if inviter == "" || inv.inviter == inviter {
			chosen = &inv
			break
		}
	}
	if chosen == nil {
		h.mu.Unlock()
		return fail("no_invite")
	}
	delete(h.invites, target)
	sq := h.squads[chosen.squadID]
	if sq == nil {
		h.mu.Unlock()
		return fail("squad_gone")
	}
	if h.squadOf(target) != nil {
		h.mu.Unlock()
		return fail("already_in_squad")
	}
	sq.members = append(sq.members, target)
	h.bySquad[target] = sq.id
	record := *sq
	record.members = append([]string(nil), sq.members...)
	h.mu.Unlock()

	h.push(target, buildSquadJoinedPayload(&record))
	for _, m := range record.members {
		if m != target {
			h.push(m, buildSquadInfoPayload(&record))
		}
	}
	socialHubInstance.joinSquadChannel(target, record.id)
	logrus.WithFields(logrus.Fields{"player": target, "squad": record.id, "members": len(record.members)}).Info("squad: joined")
	return buildSquadResultPayload("YA_SquadJoin", "")
}

// leave removes pid from their squad. The rest are told (YA_SquadLeave, then
// the new state); a squad of one disbands; a departing leader hands over to
// the next member. Returns the squad id left, or "".
func (h *squadHub) leave(pid string) string {
	pid = squadPID(pid)
	h.mu.Lock()
	sq := h.squadOf(pid)
	if sq == nil {
		h.mu.Unlock()
		return ""
	}
	searching := sq.state == 1
	if searching {
		h.dequeueLocked(sq)
	}
	delete(h.bySquad, pid)
	kept := sq.members[:0]
	for _, m := range sq.members {
		if m != pid {
			kept = append(kept, m)
		}
	}
	sq.members = kept
	if sq.leader == pid && len(kept) > 0 {
		sq.leader = kept[0]
	}
	var disbanded []string
	if len(sq.members) <= 1 {
		disbanded = append(disbanded, sq.members...)
		for _, m := range sq.members {
			delete(h.bySquad, m)
		}
		delete(h.squads, sq.id)
		sq.members = nil
	}
	for invitee, list := range h.invites {
		kept := list[:0]
		for _, inv := range list {
			if inv.squadID != sq.id || len(sq.members) > 0 {
				kept = append(kept, inv)
			}
		}
		h.invites[invitee] = kept
	}
	record := *sq
	record.members = append([]string(nil), sq.members...)
	h.mu.Unlock()

	socialHubInstance.leaveSquadChannel(pid, record.id)
	for _, m := range disbanded {
		h.push(m, buildSquadLeavePayload(m, record.id))
		socialHubInstance.leaveSquadChannel(m, record.id)
	}
	canceller := ""
	if searching {
		canceller = pid // "Player %s canceled match making!"
	}
	for _, m := range record.members {
		h.push(m, buildSquadLeavePayload(pid, record.id))
		h.push(m, buildSquadInfoPayloadCancelled(&record, canceller))
	}
	logrus.WithFields(logrus.Fields{"player": pid, "squad": record.id, "left": len(record.members), "disbanded": len(disbanded) > 0}).Info("squad: left")
	return record.id
}

// --- payloads -------------------------------------------------------------------

func buildSquadResultPayload(rt, failure string) []byte {
	result := "success"
	if failure != "" {
		result = failure
	}
	b := protocol.AppendStringField(nil, "RT", rt)
	b = protocol.AppendStringField(b, "result", result)
	if failure != "" {
		b = protocol.AppendStringField(b, "reason", failure)
	}
	return b
}

func appendSquadRecord(b []byte, stack []int, sq *squad) ([]byte, []int) {
	b = protocol.AppendStringField(b, "ID", sq.id)
	b = protocol.AppendStringField(b, "PIDLeader", sq.leader)
	b, stack = protocol.AppendArrayStart(b, stack, "Users")
	for _, m := range sq.members {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "PID", m)
		b = protocol.AppendStringField(b, "Name", mmogPlayerStateForPID(m).displayName)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b = protocol.AppendStringField(b, "GameMode", sq.gameMode)
	// Numeric strings: the client reads scalars through its double/int64/
	// string union, where an int32 node reads as 0.
	b = protocol.AppendStringField(b, "State", strconv.Itoa(sq.state))
	b = protocol.AppendStringField(b, "FleetType", strconv.Itoa(sq.fleetType))
	return b, stack
}

func buildSquadJoinedPayload(sq *squad) []byte {
	b := protocol.AppendStringField(nil, "RT", "YA_SquadJoined")
	b, _ = appendSquadRecord(b, nil, sq)
	return b
}

func buildSquadInfoPayload(sq *squad) []byte {
	return buildSquadInfoPayloadCancelled(sq, "")
}

// buildSquadInfoPayloadCancelled is YA_SquadInfo naming the member who
// cancelled the squad's matchmaking: with State 0 and a name longer than one
// character, the client reports "Player %s canceled match making!"
// (transition code at 0x142A2BFB1).
func buildSquadInfoPayloadCancelled(sq *squad, canceller string) []byte {
	b := protocol.AppendStringField(nil, "RT", "YA_SquadInfo")
	b, _ = appendSquadRecord(b, nil, sq)
	b = protocol.AppendStringField(b, "PidLeader", sq.leader)
	b = protocol.AppendStringField(b, "StartTime", "0")
	name := ""
	if canceller != "" {
		name = mmogPlayerStateForPID(canceller).displayName
	}
	b = protocol.AppendStringField(b, "CancellingPlayerId", canceller)
	b = protocol.AppendStringField(b, "CancellingPlayerName", name)
	return b
}

func buildSquadInvitePushPayload(inviter string) []byte {
	b := protocol.AppendStringField(nil, "RT", "YA_SquadInvite")
	b = protocol.AppendStringField(b, "pid", inviter)
	b = protocol.AppendStringField(b, "name", mmogPlayerStateForPID(inviter).displayName)
	return b
}

func buildSquadLeavePayload(pid, squadID string) []byte {
	b := protocol.AppendStringField(nil, "RT", "YA_SquadLeave")
	b = protocol.AppendStringField(b, "pid", pid)
	b = protocol.AppendStringField(b, "squadId", squadID)
	return b
}

// buildMmogSquadRequestPayload answers the client's squad requests.
func buildMmogSquadRequestPayload(requestName, playerPID string, payload []byte) []byte {
	pid := squadPID(normalizedPlayerStatePID(playerPID))
	field := func(name string) string {
		if v := protocol.FirstStringField(payload, name); v != "" {
			return v
		}
		return protocol.FirstGUIDField(payload, name)
	}
	switch requestName {
	case "YA_SquadInvite":
		return squadHubInstance.invite(pid, field("pid"))
	case "YA_SquadAccept":
		return squadHubInstance.accept(pid, field("inv"))
	case "YA_SquadLeave":
		// Only a request naming the player's own squad leaves it. The client
		// also sends YA_SquadLeave with an all-zero SquadID after every match
		// (60 of 60 in the logs); a zero id must not break a squad on the
		// way back from battle.
		requested := squadPID(field("SquadID"))
		if requested == "" || requested != squadHubInstance.squadIDOf(pid) {
			return buildMmogSquadPayload(requestName, playerPID)
		}
		if left := squadHubInstance.leave(pid); left != "" {
			return buildSquadLeavePayload(pid, left)
		}
		// Not in a squad: the client sends this after every match with a
		// zero SquadID. Answer without pid/squadId so its leave handler has
		// nothing to act on (the previous reply, which it accepted 60 times).
		return buildMmogSquadPayload(requestName, playerPID)
	}
	return buildMmogSquadPayload(requestName, playerPID)
}
