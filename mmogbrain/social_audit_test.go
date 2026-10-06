package main

import (
	"testing"
	"time"
)

// Social audit 2026-10-06.

// chat.channel.info is answered as a typed event with the info in the root
// data -- the only form the client routes to its channel-info handler.
func TestChannelInfoIsTyped(t *testing.T) {
	hub := socialTestHub(t)
	peer, _ := socialTestPeer(t, hub, "player-a")
	res := handleChatMethod(socialRequest{method: "chat.channel.info",
		params: map[string]any{"channel": "dreadnought.global"}, peer: peer, hub: hub})
	if res[firmamentRootType] != "chat.channel.info" {
		t.Errorf("reply type %v, want chat.channel.info", res[firmamentRootType])
	}
	data, _ := res[firmamentRootData].(map[string]any)
	if data == nil || data["channel"] != "dreadnought.global" {
		t.Fatalf("reply data %v", res[firmamentRootData])
	}
	if members, _ := data["members"].([]any); len(members) != 1 {
		t.Errorf("members %v, want the one player in the room", data["members"])
	}
}

// waitNotice reads pushes until a chat.channel.notice for the room arrives.
func waitNotice(t *testing.T, ch <-chan map[string]any, room string) map[string]any {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-ch:
			data, _ := msg["data"].(map[string]any)
			if msg["type"] == "chat.channel.notice" && data["channel"] == room {
				return data
			}
		case <-deadline:
			t.Fatalf("no notice for %s", room)
			return nil
		}
	}
}

// A room's members hear about arrivals and departures -- at login, in a
// squad, and on disconnect -- in the event shape the client routes.
func TestRoomMembershipIsAnnounced(t *testing.T) {
	hub := socialTestHub(t)
	_, aRead := socialTestPeer(t, hub, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	a := pushLines(aRead)
	b, _ := socialTestPeer(t, hub, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	// b came online: a, in Global, is told.
	if data := waitNotice(t, a, "dreadnought.global"); data["action"] != "join" || data["user"] != dashedPlayerGUID(b.playerID) {
		t.Errorf("a got %v for b's arrival", data)
	}
	// b went offline: a is told b left.
	hub.leave(b)
	if data := waitNotice(t, a, "dreadnought.global"); data["action"] != "leave" || data["user"] != dashedPlayerGUID(b.playerID) {
		t.Errorf("a got %v for b's departure", data)
	}
}

// Leaving a squad tells the player itself (the client clears its squad room
// only on a leave naming itself) and the members who stay.
func TestSquadRoomLeaveIsAnnounced(t *testing.T) {
	hub := socialTestHub(t)
	saved := socialHubInstance
	socialHubInstance = hub
	t.Cleanup(func() { socialHubInstance = saved })
	a, aRead := socialTestPeer(t, hub, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	b, bRead := socialTestPeer(t, hub, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	aCh, bCh := pushLines(aRead), pushLines(bRead)
	hub.joinSquadChannel(a.playerID, "sq1")
	hub.joinSquadChannel(b.playerID, "sq1")
	waitNotice(t, aCh, "sq1.squad") // a's own join
	if data := waitNotice(t, aCh, "sq1.squad"); data["user"] != dashedPlayerGUID(b.playerID) {
		t.Errorf("a was not told b joined the squad room: %v", data)
	}
	hub.leaveSquadChannel(b.playerID, "sq1")
	waitNotice(t, bCh, "sq1.squad") // b's own join
	if data := waitNotice(t, bCh, "sq1.squad"); data["action"] != "leave" || data["user"] != dashedPlayerGUID(b.playerID) {
		t.Errorf("b was not told it left the squad room: %v", data)
	}
	if data := waitNotice(t, aCh, "sq1.squad"); data["action"] != "leave" {
		t.Errorf("a was not told b left: %v", data)
	}
}

// An unanswered invite leaves the inviter alone in a squad of one; that must
// not block others from inviting them, and accepting moves them over.
func TestSquadOfOneDoesNotBlock(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	h := newSquadHub()
	old := squadHubInstance
	squadHubInstance = h
	t.Cleanup(func() { squadHubInstance = old })
	const a, b, c = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc"
	for _, p := range []string{a, b, c} {
		h.connected(p)
	}
	h.invite(a, b) // never answered: a is alone in its own squad
	solo := h.squadIDOf(a)
	if solo == "" {
		t.Fatal("inviting did not create the inviter's squad")
	}
	if reply := h.invite(c, a); squadField(reply, "result", "user_in_squad") {
		t.Fatal("a squad of one blocked an invite")
	}
	h.drainPushes(a)
	if reply := h.accept(a, c); squadField(reply, "result", "already_in_squad") {
		t.Fatal("a squad of one blocked accepting")
	}
	if got := h.squadIDOf(a); got == solo || got == "" || got != h.squadIDOf(c) {
		t.Errorf("a is in %q, want c's squad %q", got, h.squadIDOf(c))
	}
	pushes := h.drainPushes(a)
	if len(pushes) < 2 || rtOf(pushes[0]) != "YA_SquadLeave" || rtOf(pushes[1]) != "YA_SquadJoined" {
		t.Errorf("a's pushes %v, want YA_SquadLeave (its own squad) then YA_SquadJoined", pushes)
	}
	// b's invite from the dissolved squad is gone.
	if reply := h.accept(b, a); !squadField(reply, "result", "no_invite") && !squadField(reply, "result", "squad_gone") {
		t.Errorf("accepting an invite from a dissolved squad: %q", reply)
	}
	// A real squad (two members) still blocks.
	if reply := h.invite(b, a); !squadField(reply, "result", "user_in_squad") {
		t.Errorf("inviting a member of a real squad: %q, want user_in_squad", reply)
	}
}

// A direct message reaches its recipient and comes back to its sender, both
// carrying sender and recipient. The client names the recipient by its DASHED
// GUID (live request, 2026-10-06); it used to be looked up as given and every
// direct message was answered "recipient is offline".
func TestDirectMessage(t *testing.T) {
	hub := socialTestHub(t)
	const aID, bID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	a, aRead := socialTestPeer(t, hub, aID)
	_, bRead := socialTestPeer(t, hub, bID)
	aCh, bCh := pushLines(aRead), pushLines(bRead)
	res := handleChatMethod(socialRequest{method: "chat.user.message", peer: a, hub: hub,
		params: map[string]any{"recipient": dashedPlayerGUID(bID), "text": "test 123"}})
	if res["status"] != "success" {
		t.Fatalf("reply %v", res)
	}
	waitWhisper := func(ch <-chan map[string]any, who string) {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			select {
			case msg := <-ch:
				data, _ := msg["data"].(map[string]any)
				if msg["type"] != "chat.user.message" {
					continue
				}
				if data["text"] != "test 123" || data["sender"] != dashedPlayerGUID(aID) || data["recipient"] != dashedPlayerGUID(bID) {
					t.Errorf("%s got %v", who, data)
				}
				return
			case <-deadline:
				t.Fatalf("%s got no direct message", who)
			}
		}
	}
	waitWhisper(bCh, "recipient")
	waitWhisper(aCh, "sender (echo)")
}
