package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/db"
)

// socialTestHub builds a hub backed by a throwaway database so friend state is
// exercised for real rather than through a nil-db short circuit.
func socialTestHub(t *testing.T) *socialHub {
	t.Helper()
	database, err := db.Open(t.TempDir() + "/social.db")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return &socialHub{
		peers:    map[string]*socialPeer{},
		channels: map[string]map[string]bool{},
		db:       func() *sql.DB { return database },
	}
}

// socialTestPeer wires a peer to one end of a pipe so pushes can be read back.
func socialTestPeer(t *testing.T, hub *socialHub, playerID string) (*socialPeer, *bufio.Reader) {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	peer := newSocialPeer(playerID, playerID+"-peer", server)
	t.Cleanup(peer.close)
	hub.join(peer)
	return peer, bufio.NewReader(client)
}

func readPush(t *testing.T, r *bufio.Reader) map[string]any {
	t.Helper()
	_ = r.Buffered()
	type result struct {
		line []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := r.ReadBytes('\n')
		ch <- result{line, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("read push: %v", res.err)
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(res.line), &msg); err != nil {
			t.Fatalf("push is not JSON: %v (%q)", err, res.line)
		}
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a push")
		return nil
	}
}

// readNotice unwraps a chat event and returns its params.
//
// Chat events are routed by the client's inbound dispatcher (FUN_142a8dfa0) on
// the frame's METHOD, so they must carry one -- the server.notice envelope our
// auth success uses has no method and the client logged such a frame in !!IN!!
// and did nothing with it.
func readNotice(t *testing.T, r *bufio.Reader) (string, map[string]any) {
	t.Helper()
	msg := readPush(t, r)
	method, _ := msg["method"].(string)
	if method == "" {
		t.Fatalf("chat event has no method, so the client cannot route it: %v", msg)
	}
	params, _ := msg["params"].(map[string]any)
	if params == nil {
		t.Fatalf("chat event has no params: %v", msg)
	}
	return method, params
}

// A channel type the client's classifier does not know must be refused, not
// created. UYMmogChat only registers all/team/squad/global/language/customroom
// (FUN_142a1f6d0) and drops anything else, so a channel we invent here would
// look exactly like a message-delivery bug from the player's side.
func TestChatRejectsUnknownChannelTypes(t *testing.T) {
	hub := socialTestHub(t)
	peer, _ := socialTestPeer(t, hub, "player-a")

	for _, name := range []string{"global", "language.english", "squad.42", "team.1", "customroom.abc", "all"} {
		if _, ok := chatChannelType(name); !ok {
			t.Errorf("channel %q should be accepted; it is one of the client's six types", name)
		}
	}
	for _, name := range []string{"lobby", "trade.eu", "whisper", ""} {
		if _, ok := chatChannelType(name); ok {
			t.Errorf("channel %q should be refused; the client's classifier has no such type", name)
		}
	}

	res := handleChatMethod(socialRequest{
		method: "chat.channel.join",
		params: map[string]any{"channel": "lobby"},
		peer:   peer, hub: hub,
	})
	if res["status"] != "error" {
		t.Errorf("joining an unknown channel type returned %v; it must be refused", res["status"])
	}
}

// Every player is placed in the two rooms the client creates for itself at
// startup ("Adding Chat room type Global." / "English."). Without them the
// client's own join targets a channel that does not exist.
func TestPlayersJoinTheClientsDefaultRooms(t *testing.T) {
	hub := socialTestHub(t)
	peer, _ := socialTestPeer(t, hub, "player-a")

	peer.mu.Lock()
	defer peer.mu.Unlock()
	for _, name := range defaultChatChannels {
		if !peer.channels[name] {
			t.Errorf("player was not placed in default room %q", name)
		}
	}
}

// A channel message reaches the other members and not the sender's own socket
// twice.
func TestChatMessageReachesOtherMembers(t *testing.T) {
	hub := socialTestHub(t)
	sender, _ := socialTestPeer(t, hub, "player-a")
	_, listenerRead := socialTestPeer(t, hub, "player-b")

	go handleChatMethod(socialRequest{
		method: "chat.channel.message",
		params: map[string]any{"channel": "global", "message": "hello"},
		peer:   sender, hub: hub,
	})

	method, params := readNotice(t, listenerRead)
	if method != "chat.channel.message" {
		t.Errorf("event method = %v, want chat.channel.message", method)
	}
	if params["message"] != "hello" {
		t.Errorf("event message = %v, want hello", params["message"])
	}
	if params["channel"] != "global" {
		t.Errorf("event channel = %v, want global", params["channel"])
	}
}

// An ignored sender's channel traffic must not reach the ignoring player.
func TestIgnoredSenderIsNotBroadcastTo(t *testing.T) {
	hub := socialTestHub(t)
	sender, _ := socialTestPeer(t, hub, "player-a")
	listener, listenerRead := socialTestPeer(t, hub, "player-b")

	if err := hub.addIgnore(listener.playerID, sender.playerID); err != nil {
		t.Fatalf("addIgnore: %v", err)
	}
	go handleChatMethod(socialRequest{
		method: "chat.channel.message",
		params: map[string]any{"channel": "global", "message": "blocked"},
		peer:   sender, hub: hub,
	})

	// Nothing should arrive. A short wait is enough: the broadcast is synchronous
	// once the goroutine runs.
	done := make(chan struct{})
	go func() {
		_, _ = listenerRead.ReadBytes('\n')
		close(done)
	}()
	select {
	case <-done:
		t.Error("an ignored sender's message was delivered")
	case <-time.After(300 * time.Millisecond):
	}
}

