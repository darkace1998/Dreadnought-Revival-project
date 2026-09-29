package main

import (
	"bytes"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

func squadField(payload []byte, name, want string) bool {
	return bytes.Contains(payload, protocol.AppendStringField(nil, name, want))
}

func rtOf(p []byte) string { return protocol.FirstStringField(p, "RT") }

// The whole invite -> accept -> leave cycle, with the pushes each side gets.
func TestSquadInviteAcceptLeave(t *testing.T) {
	h := newSquadHub()
	old := squadHubInstance
	squadHubInstance = h
	t.Cleanup(func() { squadHubInstance = old })
	const a, b = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	h.connected(a)
	h.connected(b)

	// The invitee is named as the client sends it: dashed or not.
	reply := h.invite(a, dashedPlayerGUID(b))
	if rtOf(reply) != "YA_SquadCreateAndJoin" || !squadField(reply, "result", "success") {
		t.Fatalf("invite reply %q, want YA_SquadCreateAndJoin result=success", reply)
	}
	ap := h.drainPushes(a)
	if len(ap) != 1 || rtOf(ap[0]) != "YA_SquadJoined" || !squadField(ap[0], "PIDLeader", a) {
		t.Fatalf("inviter pushes %d (%v), want YA_SquadJoined with PIDLeader %s", len(ap), ap, a)
	}
	bp := h.drainPushes(b)
	if len(bp) != 1 || rtOf(bp[0]) != "YA_SquadInvite" || !squadField(bp[0], "pid", a) {
		t.Fatalf("invitee pushes %v, want YA_SquadInvite pid=%s", bp, a)
	}

	reply = h.accept(b, a)
	if rtOf(reply) != "YA_SquadJoin" || !squadField(reply, "result", "success") {
		t.Fatalf("accept reply %q, want YA_SquadJoin result=success", reply)
	}
	bp = h.drainPushes(b)
	if len(bp) != 1 || rtOf(bp[0]) != "YA_SquadJoined" || !squadField(bp[0], "PID", b) || !squadField(bp[0], "PID", a) {
		t.Fatalf("accepter pushes %v, want YA_SquadJoined listing both", bp)
	}
	ap = h.drainPushes(a)
	if len(ap) != 1 || rtOf(ap[0]) != "YA_SquadInfo" || !squadField(ap[0], "PID", b) {
		t.Fatalf("leader pushes %v, want YA_SquadInfo with the new member", ap)
	}

	// A squad of two loses one: it disbands, and the one left is told.
	if id := h.leave(b); id == "" {
		t.Fatal("leave found no squad")
	}
	ap = h.drainPushes(a)
	if len(ap) == 0 || rtOf(ap[0]) != "YA_SquadLeave" {
		t.Fatalf("remaining member pushes %v, want YA_SquadLeave", ap)
	}
	h.mu.Lock()
	n := len(h.squads)
	h.mu.Unlock()
	if n != 0 {
		t.Errorf("%d squads left after the squad fell to one member, want 0", n)
	}
}

// Invites to offline players, or to someone already in a squad, are refused;
// accepting with no invite is refused.
func TestSquadRefusals(t *testing.T) {
	h := newSquadHub()
	const a, b, c = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc"
	h.connected(a)
	if r := h.invite(a, b); squadField(r, "result", "success") {
		t.Error("an invite to an offline player succeeded")
	}
	h.connected(b)
	h.connected(c)
	if r := h.accept(b, a); squadField(r, "result", "success") {
		t.Error("accepting a non-existent invite succeeded")
	}
	h.invite(a, b)
	h.accept(b, a)
	if r := h.invite(c, b); squadField(r, "result", "success") {
		t.Error("inviting a player who is already in a squad succeeded")
	}
}

// A disconnect leaves the squad, and the squad's pending invites die with it.
func TestSquadDisconnectLeaves(t *testing.T) {
	h := newSquadHub()
	old := squadHubInstance
	squadHubInstance = h
	t.Cleanup(func() { squadHubInstance = old })
	const a, b, c = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc"
	for _, p := range []string{a, b, c} {
		h.connected(p)
	}
	h.invite(a, b)
	h.accept(b, a)
	h.invite(a, c)
	h.disconnected(a)
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.squads) != 0 {
		t.Errorf("%d squads after the leader of a two-man squad dropped, want 0", len(h.squads))
	}
	if len(h.invites[c]) != 0 {
		t.Errorf("a dead squad's invite is still pending: %v", h.invites[c])
	}
}

