package main

// Social: chat channels, friends and ignores over the Firmament JSON-RPC
// socket.
//
// The client's whole social surface is 32 methods, recovered from its own
// string table rather than guessed:
//
//	chat.channel.list  .info  .join  .leave  .message  .notice
//	chat.channel.admin.create  .admin.close  .admin.forcejoin
//	chat.channel.kick  .addbans  .removebans  .addinvites  .removeinvites
//	chat.channel.setmodes      chat.user.message      chat.report
//	presence.friends.listing  .add  .confirm  .remove  .removepending  .state
//	presence.pending_friends.listing
//	presence.ignore.listing   presence.ignores.add   presence.ignores.remove
//	presence.status.set  .setmessage   presence.data.list
//
// plus four the SERVER sends unprompted:
//
//	presence.friends.friendrequest  .friendrequestconfirmed
//	presence.friends.friendrequestcanceled  .friendremoved
//
// Squad is NOT here. It runs on the MMOG binary channel as YA_Squad* and is
// handled by the response dispatcher.
//
// Channel names are "<type>" or "<type>.<id>". UYMmogChat's classifier
// (FUN_142a1f6d0) registers exactly six type tokens -- all, team, squad, global,
// language, customroom -- and OnUserJoinedChannel (FUN_142a377d0) scans the name
// for a '.' to split type from id, so anything outside that set is dropped
// before it reaches a room. The client announces the two it wants at startup:
// "Adding Chat room type Global." and "Adding Chat room type English.".

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// Channel type tokens the client's classifier accepts. A name whose type is not
// one of these is refused rather than silently created, because the client will
// drop it anyway and a silent drop is indistinguishable from a delivery bug.
const (
	chatTypeAll        = "all"
	chatTypeTeam       = "team"
	chatTypeSquad      = "squad"
	chatTypeGlobal     = "global"
	chatTypeLanguage   = "language"
	chatTypeCustomRoom = "customroom"
)

var chatChannelTypes = map[string]bool{
	chatTypeAll:        true,
	chatTypeTeam:       true,
	chatTypeSquad:      true,
	chatTypeGlobal:     true,
	chatTypeLanguage:   true,
	chatTypeCustomRoom: true,
}

// defaultChatChannels are the rooms every player is placed in on connect. The
// client creates exactly these two itself at startup ("Adding Chat room type
// Global." / "English.") and waits to be told their names.
//
// The form is "<id>.<type>", NOT "<type>.<id>" -- see chatChannelType.
var defaultChatChannels = []string{"dreadnought.global", "english.language"}

// chatChannelType returns the type token of a channel name, and whether it is
// one the client will accept.
//
// The type is the segment AFTER THE LAST DOT. UYMmogChat's classifier
// (FUN_142a1f6d0) scans the name backwards for '.', bails out returning type 0
// when there is none, and compares only the SUFFIX against its six tokens:
//
//	while (p = p - 1, *p != 0x2e) { if (p == start) return 0; }
//	...
//	if (compare(suffix, "all") == 0) type = 1;   // then team, squad, global, ...
//
// So the id comes first and the type last: "english.language", "<match>.team",
// "<squad>.squad", "<cluster>.global". Getting this backwards produced both of
// the errors seen live -- a bare "global" has no dot at all ("Failed to parse
// channel name. Outgoing chat will not be available"), and "language.english"
// parses but classifies on "english", which is not a type
// ("OnChatChannelJoined: Message type unknown or unsupported").
func chatChannelType(name string) (string, bool) {
	idx := strings.LastIndex(name, ".")
	if idx < 0 {
		// No dot at all: the client cannot classify it and says so.
		return "", false
	}
	token := strings.ToLower(name[idx+1:])
	return token, chatChannelTypes[token]
}

// socialPeer is one authenticated Firmament connection.
//
// Writes are serialised per connection: chat fans a message out to every member
// of a channel from whichever goroutine received it, so two deliveries can race
// on the same socket and interleave halves of two JSON lines.
type socialPeer struct {
	playerID string
	peerID   string
	name     string
	conn     net.Conn

	// out is this connection's outbound queue, drained by one writer goroutine.
	//
	// Delivery must NEVER block the sender. A channel broadcast walks every
	// member from whichever goroutine received the message, so writing straight
	// to the sockets means the slowest reader in the room decides how long
	// everyone else waits for their copy -- and a client that has stopped
	// reading entirely wedges the broadcast permanently. Caught by the tests:
	// one unread peer hung the whole suite for ten minutes.
	out    chan []byte
	closed chan struct{}
	once   sync.Once

	mu       sync.Mutex
	channels map[string]bool
	status   string
	message  string
}

