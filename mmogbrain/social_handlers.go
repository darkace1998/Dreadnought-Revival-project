package main

// Firmament request handling for chat, friends and ignores.
//
// Every handler returns the JSON-RPC `result` object for the request. Anything
// it needs to push to OTHER players it sends itself through the hub, because the
// caller only owns the one connection.
//
// Field names are doubled (channel/channel_name, pid/PID, message/content) for
// the same reason the MMOG payloads double theirs: the client resolves several
// of these by name and the exact casing it reads is not established for every
// one of them. Where the casing IS known -- "name" and "Name" in the market
// catalog, for instance -- only the known one is sent. These are cheap and the
// alternative is a silent empty panel.

import (
	"os"
	"strings"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
	"sort"
	"sync"
	"time"
)

// socialRequest is one decoded JSON-RPC call from a connected player.
type socialRequest struct {
	method string
	params map[string]any
	peer   *socialPeer
	hub    *socialHub
}

func (r socialRequest) str(names ...string) string {
	for _, name := range names {
		if value, ok := r.params[name].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

// channelName pulls the channel out of a request, defaulting to the global room
// so a malformed call lands somewhere valid rather than creating a nameless one.
func (r socialRequest) channelName() string {
	if name := r.str("channel", "channel_name", "channelName", "Channel", "room", "name"); name != "" {
		return name
	}
	return defaultChatChannels[0]
}

// targetPlayer pulls the other party out of a friend/whisper request.
func (r socialRequest) targetPlayer() string {
	return r.str("pid", "PID", "player_id", "playerId", "target", "user", "friend_id", "recipient", "to")
}

func socialOK(extra map[string]any) map[string]any {
	result := map[string]any{"status": "success"}
	for k, v := range extra {
		result[k] = v
	}
	return result
}

func socialError(message string) map[string]any {
	return map[string]any{"status": "error", "error": message, "message": message}
}

// handleSocialMethod runs one social call. The bool reports whether the method
// belongs to this subsystem at all; false means the caller should fall through
// to its own generic handling.
func handleSocialMethod(r socialRequest) (map[string]any, bool) {
	switch {
	case strings.HasPrefix(r.method, "chat."):
		return handleChatMethod(r), true
	case strings.HasPrefix(r.method, "presence.friends."),
		strings.HasPrefix(r.method, "presence.pending_friends."),
		strings.HasPrefix(r.method, "presence.ignore"):
		return handlePresenceSocialMethod(r), true
	case strings.HasPrefix(r.method, "user."):
		return handleUserMethod(r), true
	}
	return nil, false
}

// handleUserMethod answers the user.* family.
//
// These used to fall through to the generic `{"status":"success"}` reply, which
// is the shape of "your request was fine" and NOT of "here are the users". The
// client sends `user.search` seventeen times during a single login with the
// player's own STEAM PERSONA as the search term:
//
//	{"method":"user.search","params":{"terms":"DARKACE","limit":100,"offset":0}}
//
// That is the only place the client ever tells this server who Steam thinks it
// is -- there is no name field anywhere in the mmog protocol, YA_PlayerGet has
// none, and YA_GetPlayersInformation carries only
// infos/DisplayInfo/UnlockedFleetType/Elite/Rank. Answering "success, no users"
// left the client with no profile for itself, and the player's name blank.
func handleUserMethod(r socialRequest) map[string]any {
	switch r.method {
	case "user.whois":
		// Resolve player ids to names. FIXED 2026-09-28: answered with a bare
		// {"status":"success"}, so the client got no users and asked again
		// every ~60 ms forever ("Successfully sent request to resolve 1
		// usernames"), and names showed "//0". The message parser
		// (0x142A52390) reads the reply's TOP-LEVEL data.users[] (guid,
		// display_name, full_display_name, public_id, number, status_message,
		// is_*) into the list the whois callback (0x142AAA090) consumes.
		found := []any{}
		notFound := []any{}
		requester := ""
		if r.peer != nil {
			requester = r.peer.playerID
		}
		for _, id := range stringListParam(r.params, "users", "user", "ids") {
			pid := protocol.NormalizePlayerPID(id)
			if pid == "" || !playerExists(pid) {
				notFound = append(notFound, id)
				logWhois(requester, id, false)
				// Answer it anyway. The client re-asks for an id until a user
				// record with that guid comes back -- about 15 times a second,
				// forever ("Successfully sent request to resolve 1 usernames",
				// operator's log 2026-09-28) -- so an id we cannot resolve gets
				// a placeholder record rather than no record.
				found = append(found, unknownWhoisEntry(id))
				continue
			}
			logWhois(requester, id, true)
			found = append(found, r.hub.presenceEntry(pid))
			// Fill the client's name cache: only a user.profile push does
			// (see userProfileEvent); the reply alone left it re-asking.
			if r.peer != nil {
				peerID, online := "", false
				if other := r.hub.peerFor(pid); other != nil {
					peerID, online = other.peerID, true
				}
				_ = r.peer.send(userProfileEvent(pid, peerID, online))
			}
		}
		return socialOK(map[string]any{
			"users":           found,
			"users_not_found": notFound,
			firmamentRootData: map[string]any{"users": found, "users_not_found": notFound},
			firmamentRootType: "user.whois",
		})

	case "user.search":
		terms := strings.TrimSpace(stringParam(r.params, "terms", "term", "query"))
		adoptSteamPersona(r, terms)

		users := r.hub.searchUsers(terms, r.peer.playerID)
		// Several aliases for one list, the same defensive shape the chat
		// handlers use: the key the client reads is not recoverable from the
		// binary (user.search appears only as a literal, with no result parser
		// near it), and an extra key it ignores costs nothing while a missing
		// one costs the whole feature.
		// FIXED 2026-09-28: the client reads the results from the reply's
		// TOP-LEVEL data.users (UE handler 0x142A3AB80: root "data" ->
		// "users" array -> each "guid", "display_name"); everything below was
		// inside "result", so every search showed nothing.
		return socialOK(map[string]any{
			"users":           users,
			"results":         users,
			"listing":         users,
			"total":           len(users),
			firmamentRootData: map[string]any{"users": users},
		})
	}
	// Everything else in the family keeps the old behaviour rather than
	// inventing a shape for it.
	return socialOK(nil)
}

// adoptSteamPersona records the name the client just told us Steam knows it by.
//
// The original backend authenticated through Steam and would have had this from
// the ticket; we only ever see it here. It is taken as the player's display name
// when the stored one is still a placeholder or their bare login name, and never
// over a name they set themselves in captain customisation.
//
// It is a CLAIM -- the connection is authenticated but the string is not -- so
// it is length- and content-checked, and DN_NO_STEAM_PERSONA=1 turns it off.
func adoptSteamPersona(r socialRequest, terms string) {
	if terms == "" || os.Getenv("DN_NO_STEAM_PERSONA") == "1" {
		return
	}
	if len([]rune(terms)) > 32 || strings.ContainsAny(terms, "\r\n\t") {
		return
	}
	rememberPlayerDisplayName(r.peer.playerID, terms)
	if name := mmogPlayerStateForPID(r.peer.playerID).displayName; name != "" {
		r.peer.mu.Lock()
		r.peer.name = name
		r.peer.mu.Unlock()
	}
}

// stringParam reads the first present string parameter under any of the names.
func stringParam(params map[string]any, names ...string) string {
	for _, name := range names {
		if v, ok := params[name].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func handleChatMethod(r socialRequest) map[string]any {
	switch r.method {
	case "chat.channel.list":
		// Only the rooms this player is in. The client uses this to rebuild its
		// tab bar, so listing rooms it is not a member of would show tabs whose
		// messages never arrive.
		r.peer.mu.Lock()
		names := make([]string, 0, len(r.peer.channels))
		for name := range r.peer.channels {
			names = append(names, name)
		}
		r.peer.mu.Unlock()

		channels := make([]any, 0, len(names))
		for _, name := range names {
			channels = append(channels, r.hub.channelInfo(name))
		}
		return socialOK(map[string]any{"channels": channels, "listing": channels})

	case "chat.channel.info":
		return socialOK(map[string]any{"channel": r.hub.channelInfo(r.channelName())})

	case "chat.channel.join", "chat.channel.admin.create", "chat.channel.admin.forcejoin":
		name := r.channelName()
		if !r.hub.joinChannel(r.peer, name) {
			// Refused rather than created: the client's classifier only knows
			// six type tokens and silently drops anything else, so inventing
			// the channel here would look identical to a delivery failure.
			return socialError("unknown channel type: " + name)
		}
		info := r.hub.channelInfo(name)
		// Tell the room, so open clients update their user list without polling.
		r.hub.broadcast(name, "", map[string]any{
			"jsonrpc": "2.0",
			"method":  "chat.channel.notice",
			"params": map[string]any{
				"channel": name,
				"notice":  "join",
				"user":    r.hub.presenceEntry(r.peer.playerID),
			},
		})
		return socialOK(map[string]any{"channel": info, "channels": []any{info}})

	case "chat.channel.leave", "chat.channel.admin.close":
		name := r.channelName()
		r.hub.leaveChannel(r.peer, name)
		r.hub.broadcast(name, "", map[string]any{
			"jsonrpc": "2.0",
			"method":  "chat.channel.notice",
			"params": map[string]any{
				"channel": name,
				"notice":  "leave",
				"user":    r.hub.presenceEntry(r.peer.playerID),
			},
		})
		return socialOK(map[string]any{"channel": name})

	case "chat.channel.message", "chat.channel.notice":
		name := r.channelName()
		body := r.str("message", "content", "text", "body", "Message")
		if body == "" {
			return socialError("empty message")
		}
		persistMmogChatMessage(r.peer.playerID, name, body)
		notice := chatMessageNotice(r.method, name, r.hub.presenceEntry(r.peer.playerID), body)
		r.hub.broadcast(name, r.peer.playerID, notice)
		return socialOK(map[string]any{"channel": name})

	case "chat.user.message":
		target := r.targetPlayer()
		body := r.str("message", "content", "text", "body", "Message")
		if target == "" || body == "" {
			return socialError("whisper needs a recipient and a message")
		}
		if r.hub.ignores(target, r.peer.playerID) {
			// Reported as delivered. Telling the sender they are ignored is a
			// harassment vector, and the real service does not.
			return socialOK(nil)
		}
		peer := r.hub.peerFor(target)
		if peer == nil {
			return socialError("recipient is offline")
		}
		notice := chatMessageNotice(r.method, "", r.hub.presenceEntry(r.peer.playerID), body)
		_ = peer.send(notice)
		return socialOK(nil)

	case "chat.report":
		// Accepted and recorded in the log only. There is no moderation backend
		// and silently succeeding is better than an error the player cannot act
		// on -- but nothing here pretends a report was actioned.
		return socialOK(nil)
	}

	// Bans, invites, kicks and modes: acknowledged so the UI does not stall,
	// but deliberately not implemented. There is no channel-ownership model yet,
	// and a half-enforced ban is worse than none.
	return socialOK(map[string]any{"channel": r.channelName()})
}

func handlePresenceSocialMethod(r socialRequest) map[string]any {
	switch r.method {
	case "presence.friends.listing", "presence.friends.state", "presence.pending_friends.listing":
		// The client reads the lists from the ROOT "data" (see
		// friendListingData); "result" is kept for anything reading the old
		// place.
		friends, pending := r.hub.friendListing(r.peer.playerID)
		data := r.hub.friendListingData(r.peer.playerID)
		logrus.WithFields(logrus.Fields{
			"player": r.peer.playerID, "method": r.method,
			"friends": len(data["friends"].([]any)), "outgoing": len(data["pending_friends"].([]any)),
			"incoming": len(data["incoming_friend_requests"].([]any)),
		}).Info("social: friend listing")
		return socialOK(map[string]any{
			firmamentRootData: data,
			firmamentRootType: r.method,
			"friends":         friends,
			"listing":         friends,
			"pending_friends": pending,
		})

	case "presence.friends.add":
		target := r.targetPlayer()
		logrus.WithFields(logrus.Fields{"player": r.peer.playerID, "target": target, "params": paramKeys(r.params)}).
			Info("social: friend request")
		if target == "" || target == r.peer.playerID {
			return socialError("a friend request needs another player")
		}
		// Only real players. FIXED 2026-09-28: requests to a player that does
		// not exist were stored -- every "add friend" from the in-match
		// scoreboard targeted "ad000000-0000-..." (the client parsed the text
		// "INVALID" of an unset UniqueNetId; see result.pid in
		// buildMmogLoginSuccessPayload) and left a pending row nobody could
		// ever answer.
		if !playerExists(target) {
			return socialError("no such player")
		}
		if err := r.hub.addFriend(r.peer.playerID, target); err != nil {
			return socialError(err.Error())
		}
		// Two players who each asked have become friends (addFriend turns the
		// second request into an accept): tell BOTH, as a confirm would.
		if state, requester := r.hub.friendState(r.peer.playerID, target); state == "accepted" {
			// requestor is whoever asked FIRST, target the other one -- which
			// is this player only when the other side asked first. FIXED
			// 2026-09-28: target was always this player, so re-adding a friend
			// you had asked first sent requestor == target == you, and the
			// client listed the player as their own friend (operator).
			me := socialID(r.peer.playerID)
			other := socialID(target)
			confirmedTarget := me
			if socialID(requester) == me {
				confirmedTarget = other
			}
			r.hub.notifyFriendEvent(target, "presence.friends.friendrequestconfirmed", requester, confirmedTarget, r.peer.playerID)
			r.hub.notifyFriendEvent(r.peer.playerID, "presence.friends.friendrequestconfirmed", requester, confirmedTarget, target)
		} else {
			r.hub.notifyFriendEvent(target, "presence.friends.friendrequest", r.peer.playerID, target, r.peer.playerID)
		}
		// The client's add-result handler (0x142AA8A30) reads the target from
		// the raw reply's data.notice.target (a GUID string) and adds it to its
		// outgoing list; a bare "success" logged "Friend add request result.
		// Failed to parse target" (operator, 2026-09-28) although the request
		// was stored.
		r.hub.pushFriendListing(r.peer.playerID)
		r.hub.pushFriendListing(target)
		return socialOK(map[string]any{
			firmamentRootData: map[string]any{"notice": map[string]any{
				"action": "presence.friends.add",
				"status": "success",
				"target": dashedPlayerGUID(socialID(target)),
			}},
		})

	case "presence.friends.confirm":
		target := r.targetPlayer()
		if target == "" {
			return socialError("confirm needs a player")
		}
		if err := r.hub.confirmFriend(r.peer.playerID, target); err != nil {
			return socialError(err.Error())
		}
		// target asked; this player confirmed.
		r.hub.notifyFriendEvent(target, "presence.friends.friendrequestconfirmed", target, r.peer.playerID, r.peer.playerID)
		r.hub.pushFriendListing(r.peer.playerID)
		r.hub.pushFriendListing(target)
		return socialOK(nil)

	case "presence.friends.remove", "presence.friends.removepending":
		target := r.targetPlayer()
		if target == "" {
			return socialError("remove needs a player")
		}
		if err := r.hub.removeFriend(r.peer.playerID, target); err != nil {
			return socialError(err.Error())
		}
		event := "presence.friends.friendremoved"
		if r.method == "presence.friends.removepending" {
			event = "presence.friends.friendrequestcanceled"
		}
		r.hub.notifyFriendEvent(target, event, r.peer.playerID, target, r.peer.playerID)
		r.hub.pushFriendListing(r.peer.playerID)
		r.hub.pushFriendListing(target)
		return socialOK(nil)

	case "presence.ignore.listing":
		ignored := make([]any, 0)
		for _, pid := range r.hub.ignoreList(r.peer.playerID) {
			ignored = append(ignored, r.hub.presenceEntry(pid))
		}
		return socialOK(map[string]any{"listing": ignored, "ignores": ignored})

	case "presence.ignores.add":
		if target := r.targetPlayer(); target != "" {
			if err := r.hub.addIgnore(r.peer.playerID, target); err != nil {
				return socialError(err.Error())
			}
			r.hub.pushFriendListing(r.peer.playerID)
		}
		return socialOK(nil)

	case "presence.ignores.remove":
		if target := r.targetPlayer(); target != "" {
			if err := r.hub.removeIgnore(r.peer.playerID, target); err != nil {
				return socialError(err.Error())
			}
			r.hub.pushFriendListing(r.peer.playerID)
		}
		return socialOK(nil)
	}
	return socialOK(nil)
}

// notifyFriendEvent pushes one of the four server-initiated friend methods to a
// player if they are connected. Offline players pick the change up from their
// next listing, which is why nothing is queued here.
//
// SHAPE, from the client (verified in the binary 2026-09-28): the parser
// 0x142A52390 reads data.requestor -> message+0x7B0, data.target -> +0x7D0 and
// data.friend -> +0x7F0, each a GUID STRING. "Friend request received from %s"
// (0x142AA87A0) parses +0x7B0; the confirm handler (0x142AA8030) parses both
// +0x7B0 and +0x7D0 and compares them with its own GUID (+0x3A8); the removed
// handler (0x142AA857C) parses +0x7B0. requestor is the player who ASKED,
// target the other one.
// FIXED 2026-09-28: this was a JSON-RPC {method, params} frame -- the shape the
// client SENDS, which its dispatcher never routes (see chatChannelNotice) --
// and the recipient was looked up by the dashed GUID while peers are keyed by
// the 32-hex id, so nothing was sent at all ("no notification", operator).
func (h *socialHub) notifyFriendEvent(recipientID, method, requestorID, targetID, actorID string) {
	peer := h.peerFor(socialID(recipientID))
	fields := logrus.Fields{"event": method, "recipient": socialID(recipientID), "actor": socialID(actorID)}
	if peer == nil {
		logrus.WithFields(fields).Info("social: friend push NOT sent -- recipient offline (they get it from their next listing)")
		return
	}
	actor := socialID(actorID)
	// The actor's profile first, so the name cache has it when the handler
	// resolves the GUID (GetUsername would otherwise queue a whois).
	actorPeerID := ""
	if ap := h.peerFor(actor); ap != nil {
		actorPeerID = ap.peerID
	}
	_ = peer.send(userProfileEvent(actor, actorPeerID, actorPeerID != ""))
	data := h.friendListingData(socialID(recipientID))
	data["requestor"] = dashedPlayerGUID(socialID(requestorID))
	data["target"] = dashedPlayerGUID(socialID(targetID))
	data["friend"] = dashedPlayerGUID(actor)
	data["users"] = []any{h.presenceEntry(actor)}
	err := peer.send(firmamentEvent(method, data))
	if err != nil {
		logrus.WithFields(fields).WithError(err).Warn("social: friend push write failed")
		return
	}
	logrus.WithFields(fields).Info("social: friend push delivered")
}

// friendState is the stored state of the pair ("pending", "accepted") and who
// asked, or "" when there is no row.
func (h *socialHub) friendState(playerID, otherID string) (state, requester string) {
	other := socialID(otherID)
	for _, e := range h.friendsOf(socialID(playerID)) {
		if socialID(e.playerID) == other {
			return e.state, socialID(e.requester)
		}
	}
	return "", ""
}

// playerExists reports whether pid has a player record.
func playerExists(pid string) bool {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return true // no database (tests without one): do not block
	}
	var n int
	if err := database.QueryRow(`SELECT count(*) FROM player_state WHERE user_id=?`, normalizedPlayerStatePID(pid)).Scan(&n); err != nil {
		return true
	}
	return n > 0
}

// stringListParam reads the first present key as a list of strings (a single
// string is accepted as a one-element list).
func stringListParam(params map[string]any, keys ...string) []string {
	for _, k := range keys {
		switch v := params[k].(type) {
		case []any:
			out := make([]string, 0, len(v))
			for _, e := range v {
				if s, ok := e.(string); ok && s != "" {
					out = append(out, s)
				}
			}
			return out
		case string:
			if v != "" {
				return []string{v}
			}
		}
	}
	return nil
}

// whoisLogged rate-limits logUnresolvedWhois: the client re-asks for an
// unresolved id about 15 times a second ("Successfully sent request to
// resolve 1 usernames"), so each id is logged at most once a minute.
var whoisLogged sync.Map // id -> time.Time

// logWhois records a user.whois id, at most once a minute per id -- enough to
// see which id a client loops on (operator's client log, 2026-09-28).
func logWhois(requester, id string, found bool) {
	if last, ok := whoisLogged.Load(id); ok && time.Since(last.(time.Time)) < time.Minute {
		return
	}
	whoisLogged.Store(id, time.Now())
	logrus.WithFields(logrus.Fields{"player": requester, "id": id, "known": found}).Info("social: user.whois")
}

// unknownWhoisEntry is the record sent back for an id this server cannot
// resolve: the client's own guid, so its pending lookup completes.
func unknownWhoisEntry(id string) map[string]any {
	return map[string]any{
		"guid":              id,
		"pid":               id,
		"PID":               id,
		"name":              "Unknown player",
		"display_name":      "Unknown player",
		"full_display_name": "Unknown player",
		"number":            "0",
		"status":            "offline",
		"online":            false,
	}
}

// paramKeys lists a request's parameter names, for logs.
func paramKeys(params map[string]any) []string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
