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
	"strings"
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
	}
	return nil, false
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
	case "presence.friends.listing", "presence.friends.state":
		friends, pending := r.hub.friendListing(r.peer.playerID)
		return socialOK(map[string]any{
			"friends":         friends,
			"listing":         friends,
			"pending_friends": pending,
		})

	case "presence.pending_friends.listing":
		_, pending := r.hub.friendListing(r.peer.playerID)
		return socialOK(map[string]any{
			"pending_friends": pending,
			"listing":         pending,
			"friends":         pending,
		})

	case "presence.friends.add":
		target := r.targetPlayer()
		if target == "" || target == r.peer.playerID {
			return socialError("a friend request needs another player")
		}
		if err := r.hub.addFriend(r.peer.playerID, target); err != nil {
			return socialError(err.Error())
		}
		r.hub.notifyFriendEvent(target, "presence.friends.friendrequest", r.peer.playerID)
		return socialOK(nil)

	case "presence.friends.confirm":
		target := r.targetPlayer()
		if target == "" {
			return socialError("confirm needs a player")
		}
		if err := r.hub.confirmFriend(r.peer.playerID, target); err != nil {
			return socialError(err.Error())
		}
		r.hub.notifyFriendEvent(target, "presence.friends.friendrequestconfirmed", r.peer.playerID)
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
		r.hub.notifyFriendEvent(target, event, r.peer.playerID)
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
		}
		return socialOK(nil)

	case "presence.ignores.remove":
		if target := r.targetPlayer(); target != "" {
			if err := r.hub.removeIgnore(r.peer.playerID, target); err != nil {
				return socialError(err.Error())
			}
		}
		return socialOK(nil)
	}
	return socialOK(nil)
}

// notifyFriendEvent pushes one of the four server-initiated friend methods to a
// player if they are connected. Offline players pick the change up from their
// next listing, which is why nothing is queued here.
func (h *socialHub) notifyFriendEvent(targetPlayerID, method, actorPlayerID string) {
	peer := h.peerFor(targetPlayerID)
	if peer == nil {
		return
	}
	actor := h.presenceEntry(actorPlayerID)
	friends, pending := h.friendListing(targetPlayerID)
	_ = peer.send(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params": map[string]any{
			"pid":             actorPlayerID,
			"PID":             actorPlayerID,
			"user":            actor,
			"friend":          actor,
			"friends":         friends,
			"pending_friends": pending,
		},
	})
}