// socialPeerQueueDepth is how far a connection may fall behind before its
// messages start being dropped. Chat is not worth unbounded memory, and a peer
// this far behind is not reading.
const socialPeerQueueDepth = 64

func newSocialPeer(playerID, peerID string, conn net.Conn) *socialPeer {
	peer := &socialPeer{
		playerID: playerID,
		peerID:   peerID,
		// Without this every presence entry, chat user and friend listing went
		// out with name:"" -- presenceEntry copies peer.name, and nothing ever
		// assigned it.
		name:     mmogPlayerStateForPID(playerID).displayName,
		conn:     conn,
		out:      make(chan []byte, socialPeerQueueDepth),
		closed:   make(chan struct{}),
		channels: map[string]bool{},
		status:   "online",
	}
	go peer.writeLoop()
	return peer
}

// writeRaw queues an already-encoded line. Every write to a Firmament socket
// has to go through here.
//
// The pong, auth and JSON-RPC result paths used to write to the connection
// directly while this peer's writer goroutine wrote to the same socket, which is
// two goroutines on one TCP stream with no shared lock -- interleaved bytes, and
// a frame the client silently drops. It was invisible for as long as nothing was
// ever pushed unprompted.
func (p *socialPeer) writeRaw(line []byte) error {
	select {
	case <-p.closed:
		return errSocialPeerClosed
	default:
	}
	select {
	case p.out <- line:
		return nil
	case <-p.closed:
		return errSocialPeerClosed
	default:
		return errSocialPeerBacklogged
	}
}

func (p *socialPeer) writeLoop() {
	for {
		select {
		case line := <-p.out:
			if _, err := p.conn.Write(line); err != nil {
				p.close()
				return
			}
		case <-p.closed:
			return
		}
	}
}

func (p *socialPeer) close() {
	p.once.Do(func() { close(p.closed) })
}

// send queues a message. It reports an error only when the peer is gone or so
// far behind that dropping is the right answer; it never waits on the socket.
func (p *socialPeer) send(payload any) error {
	line, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	line = append(line, '\r', '\n')
	return p.writeRaw(line)
}

var (
	errSocialPeerClosed     = errors.New("social: peer connection closed")
	errSocialPeerBacklogged = errors.New("social: peer is not reading, message dropped")
)

// socialHub tracks who is connected and which channels they are in.
//
// Membership is deliberately NOT persisted: a channel is only meaningful while
// someone is connected to it, and a stale membership row would make the server
// fan messages out to a socket that is gone. Friends and ignores ARE persisted,
// because those outlive the session.
type socialHub struct {
	mu    sync.RWMutex
	peers map[string]*socialPeer // playerID -> peer
	// channels maps a channel name to its members. A channel exists for as long
	// as it has one.
	channels map[string]map[string]bool
	log      *logrus.Logger
	db       func() *sql.DB
}

var socialHubInstance = &socialHub{
	peers:    map[string]*socialPeer{},
	channels: map[string]map[string]bool{},
	db:       currentMmogPlayerStateDB,
}

func (h *socialHub) setLogger(log *logrus.Logger) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.log == nil {
		h.log = log
	}
}

// join adds a peer and puts them in the default rooms. A second connection for
// the same player replaces the first: the client reconnects on its own after a
// drop, and keeping the dead socket would mean every message to that player got
// written to a closed connection first.
func (h *socialHub) join(peer *socialPeer) {
	h.mu.Lock()
	previous := h.peers[peer.playerID]
	h.peers[peer.playerID] = peer
	h.mu.Unlock()

	if previous != nil && previous != peer {
		h.leave(previous)
	}
	for _, name := range defaultChatChannels {
		h.joinChannel(peer, name)
	}
}

func (h *socialHub) leave(peer *socialPeer) {
	peer.close()

	h.mu.Lock()
	// Channel membership is keyed by PLAYER, not by connection, so a peer that
	// has already been replaced by a reconnect must not tear down the
	// replacement's membership on its way out.
	if h.peers[peer.playerID] != peer {
		h.mu.Unlock()
		return
	}
	// The player really went offline: tell their friends, once the lock is
	// released (the push looks peers up).
	defer func() {
		if h.db != nil {
			h.broadcastFriendState(peer.playerID, false)
		}
	}()
	defer h.mu.Unlock()
	delete(h.peers, peer.playerID)
	for name, members := range h.channels {
		if members[peer.playerID] {
			delete(members, peer.playerID)
			if len(members) == 0 {
				delete(h.channels, name)
			}
		}
	}
}

