package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
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
	method, _ := msg["type"].(string)
	if method == "" {
		t.Fatalf("chat event has no type, so the dispatcher cannot route it: %v", msg)
	}
	data, _ := msg["data"].(map[string]any)
	if data == nil {
		t.Fatalf("chat event has no data: %v", msg)
	}
	return method, data
}

// A channel type the client's classifier does not know must be refused, not
// created. UYMmogChat only registers all/team/squad/global/language/customroom
// (FUN_142a1f6d0) and drops anything else, so a channel we invent here would
// look exactly like a message-delivery bug from the player's side.
func TestChatRejectsUnknownChannelTypes(t *testing.T) {
	hub := socialTestHub(t)
	peer, _ := socialTestPeer(t, hub, "player-a")

	// The TYPE is the segment after the LAST dot -- "<id>.<type>". The
	// classifier scans backwards for '.', so a name without one cannot be
	// classified at all, and a name whose LAST segment is not one of the six
	// tokens classifies as unknown.
	for _, name := range []string{
		"dreadnought.global", "english.language", "42.squad", "1.team",
		"abc.customroom", "match.all",
	} {
		if _, ok := chatChannelType(name); !ok {
			t.Errorf("channel %q should be accepted; its last segment is one of the client's six types", name)
		}
	}
	for _, name := range []string{
		"global",           // no dot: "Failed to parse channel name"
		"all",              // no dot
		"language.english", // classifies on "english": "Message type unknown or unsupported"
		"global.eu",        // type-first is the wrong way round
		"lobby.trade", "", ".",
	} {
		if _, ok := chatChannelType(name); ok {
			t.Errorf("channel %q should be refused; the client cannot classify it", name)
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
		params: map[string]any{"channel": "dreadnought.global", "message": "hello"},
		peer:   sender, hub: hub,
	})

	method, params := readNotice(t, listenerRead)
	if method != "chat.channel.message" {
		t.Errorf("event method = %v, want chat.channel.message", method)
	}
	if params["message"] != "hello" {
		t.Errorf("event message = %v, want hello", params["message"])
	}
	if params["channel"] != "dreadnought.global" {
		t.Errorf("event channel = %v, want dreadnought.global", params["channel"])
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
		params: map[string]any{"channel": "dreadnought.global", "message": "blocked"},
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

// A friend request pushes presence.friends.friendrequest to the other player,
// in the {type, data} envelope the dispatcher routes, with data.requestor the
// asker's GUID string: "Friend request received from %s" (0x142AA87A0) parses
// message+0x7B0, which the parser fills from data.requestor. The target comes in
// dashed, as the client sends it, while peers are keyed by the 32-hex id -- the
// lookup that used to find no one (operator, 2026-09-28: "no notification").
func TestFriendRequestPushesToTarget(t *testing.T) {
	hub := socialTestHub(t)
	const pa, pb = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	a, _ := socialTestPeer(t, hub, pa)
	_, bRead := socialTestPeer(t, hub, pb)

	go handlePresenceSocialMethod(socialRequest{
		method: "presence.friends.add",
		params: map[string]any{"user": dashedPlayerGUID(pb)},
		peer:   a, hub: hub,
	})

	if method, data := readNotice(t, bRead); method != "user.profile" || data["guid"] != dashedPlayerGUID(pa) {
		t.Errorf("first push %v guid=%v, want the requester's user.profile", method, data["guid"])
	}
	method, data := readNotice(t, bRead)
	if method != "presence.friends.friendrequest" {
		t.Errorf("push type = %v, want presence.friends.friendrequest", method)
	}
	if data["requestor"] != dashedPlayerGUID(pa) || data["target"] != dashedPlayerGUID(pb) {
		t.Errorf("requestor=%v target=%v, want %s / %s", data["requestor"], data["target"], dashedPlayerGUID(pa), dashedPlayerGUID(pb))
	}
}

// Two players who each asked are friends; both get the confirm, with requestor
// the one who asked first.
func TestMutualFriendRequestConfirmsBoth(t *testing.T) {
	hub := socialTestHub(t)
	const pa, pb = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	_, aRead := socialTestPeer(t, hub, pa)
	b, _ := socialTestPeer(t, hub, pb)
	if err := hub.addFriend(pa, pb); err != nil {
		t.Fatal(err)
	}
	go handlePresenceSocialMethod(socialRequest{
		method: "presence.friends.add",
		params: map[string]any{"user": dashedPlayerGUID(pa)},
		peer:   b, hub: hub,
	})
	readNotice(t, aRead) // b's profile
	method, data := readNotice(t, aRead)
	if method != "presence.friends.friendrequestconfirmed" || data["requestor"] != dashedPlayerGUID(pa) || data["target"] != dashedPlayerGUID(pb) {
		t.Errorf("got %v requestor=%v target=%v, want friendrequestconfirmed %s / %s", method, data["requestor"], data["target"], dashedPlayerGUID(pa), dashedPlayerGUID(pb))
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
	if members := hub.channelMembers("dreadnought.global"); len(members) != 1 {
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
	notice := chatJoinNotice("dreadnought.global", map[string]any{"pid": "player-a"})

	// The METHOD goes in "type": the dispatcher routes on one string, comparing
	// it against "server.notice" first and "chat.channel.notice" later, which is
	// the same slot that carries "pong". A "method"/"params" frame is the shape
	// the CLIENT sends, and one was logged in !!IN!! and ignored.
	if notice["type"] != "chat.channel.notice" {
		t.Errorf("join event type = %v, want chat.channel.notice", notice["type"])
	}
	params, _ := notice["data"].(map[string]any)
	if params == nil {
		t.Fatal("join event has no data")
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
	if params["channel"] != "dreadnought.global" {
		t.Errorf("join event channel = %v, want dreadnought.global", params["channel"])
	}
}

// The requester arrives undashed and the target as the client's dashed GUID;
// both must land in one form, or the target never sees or accepts the request
// (2026-09-28).
func TestFriendRequestAcrossIDFormats(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const a = "0123456789abcdef0123456789abcdef"
	const b = "fedcba9876543210fedcba9876543210"
	if err := socialHubInstance.addFriend(a, dashedPlayerGUID(b)); err != nil {
		t.Fatal(err)
	}
	pending := socialHubInstance.friendsOf(b)
	if len(pending) != 1 || pending[0].state != "pending" || pending[0].playerID != a {
		t.Fatalf("target's view: %+v", pending)
	}
	if err := socialHubInstance.confirmFriend(b, dashedPlayerGUID(a)); err != nil {
		t.Fatal(err)
	}
	if got := socialHubInstance.friendsOf(a); len(got) != 1 || got[0].state != "accepted" {
		t.Fatalf("after confirm: %+v", got)
	}
}

// A player sent to a match is joined to its MatchAll and MatchTeam rooms and
// told so; an earlier match's rooms are left (2026-09-28).
func TestJoinMatchChannels(t *testing.T) {
	hub := socialTestHub(t)
	peer, _ := socialTestPeer(t, hub, "0123456789abcdef0123456789abcdef")
	hub.joinMatchChannels(peer.playerID, "m1", 2)
	peer.mu.Lock()
	in := map[string]bool{}
	for k := range peer.channels {
		in[k] = true
	}
	peer.mu.Unlock()
	if !in["m1.all"] || !in["m1-2.team"] {
		t.Fatalf("channels after match m1: %v", in)
	}
	hub.joinMatchChannels(peer.playerID, "m2", 1)
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.channels["m1.all"] || peer.channels["m1-2.team"] || !peer.channels["m2.all"] || !peer.channels["m2-1.team"] {
		t.Fatalf("channels after match m2: %v", peer.channels)
	}
}

// _OnChatChannelMessage (0x142AA7100) parses data.sender as a GUID string; an
// object read as "" gave "GetUsername called with empty guid" and a nameless
// chat line (operator's client log, 2026-09-28).
func TestChatMessageSenderIsGUIDString(t *testing.T) {
	const pid = "0123456789abcdef0123456789abcdef"
	notice := chatMessageNotice("chat.channel.message", "m1.all", map[string]any{"pid": dashedPlayerGUID(pid)}, "hi")
	data := notice["data"].(map[string]any)
	want := "01234567-89ab-cdef-0123-456789abcdef"
	if data["sender"] != want {
		t.Errorf("data.sender = %#v, want the dashed GUID string %q", data["sender"], want)
	}
	if data["text"] != "hi" || data["channel"] != "m1.all" {
		t.Errorf("data.text=%v data.channel=%v, want hi / m1.all", data["text"], data["channel"])
	}
}

// Re-adding a friend you asked first: requestor is you, target the OTHER player.
// Both used to be you, and the client listed the player as their own friend.
func TestReAddingAnAcceptedFriendNeverNamesYourself(t *testing.T) {
	hub := socialTestHub(t)
	const pa, pb = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	a, aRead := socialTestPeer(t, hub, pa)
	_, bRead := socialTestPeer(t, hub, pb)
	if err := hub.addFriend(pa, pb); err != nil {
		t.Fatal(err)
	}
	if err := hub.addFriend(pb, pa); err != nil {
		t.Fatal(err)
	}
	go handlePresenceSocialMethod(socialRequest{
		method: "presence.friends.add",
		params: map[string]any{"user": dashedPlayerGUID(pb)},
		peer:   a, hub: hub,
	})
	for name, r := range map[string]*bufio.Reader{"target": bRead, "requester": aRead} {
		readNotice(t, r) // profile
		_, data := readNotice(t, r)
		if data["requestor"] != dashedPlayerGUID(pa) || data["target"] != dashedPlayerGUID(pb) {
			t.Errorf("%s got requestor=%v target=%v, want %s / %s", name, data["requestor"], data["target"], dashedPlayerGUID(pa), dashedPlayerGUID(pb))
		}
	}
}

// The listing must reach the client's parser: root "data", each element
// {friend: "<guid>"}, outgoing and incoming requests in separate lists. It went
// out under "result" as presence objects, and every client restart showed an
// empty friend list (operator, 2026-09-28).
func TestFriendListingIsInTheShapeTheClientParses(t *testing.T) {
	hub := socialTestHub(t)
	const me, friend, asked, asker = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"cccccccccccccccccccccccccccccccc", "dddddddddddddddddddddddddddddddd"
	peer, _ := socialTestPeer(t, hub, me)
	for _, pair := range [][2]string{{me, friend}, {friend, me}, {me, asked}, {asker, me}} {
		if err := hub.addFriend(pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	res := handlePresenceSocialMethod(socialRequest{method: "presence.friends.listing", peer: peer, hub: hub})
	result, _ := res["result"].(map[string]any)
	if result == nil {
		result = res
	}
	data, ok := result[firmamentRootData].(map[string]any)
	if !ok {
		t.Fatalf("listing reply has no root data: %v", res)
	}
	one := func(key, want string) {
		list, _ := data[key].([]any)
		if len(list) != 1 || list[0].(map[string]any)["friend"] != dashedPlayerGUID(want) {
			t.Errorf("data.%s = %v, want one element with friend %s", key, list, dashedPlayerGUID(want))
		}
	}
	one("friends", friend)
	one("pending_friends", asked)
	one("incoming_friend_requests", asker)
}

func TestOnlineListsConnectedPlayersLoopbackOnly(t *testing.T) {
	for addr, want := range map[string]int{"127.0.0.1:5000": http.StatusOK, "10.0.0.5:5000": http.StatusForbidden} {
		req := httptest.NewRequest(http.MethodGet, "/online", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		onlineHandler(rec, req)
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", addr, rec.Code, want)
		}
	}
}

// The friend list is pushed as a presence.friends.listing EVENT: the client
// never requests it (it routes the type as an inbound event, 0x142A8DFA0).
func TestFriendListingIsPushed(t *testing.T) {
	hub := socialTestHub(t)
	const me, friend, ignored = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc"
	_, read := socialTestPeer(t, hub, me)
	_ = hub.addFriend(me, friend)
	_ = hub.addFriend(friend, me)
	_ = hub.addIgnore(me, ignored)
	go hub.pushFriendListing(me)
	method, data := readNotice(t, read)
	if method != "presence.friends.listing" {
		t.Fatalf("push type %q, want presence.friends.listing", method)
	}
	friends, _ := data["friends"].([]any)
	if len(friends) != 1 || friends[0].(map[string]any)["friend"] != dashedPlayerGUID(friend) {
		t.Errorf("data.friends = %v, want the one friend's GUID", friends)
	}
	ign, _ := data["ignores"].([]any)
	if len(ign) != 1 || ign[0].(map[string]any)["ignored"] != dashedPlayerGUID(ignored) {
		t.Errorf("data.ignores = %v, want {ignored: guid}", ign)
	}
}

// Coming online and going offline reach every connected friend as
// presence.friends.state, after the player's profile.
func TestFriendStateIsBroadcastOnlineAndOffline(t *testing.T) {
	hub := socialTestHub(t)
	const me, friend = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	_, friendRead := socialTestPeer(t, hub, friend)
	_ = hub.addFriend(me, friend)
	_ = hub.addFriend(friend, me)
	mePeer, _ := socialTestPeer(t, hub, me)

	go hub.broadcastFriendState(me, true)
	readNotice(t, friendRead) // profile
	method, data := readNotice(t, friendRead)
	if method != "presence.friends.state" || data["friend"] != dashedPlayerGUID(me) || data["status"] != float64(1) {
		t.Errorf("online push: %s %v, want presence.friends.state friend=%s status=1", method, data, dashedPlayerGUID(me))
	}

	go hub.leave(mePeer)
	readNotice(t, friendRead) // profile
	method, data = readNotice(t, friendRead)
	if method != "presence.friends.state" || data["status"] != float64(0) {
		t.Errorf("offline push: %s %v, want status 0", method, data)
	}
}