// Friendship is one row per pair whichever side asks, and a mutual add resolves
// to accepted rather than leaving both sides pending forever.
func TestFriendRequestLifecycle(t *testing.T) {
	hub := socialTestHub(t)
	a, _ := socialTestPeer(t, hub, "player-a")
	b, _ := socialTestPeer(t, hub, "player-b")

	if err := hub.addFriend(a.playerID, b.playerID); err != nil {
		t.Fatalf("addFriend: %v", err)
	}
	friends, pending := hub.friendListing(b.playerID)
	if len(friends) != 0 || len(pending) != 1 {
		t.Fatalf("after a request: friends=%d pending=%d, want 0 and 1", len(friends), len(pending))
	}
	if incoming, _ := pending[0].(map[string]any)["incoming"].(bool); !incoming {
		t.Error("the request should read as INCOMING to the player who did not ask")
	}
	_, senderPending := hub.friendListing(a.playerID)
	if incoming, _ := senderPending[0].(map[string]any)["incoming"].(bool); incoming {
		t.Error("the request should read as OUTGOING to the player who asked")
	}

	// The requester may not accept their own request.
	if err := hub.confirmFriend(a.playerID, b.playerID); err != nil {
		t.Fatalf("confirmFriend: %v", err)
	}
	if friends, _ = hub.friendListing(a.playerID); len(friends) != 0 {
		t.Error("a requester was able to confirm their own friend request")
	}

	if err := hub.confirmFriend(b.playerID, a.playerID); err != nil {
		t.Fatalf("confirmFriend: %v", err)
	}
	for _, pid := range []string{a.playerID, b.playerID} {
		friends, pending = hub.friendListing(pid)
		if len(friends) != 1 || len(pending) != 0 {
			t.Errorf("%s: friends=%d pending=%d after confirm, want 1 and 0", pid, len(friends), len(pending))
		}
	}

	if err := hub.removeFriend(a.playerID, b.playerID); err != nil {
		t.Fatalf("removeFriend: %v", err)
	}
	if friends, _ = hub.friendListing(b.playerID); len(friends) != 0 {
		t.Error("friend survived removal")
	}
}

// Two people adding each other must end up friends, not stuck pending.
func TestMutualAddResolvesToFriends(t *testing.T) {
	hub := socialTestHub(t)
	if err := hub.addFriend("player-a", "player-b"); err != nil {
		t.Fatalf("addFriend: %v", err)
	}
	if err := hub.addFriend("player-b", "player-a"); err != nil {
		t.Fatalf("addFriend: %v", err)
	}
	friends, pending := hub.friendListing("player-a")
	if len(friends) != 1 || len(pending) != 0 {
		t.Errorf("mutual add gave friends=%d pending=%d, want 1 and 0", len(friends), len(pending))
	}
}

// A friend request pushes one of the four server-initiated methods to the other
// player if they are connected.
func TestFriendRequestPushesToTarget(t *testing.T) {
	hub := socialTestHub(t)
	a, _ := socialTestPeer(t, hub, "player-a")
	_, bRead := socialTestPeer(t, hub, "player-b")

	go handlePresenceSocialMethod(socialRequest{
		method: "presence.friends.add",
		params: map[string]any{"pid": "player-b"},
		peer:   a, hub: hub,
	})

	msg := readPush(t, bRead)
	if msg["method"] != "presence.friends.friendrequest" {
		t.Errorf("push method = %v, want presence.friends.friendrequest", msg["method"])
	}
}

// A second connection for the same player replaces the first, or every message
// to that player would be written to a dead socket first.
func TestReconnectReplacesThePreviousPeer(t *testing.T) {
	hub := socialTestHub(t)
	first, _ := socialTestPeer(t, hub, "player-a")
	second, _ := socialTestPeer(t, hub, "player-a")

	if got := hub.peerFor("player-a"); got != second {
		t.Error("the newest connection should be the one the hub delivers to")
	}
	if members := hub.channelMembers("global"); len(members) != 1 {
		t.Errorf("global has %d members after a reconnect, want 1", len(members))
	}
	_ = first
}

// A channel-join event must be a chat.channel.NOTICE carrying "join", not a
// frame whose method is chat.channel.join.
//
// The inbound dispatcher (FUN_142a8dfa0) routes on the method, comparing it
// against globals that resolve to chat.channel.notice / .info / .message and
// chat.user.message. Only for chat.channel.notice does it then switch on a value
// compared against "join" (DAT_1438cdba4) and "leave". "chat.channel.join" is
// the name of the REQUEST a client sends; no inbound frame is routed by it, and
// sending one is why the client received our join in !!IN!! and still reported
// "channel name is empty".
func TestChatJoinEventUsesTheNoticeMethod(t *testing.T) {
	notice := chatJoinNotice("global", map[string]any{"pid": "player-a"})

	if notice["method"] != "chat.channel.notice" {
		t.Errorf("join event method = %v, want chat.channel.notice", notice["method"])
	}
	params, _ := notice["params"].(map[string]any)
	if params == nil {
		t.Fatal("join event has no params")
	}
	joins := 0
	for _, key := range []string{"notice", "event", "action", "state"} {
		if params[key] == "join" {
			joins++
		}
	}
	if joins == 0 {
		t.Errorf(`join event carries no "join" value the dispatcher can match: %v`, params)
	}
	if params["channel"] != "global" {
		t.Errorf("join event channel = %v, want global", params["channel"])
	}
}
