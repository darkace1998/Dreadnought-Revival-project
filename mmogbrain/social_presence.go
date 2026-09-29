package main

import "github.com/sirupsen/logrus"

// Friend presence is PUSHED, never asked for.
//
// Verified in the binary 2026-09-29: at login the client sends only
// presence.status.setmessage and presence.data.list (raw Firmament log: zero
// presence.friends.listing requests across ~40 logins), while its inbound
// dispatcher (0x142A8DFA0) routes "presence.friends.listing" and
// "presence.friends.state" as event TYPES, next to friendrequest and
// chat.channel.message. So the original service pushed the friend list and
// every friend's state; we answered a listing request the client never makes,
// and every session started with an empty list ("if I restart the client all
// friends are gone", operator 2026-09-28) although the rows were stored.

// friendListingEvent is the whole friend state as the listing push: friends,
// outgoing and incoming requests (friendListingData) and the ignore list. The
// parser reads data.ignores[] elements' "ignored" (a GUID string) into
// message+0x858.
func (h *socialHub) friendListingEvent(playerID string) map[string]any {
	data := h.friendListingData(playerID)
	ignored := make([]any, 0)
	for _, pid := range h.ignoreList(socialID(playerID)) {
		ignored = append(ignored, map[string]any{"ignored": dashedPlayerGUID(socialID(pid))})
	}
	data["ignores"] = ignored
	return firmamentEvent("presence.friends.listing", data)
}

// pushFriendListing sends a connected player their friend state.
func (h *socialHub) pushFriendListing(playerID string) {
	peer := h.peerFor(socialID(playerID))
	if peer == nil {
		return
	}
	ev := h.friendListingEvent(playerID)
	data := ev["data"].(map[string]any)
	fields := logrus.Fields{
		"player": socialID(playerID), "friends": len(data["friends"].([]any)),
		"outgoing": len(data["pending_friends"].([]any)), "incoming": len(data["incoming_friend_requests"].([]any)),
		"ignores": len(data["ignores"].([]any)),
	}
	if err := peer.send(ev); err != nil {
		logrus.WithFields(fields).WithError(err).Warn("social: friend listing push failed")
		return
	}
	logrus.WithFields(fields).Info("social: friend listing pushed")
}

// friendStateEvent is one friend's presence, as the presence.friends.state
// arm of the parser reads it (0x142A5576A..0x142A55976, only when the type is
// presence.friends.state): data.friend (GUID string), status (int getter),
// status_message (string), is_away / is_idle (bool getter).
//
// GUESS: status 1 = online, 0 = offline, the values userProfileEvent sends;
// the enum is untraced. is_away follows a client-set status of "away".
func (h *socialHub) friendStateEvent(playerID string, online bool) map[string]any {
	status, message, away := 0, "", false
	if online {
		status = 1
		if peer := h.peerFor(socialID(playerID)); peer != nil {
			peer.mu.Lock()
			message = peer.message
			away = peer.status == "away"
			peer.mu.Unlock()
		}
	}
	return firmamentEvent("presence.friends.state", map[string]any{
		"friend":         dashedPlayerGUID(socialID(playerID)),
		"status":         status,
		"status_message": message,
		"is_away":        away,
		"is_idle":        false,
	})
}

// broadcastFriendState tells every connected friend that playerID came online,
// went offline, or changed status.
func (h *socialHub) broadcastFriendState(playerID string, online bool) {
	me := socialID(playerID)
	sent := 0
	for _, entry := range h.friendsOf(me) {
		if entry.state != "accepted" {
			continue
		}
		peer := h.peerFor(socialID(entry.playerID))
		if peer == nil {
			continue
		}
		// The friend's client resolves the name from its cache; give it the
		// profile first, as for friend requests.
		peerID := ""
		if online {
			if p := h.peerFor(me); p != nil {
				peerID = p.peerID
			}
		}
		_ = peer.send(userProfileEvent(me, peerID, online))
		if peer.send(h.friendStateEvent(me, online)) == nil {
			sent++
		}
	}
	if sent > 0 {
		logrus.WithFields(logrus.Fields{"player": me, "online": online, "friends_told": sent}).
			Info("social: friend state pushed")
	}
}