func (h *socialHub) joinChannel(peer *socialPeer, name string) bool {
	if _, ok := chatChannelType(name); !ok {
		return false
	}
	h.mu.Lock()
	if h.channels[name] == nil {
		h.channels[name] = map[string]bool{}
	}
	h.channels[name][peer.playerID] = true
	h.mu.Unlock()

	peer.mu.Lock()
	peer.channels[name] = true
	peer.mu.Unlock()
	return true
}

func (h *socialHub) leaveChannel(peer *socialPeer, name string) {
	h.mu.Lock()
	if members := h.channels[name]; members != nil {
		delete(members, peer.playerID)
		if len(members) == 0 {
			delete(h.channels, name)
		}
	}
	h.mu.Unlock()

	peer.mu.Lock()
	delete(peer.channels, name)
	peer.mu.Unlock()
}

func (h *socialHub) channelMembers(name string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	members := make([]string, 0, len(h.channels[name]))
	for pid := range h.channels[name] {
		members = append(members, pid)
	}
	sort.Strings(members)
	return members
}

func (h *socialHub) peerFor(playerID string) *socialPeer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.peers[playerID]
}

func (h *socialHub) peersIn(name string) []*socialPeer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*socialPeer, 0, len(h.channels[name]))
	for pid := range h.channels[name] {
		if peer := h.peers[pid]; peer != nil {
			out = append(out, peer)
		}
	}
	return out
}

// broadcast delivers a notification to every member of a channel except those
// ignoring the sender. A failed write is logged and dropped rather than
// returned: one dead socket must not stop the rest of a room receiving.
func (h *socialHub) broadcast(channel string, senderID string, payload any) {
	for _, peer := range h.peersIn(channel) {
		if senderID != "" && peer.playerID != senderID && h.ignores(peer.playerID, senderID) {
			continue
		}
		if err := peer.send(payload); err != nil && h.log != nil {
			h.log.WithError(err).WithFields(logrus.Fields{
				"pid": peer.playerID, "channel": channel,
			}).Debug("social: broadcast write failed")
		}
	}
}

// --- friends -----------------------------------------------------------------

// dashedPlayerGUID renders a 32-hex player id in 8-4-4-4-12 form.
//
// The client parses these as GUIDs, and an undashed id parses to all zeros: it
// logged "User 00000000-0000-0000-0000-000000000000 joined channel" for a pid we
// sent as bare hex. Ids that are not 32 hex characters are passed through
// unchanged.
func dashedPlayerGUID(playerID string) string {
	if len(playerID) != 32 {
		return playerID
	}
	for _, c := range playerID {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return playerID
		}
	}
	return playerID[0:8] + "-" + playerID[8:12] + "-" + playerID[12:16] + "-" +
		playerID[16:20] + "-" + playerID[20:32]
}

// friendPairKey orders a pair so one friendship is one row whichever side asks.
// socialID is the one form a player id is stored and compared in by the
// friend and ignore tables: 32 lowercase hex, no dashes. FIXED 2026-09-28: the
// requester arrived undashed (the connection's id) and the target dashed (the
// client's GUID text), so a pair was stored in mixed forms and the target's
// own lookups -- listing his pending requests, accepting one -- never matched.
func socialID(id string) string {
	if n := protocol.NormalizePlayerPID(id); n != "" {
		return n
	}
	return id
}

func friendPairKey(a, b string) (string, string) {
	if a <= b {
		return a, b
	}
	return b, a
}

type friendEntry struct {
	playerID  string
	state     string // "accepted", "pending"
	requester string
}

func (h *socialHub) friendsOf(playerID string) []friendEntry {
	playerID = socialID(playerID)
	database := h.db()
	if database == nil || playerID == "" {
		return nil
	}
	rows, err := database.Query(
		`SELECT pid_a, pid_b, requester_id, state FROM player_friends WHERE pid_a = ? OR pid_b = ?`,
		playerID, playerID)
	if err != nil {
		return nil
	}
	defer rows.Close() //nolint:errcheck

	var out []friendEntry
	for rows.Next() {
		var a, b, requester, state string
		if rows.Scan(&a, &b, &requester, &state) != nil {
			continue
		}
		other := a
		if other == playerID {
			other = b
		}
		out = append(out, friendEntry{playerID: other, state: state, requester: requester})
	}
	return out
}

