package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

// Server announcements in chat: how many players are online, and when a
// matchmaking queue starts, so players know when to queue to play together.
//
// They go to the GLOBAL channel, from a sender named "Server". A channel of
// their own is not possible with the stock client: UYMmogChat keeps ONE
// channel-name slot per room type (Global, language, match all/team, squad,
// custom room -- see chatJoinNotice) and creates only its Global and English
// rooms at startup, so a further "server" room has no tab to appear in, and
// joining one of an existing type would replace that type's channel.
//
// The sender is a fixed GUID that is no player's. The client names a chat
// line's sender from its user.profile cache (GetUsername) or asks user.whois;
// both are answered for this GUID with the name "Server" (serverChatProfile,
// and handleUserMethod's whois).
//
// DN_SERVER_CHAT=0 turns it off. DN_SERVER_CHAT_ONLINE_EVERY (default 10m)
// is the least time between two online counts; one is posted only when the
// count changed. DN_SERVER_CHAT_QUEUE_COOLDOWN (default 2m) is the least time
// between two "queue started" lines for the same mode and fleet.

const (
	serverChatGUID = "5e7e7e70-0000-4000-8000-000000000001"
	serverChatName = "Server"
)

func serverChatEnabled() bool { return os.Getenv("DN_SERVER_CHAT") != "0" }

func serverChatDuration(env string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(env)); err == nil && d >= 0 {
		return d
	}
	return def
}

// isServerChatID reports whether a GUID (any form) is the Server sender.
func isServerChatID(id string) bool {
	return protocol.NormalizePlayerPID(id) == protocol.NormalizePlayerPID(serverChatGUID)
}

// serverChatEntry is the Server sender as a presence/whois record.
func serverChatEntry() map[string]any {
	return map[string]any{
		"guid":              serverChatGUID,
		"pid":               serverChatGUID,
		"PID":               serverChatGUID,
		"peer_id":           "",
		"name":              serverChatName,
		"display_name":      serverChatName,
		"full_display_name": serverChatName,
		"number":            "0",
		"status":            "online",
		"message":           "",
		"online":            true,
	}
}

// serverChatProfile fills the client's name cache for the Server sender, the
// same push userProfileEvent sends for a player.
func serverChatProfile() map[string]any {
	return firmamentEvent("user.profile", map[string]any{
		"guid":              serverChatGUID,
		"profile":           serverChatGUID,
		"public_id":         serverChatGUID,
		"number":            "0",
		"display_name":      serverChatName,
		"full_display_name": serverChatName,
		"status_message":    "",
		"is_online":         true,
		"is_idle":           false,
		"is_away":           false,
		"is_private":        false,
		"is_admin":          true,
		"status":            1,
		"peer_id":           "",
	})
}

// announce posts one Server line to everyone in the Global channel.
func (h *socialHub) announce(text string) {
	if !serverChatEnabled() {
		return
	}
	channel := defaultChatChannels[0]
	profile := serverChatProfile()
	msg := chatMessageNotice("chat.channel.message", channel, serverChatEntry(), text)
	sent := 0
	for _, peer := range h.peersIn(channel) {
		_ = peer.send(profile)
		_ = peer.send(msg)
		sent++
	}
	logrus.WithField("recipients", sent).Info("server chat: " + text)
}

// --- online count ----------------------------------------------------------------

// serverChatOnlineLine is the online-count announcement.
func serverChatOnlineLine(online, inBattle, searching int) string {
	players := "players"
	if online == 1 {
		players = "player"
	}
	return fmt.Sprintf("%d %s online -- %d in battle, %d searching for a match.", online, players, inBattle, searching)
}

// serverChatCounts counts players online, in a running match, and queued.
func serverChatCounts() (online, inBattle, searching int) {
	ids := socialHubInstance.onlinePlayerIDs()
	online = len(ids)
	database := currentMmogPlayerStateDB()
	if database == nil {
		return online, 0, 0
	}
	_ = database.QueryRow(`SELECT COUNT(DISTINCT s.user_id) FROM match_slots s JOIN matches m ON m.id=s.match_id WHERE m.status='active'`).Scan(&inBattle)
	_ = database.QueryRow(`SELECT COUNT(DISTINCT user_id) FROM queue_entries WHERE status='waiting'`).Scan(&searching)
	return online, inBattle, searching
}

// startServerChatOnlineCount posts the online count every interval, when it
// changed since the last post.
func startServerChatOnlineCount() {
	every := serverChatDuration("DN_SERVER_CHAT_ONLINE_EVERY", 10*time.Minute)
	if !serverChatEnabled() || every == 0 {
		return
	}
	go func() {
		last := -1
		var lastAt time.Time
		for range time.Tick(time.Minute) {
			if time.Since(lastAt) < every {
				continue
			}
			online, inBattle, searching := serverChatCounts()
			if online == 0 || online == last {
				continue
			}
			socialHubInstance.announce(serverChatOnlineLine(online, inBattle, searching))
			last, lastAt = online, time.Now()
		}
	}()
}

// --- queue started ---------------------------------------------------------------

// serverChatModeNames are the player-facing names of the queueable modes.
var serverChatModeNames = map[string]string{
	"TDM":       "Team Deathmatch",
	"PodTDM":    "Team Deathmatch",
	"TurboTDM":  "Team Deathmatch",
	"TE":        "Team Elimination",
	"TER":       "Territory Control",
	"Territory": "Territory Control",
	"Onslaught": "Havoc",
}

var (
	serverChatQueueMu   sync.Mutex
	serverChatQueueLast = map[string]time.Time{}
)

// serverChatQueueLine is the "queue started" announcement, or "" for a mode
// nobody else can join (solo tutorial modes).
func serverChatQueueLine(gameMode string, fleetType int32, partySize int) string {
	mode, ok := serverChatModeNames[gameMode]
	if !ok {
		return ""
	}
	fleet := fleetTypeName(fleetType)
	who := "A player is"
	if partySize > 1 {
		who = fmt.Sprintf("A squad of %d is", partySize)
	}
	return fmt.Sprintf("%s searching for a %s %s match. Queue with your %s fleet now to play together!",
		who, fleet, mode, fleet)
}

// announceQueueStarted posts a "queue started" line when a player or squad
// just queued into an EMPTY queue for its mode and fleet -- the moment others
// can join them. Called after the queue entries are written.
func announceQueueStarted(gameMode string, fleetType int32, partySize int) {
	if !serverChatEnabled() {
		return
	}
	line := serverChatQueueLine(gameMode, fleetType, partySize)
	if line == "" {
		return
	}
	database := currentMmogPlayerStateDB()
	if database == nil {
		return
	}
	var waiting int
	if database.QueryRow(`SELECT COUNT(*) FROM queue_entries WHERE status='waiting' AND game_mode=? AND fleet_type=?`,
		gameMode, fleetType).Scan(&waiting) != nil || waiting > partySize {
		return // others were already waiting: the queue had started before
	}
	key := gameMode + "/" + strings.ToLower(fleetTypeName(fleetType))
	cooldown := serverChatDuration("DN_SERVER_CHAT_QUEUE_COOLDOWN", 2*time.Minute)
	serverChatQueueMu.Lock()
	if last, ok := serverChatQueueLast[key]; ok && time.Since(last) < cooldown {
		serverChatQueueMu.Unlock()
		return
	}
	serverChatQueueLast[key] = time.Now()
	serverChatQueueMu.Unlock()
	socialHubInstance.announce(line)
}