// The zero-SquadID YA_SquadLeave the client sends after every match must not
// break the squad; one naming the squad leaves it.
func TestSquadLeaveNeedsTheSquadsID(t *testing.T) {
	h := newSquadHub()
	old := squadHubInstance
	squadHubInstance = h
	t.Cleanup(func() { squadHubInstance = old })
	const a, b = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	h.connected(a)
	h.connected(b)
	h.invite(a, b)
	h.accept(b, a)
	id := h.squadIDOf(b)

	zero := protocol.AppendStringField(nil, "SquadID", "00000000000000000000000000000000")
	buildMmogSquadRequestPayload("YA_SquadLeave", b, zero)
	if h.squadIDOf(b) != id {
		t.Fatal("a zero-SquadID leave (sent after every match) broke the squad")
	}
	buildMmogSquadRequestPayload("YA_SquadLeave", b, protocol.AppendStringField(nil, "SquadID", id))
	if h.squadIDOf(b) != "" {
		t.Error("a leave naming the squad did not leave it")
	}
}

func formTestSquad(t *testing.T) (*squadHub, string, string) {
	t.Helper()
	h := newSquadHub()
	old := squadHubInstance
	squadHubInstance = h
	t.Cleanup(func() { squadHubInstance = old })
	const a, b = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	h.connected(a)
	h.connected(b)
	h.invite(a, b)
	h.accept(b, a)
	h.drainPushes(a)
	h.drainPushes(b)
	return h, a, b
}

func queuedParty(t *testing.T, pid string) string {
	t.Helper()
	var party string
	_ = currentMmogPlayerStateDB().QueryRow(`SELECT party_id FROM queue_entries WHERE user_id=? AND status='waiting'`, pid).Scan(&party)
	return party
}

// The leader queues the whole squad as one party; every member is pulled in
// (State 1) and their connection starts watching for the match.
func TestSquadQueuesTogether(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	h, a, b := formTestSquad(t)
	req := protocol.AppendStringField(nil, "GameType", "TDM")
	reply := h.enterSquadMatchmaking(a, req)
	if !squadField(reply, "result", "ok") {
		t.Fatalf("reply %q, want result=ok", reply)
	}
	id := h.squadIDOf(a)
	for _, p := range []string{a, b} {
		if got := queuedParty(t, p); got != id {
			t.Errorf("%s queued with party %q, want the squad %q", p[:4], got, id)
		}
		pushes := h.drainPushes(p)
		if len(pushes) != 1 || rtOf(pushes[0]) != "YA_SquadInfo" || !squadField(pushes[0], "State", "1") {
			t.Errorf("%s pushes %v, want YA_SquadInfo State=1", p[:4], pushes)
		}
		if h.takeArm(p) != 1 {
			t.Errorf("%s's connection was not armed to watch for the match", p[:4])
		}
	}
	if r := h.enterSquadMatchmaking(b, req); !squadField(r, "result", "squad_not_in_waiting_state") {
		t.Errorf("queueing an already-searching squad: %q, want squad_not_in_waiting_state", r)
	}
}

// Any member leaving the queue takes the squad out and names who cancelled.
func TestSquadCancelDequeuesEveryone(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	h, a, b := formTestSquad(t)
	h.enterSquadMatchmaking(a, protocol.AppendStringField(nil, "GameType", "TDM"))
	h.drainPushes(a)
	h.drainPushes(b)
	h.takeArm(a)
	h.takeArm(b)

	if !h.cancelSquadMatchmaking(b) {
		t.Fatal("cancel found no searching squad")
	}
	for _, p := range []string{a, b} {
		if got := queuedParty(t, p); got != "" {
			t.Errorf("%s still queued after the cancel", p[:4])
		}
		pushes := h.drainPushes(p)
		if len(pushes) != 1 || !squadField(pushes[0], "State", "0") || !squadField(pushes[0], "CancellingPlayerId", b) {
			t.Errorf("%s pushes %v, want State=0 naming the canceller", p[:4], pushes)
		}
		if h.takeArm(p) != -1 {
			t.Errorf("%s's connection was not told to stop watching", p[:4])
		}
	}
}

// A formed match flips the squad to State 2 for everyone, and it can queue
// again afterwards.
func TestSquadMatchFound(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	h, a, b := formTestSquad(t)
	req := protocol.AppendStringField(nil, "GameType", "TDM")
	h.enterSquadMatchmaking(a, req)
	h.drainPushes(a)
	h.drainPushes(b)
	h.matchFound(b)
	for _, p := range []string{a, b} {
		pushes := h.drainPushes(p)
		if len(pushes) != 1 || !squadField(pushes[0], "State", "2") {
			t.Errorf("%s pushes %v, want State=2", p[:4], pushes)
		}
	}
	h.matchFound(a) // the second member's match-ready push: nothing more
	if len(h.drainPushes(a)) != 0 {
		t.Error("State 2 was pushed twice")
	}
	if r := h.enterSquadMatchmaking(a, req); !squadField(r, "result", "ok") {
		t.Errorf("re-queue after a match: %q, want ok", r)
	}
}