func (h *socialHub) addFriend(playerID, otherID string) error {
	playerID = socialID(playerID)
	otherID = socialID(otherID)
	database := h.db()
	if database == nil {
		return nil
	}
	a, b := friendPairKey(playerID, otherID)
	// A request from the other side that is still pending becomes an accept --
	// otherwise two people adding each other would sit pending forever, each
	// waiting for a confirm the other has already effectively given.
	_, err := database.Exec(
		`INSERT INTO player_friends(pid_a, pid_b, requester_id, state) VALUES(?,?,?,'pending')
		 ON CONFLICT(pid_a, pid_b) DO UPDATE SET state =
		   CASE WHEN player_friends.state = 'pending' AND player_friends.requester_id <> ?
		        THEN 'accepted' ELSE player_friends.state END`,
		a, b, playerID, playerID)
	return err
}

func (h *socialHub) confirmFriend(playerID, otherID string) error {
	playerID = socialID(playerID)
	otherID = socialID(otherID)
	database := h.db()
	if database == nil {
		return nil
	}
	a, b := friendPairKey(playerID, otherID)
	// Only the side that did NOT ask may confirm; otherwise a requester could
	// accept their own request.
	_, err := database.Exec(
		`UPDATE player_friends SET state = 'accepted'
		 WHERE pid_a = ? AND pid_b = ? AND requester_id <> ?`, a, b, playerID)
	return err
}

func (h *socialHub) removeFriend(playerID, otherID string) error {
	playerID = socialID(playerID)
	otherID = socialID(otherID)
	database := h.db()
	if database == nil {
		return nil
	}
	a, b := friendPairKey(playerID, otherID)
	_, err := database.Exec(`DELETE FROM player_friends WHERE pid_a = ? AND pid_b = ?`, a, b)
	return err
}

