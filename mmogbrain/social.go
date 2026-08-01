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
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
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
// client creates exactly these two itself at startup, so if we do not have them
// its join goes to a channel that does not exist.
var defaultChatChannels = []string{"global", "language.english"}

// chatChannelType returns the type token of a channel name, and whether it is
// one the client will accept.
func chatChannelType(name string) (string, bool) {
	token := name
	if idx := strings.Index(name, "."); idx >= 0 {
		token = name[:idx]
	}
	token = strings.ToLower(token)
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
	writeMu  sync.Mutex

	mu       sync.Mutex
	channels map[string]bool
	status   string
	message  string
}

func (p *socialPeer) send(payload any) error {
	line, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	line = append(line, '\r', '\n')
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.conn.Write(line)
	return err
}

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
	h.mu.Lock()
	if h.peers[peer.playerID] == peer {
		delete(h.peers, peer.playerID)
	}
	for name, members := range h.channels {
		if members[peer.playerID] {
			delete(members, peer.playerID)
			if len(members) == 0 {
				delete(h.channels, name)
			}
		}
	}
	h.mu.Unlock()
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

// friendPairKey orders a pair so one friendship is one row whichever side asks.
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
	database := h.db()
	if database == nil {
		return nil
	}
	a, b := friendPairKey(playerID, otherID)
	_, err := database.Exec(`DELETE FROM player_friends WHERE pid_a = ? AND pid_b = ?`, a, b)
	return err
}

func (h *socialHub) ignoreList(playerID string) []string {
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
	for _, id := range h.ignoreList(playerID) {
		if id == otherID {
			return true
		}
	}
	return false
}

func (h *socialHub) addIgnore(playerID, otherID string) error {
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
	entry := map[string]any{
		"pid":     playerID,
		"PID":     playerID,
		"peer_id": "",
		"name":    "",
		"status":  "offline",
		"message": "",
		"online":  false,
	}
	if peer != nil {
		peer.mu.Lock()
		status, message := peer.status, peer.message
		peer.mu.Unlock()
		if status == "" {
			status = "online"
		}
		entry["peer_id"] = peer.peerID
		entry["name"] = peer.name
		entry["status"] = status
		entry["message"] = message
		entry["online"] = true
	}
	return entry
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
	for _, pid := range members {
		users = append(users, h.presenceEntry(pid))
	}
	channelType, _ := chatChannelType(name)
	return map[string]any{
		"channel":      name,
		"channel_name": name,
		"name":         name,
		"type":         channelType,
		"users":        users,
		"members":      users,
		"user_count":   len(users),
		"modes":        []any{},
	}
}

// chatMessageNotice is the server-initiated delivery of one chat line. It is
// sent with the method name the client uses for the same operation, so the
// client's own dispatcher routes it.
func chatMessageNotice(method string, channel string, sender map[string]any, body string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params": map[string]any{
			"channel":      channel,
			"channel_name": channel,
			"message":      body,
			"content":      body,
			"sender":       sender,
			"from":         sender,
			"timestamp":    time.Now().Unix(),
			"id":           uuid.New().String(),
		},
	}
}