func (h *socialHub) ignoreList(playerID string) []string {
	playerID = socialID(playerID)
	database := h.db()
	if database == nil || playerID == "" {
		return nil
	}
	rows, err := database.Query(`SELECT ignored_id FROM player_ignores WHERE pid = ?`, playerID)
	if err != nil {
		return nil
	}
	defer rows.Close() //nolint:errcheck
	var out []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

func (h *socialHub) ignores(playerID, otherID string) bool {
	playerID = socialID(playerID)
	otherID = socialID(otherID)
	for _, id := range h.ignoreList(playerID) {
		if id == otherID {
			return true
		}
	}
	return false
}

func (h *socialHub) addIgnore(playerID, otherID string) error {
	playerID = socialID(playerID)
	otherID = socialID(otherID)
	database := h.db()
	if database == nil {
		return nil
	}
	_, err := database.Exec(
		`INSERT INTO player_ignores(pid, ignored_id) VALUES(?,?) ON CONFLICT DO NOTHING`,
		playerID, otherID)
	return err
}

func (h *socialHub) removeIgnore(playerID, otherID string) error {
	playerID = socialID(playerID)
	otherID = socialID(otherID)
	database := h.db()
	if database == nil {
		return nil
	}
	_, err := database.Exec(`DELETE FROM player_ignores WHERE pid = ? AND ignored_id = ?`, playerID, otherID)
	return err
}

// --- wire shapes -------------------------------------------------------------

// socialPresenceEntry is how one player appears in a friends or channel listing.
// online is derived from the hub rather than stored, so it cannot go stale.
func (h *socialHub) presenceEntry(playerID string) map[string]any {
	peer := h.peerFor(playerID)
	guid := dashedPlayerGUID(playerID)
	storedName := mmogPlayerStateForPID(playerID).displayName
	if storedName == "Local" {
		storedName = ""
	}
	entry := map[string]any{
		"pid":     guid,
		"PID":     guid,
		"guid":    guid,
		"peer_id": "",
		// The account's display name, online or not: user.search lists
		// offline players too, and the client reads display_name.
		"name":              storedName,
		"display_name":      storedName,
		"full_display_name": storedName,
		"number":            playerNumber(playerID),
		"status":            "offline",
		"message":           "",
		"online":            false,
	}
	if peer != nil {
		peer.mu.Lock()
		status, message := peer.status, peer.message
		peer.mu.Unlock()
		if status == "" {
			status = "online"
		}
		entry["peer_id"] = peer.peerID
		if peer.name != "" {
			entry["name"] = peer.name
			entry["display_name"] = peer.name
			entry["full_display_name"] = peer.name
		}
		entry["status"] = status
		entry["message"] = message
		entry["online"] = true
	}
	return entry
}

// friendListingData is the friend state in the shape the client parses, for
// the root "data" of a listing reply and of the friend events.
//
// SHAPE, from the parser 0x142A52390 (verified in the binary 2026-09-28):
//
//	data.friends[]                  {friend, status, status_message, is_away, is_idle}
//	data.pending_friends[]          {friend}   -> message+0x828
//	data.incoming_friend_requests[] {friend}   -> message+0x840
//
// friend is the other player's GUID STRING (read with the string getter
// 0x142A94CE0), status goes through the int getter it also uses for "hashed",
// is_away/is_idle through the bool getter. FIXED 2026-09-28: the listing went
// out under JSON-RPC "result", which this parser never reads, as presence
// objects with no "friend" key -- so the client started every session with an
// empty friend list although the rows were stored ("if I restart the client
// all friends are gone", operator).
//
// GUESS: pending_friends holds OUTGOING requests and incoming_friend_requests
// the incoming ones -- read from the names and the client's separate
// "Couldn't find Incoming/Outgoing friend request" lookups; which list feeds
// which UI has not been traced. status 1/0 (online/offline) matches what
// userProfileEvent sends; the enum itself is untraced.
func (h *socialHub) friendListingData(playerID string) map[string]any {
	me := socialID(playerID)
	friends := make([]any, 0)
	outgoing := make([]any, 0)
	incoming := make([]any, 0)
	for _, entry := range h.friendsOf(me) {
		other := socialID(entry.playerID)
		item := map[string]any{"friend": dashedPlayerGUID(other)}
		if entry.state == "accepted" {
			status, message := 0, ""
			if peer := h.peerFor(other); peer != nil {
				status = 1
				peer.mu.Lock()
				message = peer.message
				peer.mu.Unlock()
			}
			item["status"] = status
			item["status_message"] = message
			item["is_away"] = false
			item["is_idle"] = false
			friends = append(friends, item)
			continue
		}
		if socialID(entry.requester) == me {
			outgoing = append(outgoing, item)
		} else {
			incoming = append(incoming, item)
		}
	}
	return map[string]any{
		"friends":                  friends,
		"pending_friends":          outgoing,
		"incoming_friend_requests": incoming,
	}
}

func (h *socialHub) friendListing(playerID string) ([]any, []any) {
	var friends, pending []any
	for _, entry := range h.friendsOf(playerID) {
		item := h.presenceEntry(entry.playerID)
		item["requester_id"] = entry.requester
		if entry.state == "accepted" {
			friends = append(friends, item)
			continue
		}
		// A pending row is a REQUEST to whoever did not ask for it, and an
		// outgoing INVITE to whoever did.
		item["incoming"] = entry.requester != playerID
		pending = append(pending, item)
	}
	if friends == nil {
		friends = []any{}
	}
	if pending == nil {
		pending = []any{}
	}
	return friends, pending
}

func (h *socialHub) channelInfo(name string) map[string]any {
	members := h.channelMembers(name)
	users := make([]any, 0, len(members))
	// members is a list of GUID STRINGS. The parser (0x142A52390) converts
	// each data.members element to text and the channel-info handler
	// (0x142A376A0) then parses those as GUIDs and resolves each name. They
	// were user objects, which read as "" -- a zero GUID per member, and the
	// client logged "GetUsername called with empty guid" for each one
	// (operator's log, 2026-09-28). users keeps the full records.
	memberIDs := make([]any, 0, len(members))
	for _, pid := range members {
		users = append(users, h.presenceEntry(pid))
		memberIDs = append(memberIDs, dashedPlayerGUID(pid))
	}
	channelType, _ := chatChannelType(name)
	return map[string]any{
		"channel":      name,
		"channel_name": name,
		"name":         name,
		"type":         channelType,
		"users":        users,
		"members":      memberIDs,
		"user_count":   len(users),
		"modes":        []any{},
	}
}

// firmamentNotice wraps a server-initiated event in the envelope the client
// actually accepts.
//
// Incoming frames are {"id","type","data"} -- NOT JSON-RPC. The two shapes we
// have watched the client accept are the pong
// ({"data":{...},"id":...,"type":"pong"}) and our own auth success, which is a
// "server.notice" whose data.notice carries an "action" naming the method it
// answers. Events follow the auth one, because that is the only server-initiated
// message this client is known to act on.
func firmamentNotice(action string, fields map[string]any) map[string]any {
	notice := map[string]any{
		"status": "success",
		"action": action,
		"method": action,
	}
	for k, v := range fields {
		notice[k] = v
	}
	return map[string]any{
		"id":   uuid.New().String(),
		"type": "server.notice",
		"data": map[string]any{"notice": notice},
	}
}

// chatJoinNotice tells a client the NAME of a channel it is now in.
//
// This is the message the whole chat window hangs on. UYMmogChat keeps one
// channel-name string per room type (MatchAll at +0x1a8, MatchTeam at +0x1b8,
// and so on) and SendChat refuses outright when the slot is empty:
//
//	if (1 < *(int *)(param_1 + 0x1b0)) { send using the stored name }
//	else "SendChat to MatchAll failed: channel name is empty"
//
// Those slots are only ever written by OnUserJoinedChannel (FUN_142a377d0),
// bound to the Firmament delegate at +0x100. The client never asks -- it creates
// its Global and English room types at startup and waits.
//
// SHAPE, from the inbound dispatcher FUN_142a8dfa0. It routes on the frame's
// METHOD, compared against four runtime-built globals which resolve to
// chat.channel.notice, chat.channel.info, chat.channel.message and
// chat.user.message. For chat.channel.notice it then switches on a value
// compared against the literals "join" (DAT_1438cdba4) and "leave":
//
//	if (method == chat.channel.notice) {
//	    if (value == "join")  { store the channel; add the user }
//	    else if (value == "leave") { remove the user }
//	}
//
// So a join is NOT method "chat.channel.join" -- that is only the name of the
// request a client sends. The event is a chat.channel.NOTICE whose value is
// "join". An earlier attempt used the server.notice envelope that carries our
// auth success; the client logged the frame in !!IN!! and did nothing with it,
// because that envelope has no method for this dispatcher to route on.
func chatJoinNotice(channel string, user map[string]any) map[string]any {
	return chatChannelNotice(channel, "join", user)
}

// chatChannelNotice builds a join/leave membership event.
//
// ENVELOPE: {"id", "type", "data"} with the METHOD IN "type".
//
// The inbound dispatcher FUN_142a8dfa0 routes on ONE string, and that string is
// compared first against the global that resolves to "server.notice" and later
// against the ones for "chat.channel.notice", "chat.channel.info",
// "chat.channel.message" and "chat.user.message". One field, all of those
// values -- so it is the frame's TYPE, the same slot that carries "pong" and
// "server.notice" in the frames this client already accepts, and the payload
// rides in "data" beside it exactly as those do.
//
// A JSON-RPC-shaped frame with "method" and "params" is therefore ignored: the
// client logged ours in !!IN!! verbatim and still reported "channel name is
// empty", because the dispatcher never found a type to route on. "method" and
// "params" are the shape the CLIENT sends, not the shape it reads.
func chatChannelNotice(channel string, event string, user map[string]any) map[string]any {
	return firmamentEvent("chat.channel.notice", map[string]any{
		// PROVEN by a runtime probe against the live client, not guessed. A
		// sentinel was placed under every plausible key at three levels (the
		// frame, data, and data.notice) and the client named the winners in its
		// own log: "User <pid> joined channel D_channel", where D_ was the
		// data-level prefix. So both of these live directly in data.
		"action":  event,
		"channel": channel,
		// The joining user's GUID as a STRING, here as in notice.user.
		// FIXED 2026-09-28: this was the whole user object. The join handler
		// (0x142A377D0) logged "User 00000000-0000-0000-0000-000000000000
		// joined channel dreadnought.global" and then "GetUsername called with
		// empty guid" (operator's client log): the user it parsed was the zero
		// GUID, which is not the player's own (+0x3A8), so the join counted as
		// SOMEONE ELSE's and the channel name -- which only an own join stores
		// -- stayed empty, and chat failed. The data-level fields are the ones
		// this parser reads (see the probe note above); an object read as a
		// string is "".
		"user":  noticeUserGUID(user),
		"users": []any{user},
		// Kept as a sibling because the join/leave value resolved to the nested
		// copy in an earlier probe round. Harmless, and cheaper than another
		// round to decide which of the two the parser prefers.
		"notice": map[string]any{
			"action":  event,
			"channel": channel,
			// The joining user's GUID as a STRING. FIXED 2026-09-27: this was
			// the user object. The parser (0x142A52390) reads data.notice.user
			// as a string into message+0x590; the channel handler
			// (0x142AA7450) parses it as a GUID and the join handler
			// (0x142A377D0) compares it with the player's own profile GUID
			// (+0x3A8): equal means "I joined", which is the ONLY path that
			// sets the channel name; different means "someone else joined".
			// An object stringified to "" -> zero GUID, which matched the
			// all-zero own GUID by accident until the user.profile push set a
			// real one; then every client logged "channel name is empty".
			"user":  noticeUserGUID(user),
			"users": []any{user},
		},
	})
}

// noticeUserGUID is the dashed GUID of a presence entry (see presenceEntry).
func noticeUserGUID(user map[string]any) string {
	if g, ok := user["pid"].(string); ok {
		return g
	}
	return ""
}

// firmamentEvent wraps a server-initiated event in the envelope the client's
// dispatcher routes: the method goes in "type", the payload in "data".
func firmamentEvent(method string, data map[string]any) map[string]any {
	payload := map[string]any{"status": "success", "method": method, "action": method}
	for k, v := range data {
		payload[k] = v
	}
	return map[string]any{
		"id":   uuid.New().String(),
		"type": method,
		"data": payload,
	}
}

// chatMessageNotice is the server-initiated delivery of one chat line. Same
// envelope as the membership notice: the dispatcher routes chat.channel.message
// and chat.user.message the same way it routes chat.channel.notice.
//
// The parser (0x142A52390) reads data.channel -> message+0x530, data.text ->
// +0x5B0 and data.sender -> +0x570, all as strings. _OnChatChannelMessage
// (0x142AA7100) parses +0x570 as a GUID (0x142AA5940 -> 0x142A584B0) and
// resolves the name with GetUsername, which fills from the user.profile cache
// or queues a user.whois.
// FIXED 2026-09-28: sender went out as the whole presence object, which the
// parser reads as "" -> zero GUID -> "GetUsername called with empty guid"
// (operator's client log, 19:09:23 client time) and a nameless chat line.
func chatMessageNotice(method string, channel string, sender map[string]any, body string) map[string]any {
	guid := noticeUserGUID(sender)
	return firmamentEvent(method, map[string]any{
		"channel":      channel,
		"channel_name": channel,
		"name":         channel,
		"message":      body,
		"content":      body,
		"text":         body,
		"sender":       guid,
		"from":         guid,
		"user":         guid,
		"users":        []any{sender},
		"timestamp":    time.Now().Unix(),
	})
}

// firmamentSelfProfile describes the connected player to themselves.
//
// The client keeps its own profile GUID at chat+0x3a8 and refuses to render an
// incoming chat line without it -- "_OnChatChannelMessage: user profile was not
// setup!" -- because the guard is FUN_142a65f80(this + 0x3a8), an all-zeros
// test. The peer id we send in client_id is the CONNECTION's identity, not the
// player's, so it never filled that slot.
func firmamentSelfProfile(playerID, peerID string) map[string]any {
	guid := dashedPlayerGUID(playerID)
	// name/nickname went out as "" for every player. The account's real name is
	// known -- it arrives in the JWT's "username" claim at gateway login and is
	// stored on the player row from there (rememberPlayerDisplayName) -- so an
	// empty string here was a gap, not an absence of data.
	name := mmogPlayerStateForPID(playerID).displayName
	return map[string]any{
		"pid":      guid,
		"PID":      guid,
		"guid":     guid,
		"user_id":  guid,
		"peer_id":  peerID,
		"name":     name,
		"nickname": name,
		"status":   "online",
		"message":  "",
		"online":   true,
	}
}

// selfUserProfileEvent is the push that sets the client's own profile, the
// prerequisite of the whole social layer. Five handlers -- chat channel and
// user messages, friend confirm/cancel/remove -- warn "user profile was not
// setup!" and drop the event while the 16 bytes at FirmamentClient+0x3A8 are
// zero (0x142A65F80). Verified from the decompile, 2026-09-27:
//
//   - The client registers a callback for event type "user.profile"
//     (0x142AA18D0 -> 0x142AA1290 -> 0x142AA9B60). Its parser (0x142A52390)
//     keeps "data" whole for any type it has no special case for.
//   - The dispatcher (0x142A8DFA0) stores data in the profile map keyed by
//     data.guid; the record builder (0x142A8CE00) reads guid, full_display_name,
//     display_name, number, public_id, status_message, global_silence_expires
//     and the is_* / status flags.
//   - The callback is keyed by data.profile, and 0x142AA9B60 accepts it only if
//     it equals the client's own id -- server.notice data.notice.user_token --
//     then parses the record's guid as a GUID into +0x3A8.
//
// So guid, profile and user_token must all be the same dashed GUID.
func selfUserProfileEvent(playerID, peerID string) map[string]any {
	return userProfileEvent(playerID, peerID, true)
}

// userProfileEvent is a user.profile push for any player. The dispatcher
// (0x142A8DFA0) stores every user.profile's data in the profile map keyed by
// data.guid, and that map is the name cache GetUsername (0x142AA3030) reads:
// guid -> lowercase dashed text -> lookup at (+0x480)+0x70, and a miss queues
// the guid for another user.whois on the next tick. A user.whois REPLY never
// fills it, so for every other player the client re-asked ~15 times a second
// forever and showed "Name//0" (operator, 2026-09-28) -- only the player's own
// number showed, because only it arrived as a user.profile. online reports
// whether the player is connected.
func userProfileEvent(playerID, peerID string, online bool) map[string]any {
	guid := dashedPlayerGUID(playerID)
	name := mmogPlayerStateForPID(playerID).displayName
	if name == "Local" {
		name = ""
	}
	status := 0
	if online {
		status = 1
	}
	return firmamentEvent("user.profile", map[string]any{
		"guid":              guid,
		"profile":           guid,
		"public_id":         guid,
		"number":            playerNumber(playerID),
		"display_name":      name,
		"full_display_name": name,
		"status_message":    "",
		"is_online":         online,
		"is_idle":           false,
		"is_away":           false,
		"is_private":        false,
		"is_admin":          false,
		// Numeric in the record builder (0x142A8CE00 sets online = status == 1);
		// overrides the envelope's "success", which would read as offline.
		"status":  status,
		"peer_id": peerID,
	})
}

// searchUsers returns the players whose display name matches a search term.
//
// Case-insensitive, substring, and it always includes the searcher when their
// own name matches -- the client's automatic user.search at login is looking for
// itself, so excluding the requester (the usual instinct for a people search)
// would answer the one question it is actually asking with nothing.
func (h *socialHub) searchUsers(terms, requesterID string) []any {
	want := normalizeSearchName(terms)
	if want == "" {
		return []any{}
	}

	seen := map[string]bool{}
	out := []any{}
	add := func(playerID string) {
		if playerID == "" || seen[playerID] || len(out) >= 100 {
			return
		}
		name := mmogPlayerStateForPID(playerID).displayName
		if name == "" || name == "Local" || !strings.Contains(normalizeSearchName(name), want) {
			return
		}
		seen[playerID] = true
		out = append(out, h.presenceEntry(playerID))
	}

	// The searcher first (the client's automatic search at login looks for
	// itself), then everyone else -- FIXED 2026-09-28: only ONLINE players
	// were searched, so an offline friend could never be found.
	add(requesterID)
	for _, id := range allPlayerIDs() {
		add(id)
	}
	h.mu.Lock()
	ids := make([]string, 0, len(h.peers))
	for id := range h.peers {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	for _, id := range ids {
		add(id)
	}
	return out
}

// normalizeSearchName folds case and drops spaces, underscores and hyphens, so
// "some tester" finds "Some_Tester".
func normalizeSearchName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer(" ", "", "_", "", "-", "").Replace(s)
}

// allPlayerIDs lists every player with a record (the default dev player is
// excluded by its placeholder name in searchUsers).
func allPlayerIDs() []string {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return nil
	}
	rows, err := database.Query(`SELECT user_id FROM player_state ORDER BY updated_at DESC LIMIT 1000`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func (h *socialHub) onlinePlayerIDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]string, 0, len(h.peers))
	for id := range h.peers {
		ids = append(ids, id)
	}
	return ids
}

// playerNumber is the player's name discriminator (shown with the name, like
// Name#1234), as the text the client parses with sscanf("%d") -- both parsers
// (0x142A52390 users lists, 0x142A8CE00 profile records) read "number" as a
// string, so a missing one showed every player as 0. Derived from the account
// id: stable across logins and needs no stored column. 1000-9999; collisions
// only matter for display.
func playerNumber(playerID string) string {
	pid := normalizedPlayerStatePID(playerID)
	return strconv.Itoa(int(crc32.ChecksumIEEE([]byte(pid))%9000) + 1000)
}

// joinMatchChannels puts a player into their match's chat rooms: "<match>.all"
// (MatchAll) and "<match>-<team>.team" (MatchTeam). In-match chat failed with
// "SendChat to MatchAll failed: channel name is empty" (operator's client log,
// 2026-09-28): those slots, like Global's, are only filled by a join notice
// naming the player himself (0x142A377D0), and nothing ever sent one for a
// match. Called when the travel push (YA_Connect) goes out. Rooms of an earlier
// match are left first, so a player is only ever in one match's chat.
func (h *socialHub) joinMatchChannels(playerID, matchID string, team int32) {
	peer := h.peerFor(socialID(playerID))
	if peer == nil || matchID == "" {
		return
	}
	all := matchID + ".all"
	teamRoom := fmt.Sprintf("%s-%d.team", matchID, team)

	peer.mu.Lock()
	var old []string
	for name := range peer.channels {
		if t, _ := chatChannelType(name); (t == chatTypeAll || t == chatTypeTeam) && name != all && name != teamRoom {
			old = append(old, name)
		}
	}
	peer.mu.Unlock()
	for _, name := range old {
		h.leaveChannel(peer, name)
	}

	entry := h.presenceEntry(peer.playerID)
	for _, name := range []string{all, teamRoom} {
		if h.joinChannel(peer, name) {
			_ = peer.send(chatJoinNotice(name, entry))
		}
	}
}
