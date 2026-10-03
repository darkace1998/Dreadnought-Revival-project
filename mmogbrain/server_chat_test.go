package main

import (
	"bufio"
	"testing"
	"time"
)

// tryReadPush reads one push, or reports none within wait.
func tryReadPush(r *bufio.Reader, wait time.Duration) bool {
	ch := make(chan bool, 1)
	go func() {
		_, err := r.ReadBytes('\n')
		ch <- err == nil
	}()
	select {
	case ok := <-ch:
		return ok
	case <-time.After(wait):
		return false
	}
}

// A Server line reaches everyone in Global, named "Server": the profile push
// that fills the client's name cache comes first, then the message from the
// Server GUID.
func TestServerAnnouncementReachesGlobal(t *testing.T) {
	hub := socialTestHub(t)
	_, read := socialTestPeer(t, hub, "player-a")
	go hub.announce("3 players online")

	method, data := readNotice(t, read)
	if method != "user.profile" || data["guid"] != serverChatGUID || data["display_name"] != serverChatName {
		t.Fatalf("first push %s %v, want the Server user.profile", method, data)
	}
	method, data = readNotice(t, read)
	if method != "chat.channel.message" || data["channel"] != defaultChatChannels[0] ||
		data["sender"] != serverChatGUID || data["text"] != "3 players online" {
		t.Errorf("second push %s %v, want the message in Global from the Server GUID", method, data)
	}
}

// user.whois for the Server GUID answers with its name, so a client that
// lost the profile can still name the line.
func TestWhoisNamesTheServer(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	hub := socialTestHub(t)
	res := handleUserMethod(socialRequest{method: "user.whois", hub: hub,
		params: map[string]any{"users": []any{serverChatGUID}}})
	users, _ := res["users"].([]any)
	if len(users) != 1 || users[0].(map[string]any)["display_name"] != serverChatName {
		t.Errorf("whois of the Server GUID: %v", res)
	}
}

// "Queue started" is posted when a queue for a mode and fleet goes from
// empty to waiting -- not for the second player, not again within the
// cooldown -- and never for solo modes.
func TestQueueStartedAnnouncement(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	hub := socialTestHub(t)
	saved := socialHubInstance
	socialHubInstance = hub
	t.Cleanup(func() { socialHubInstance = saved })
	serverChatQueueMu.Lock()
	serverChatQueueLast = map[string]time.Time{}
	serverChatQueueMu.Unlock()
	_, read := socialTestPeer(t, hub, "listener")

	queue := func(id, user, mode string, fleet int) {
		if _, err := database.Exec(`INSERT INTO queue_entries(id,user_id,game_mode,tier_min,tier_max,fleet_type,status) VALUES(?,?,?,1,5,?,'waiting')`,
			id, user, mode, fleet); err != nil {
			t.Fatal(err)
		}
	}

	queue("q1", "a", "TDM", 2)
	go announceQueueStarted("TDM", 2, 1)
	readNotice(t, read) // profile
	_, data := readNotice(t, read)
	want := "A player is searching for a Veteran Team Deathmatch match. Queue with your Veteran fleet now to play together!"
	if data["text"] != want {
		t.Errorf("announcement %q, want %q", data["text"], want)
	}

	queue("q2", "b", "TDM", 2) // joins a queue that already started
	announceQueueStarted("TDM", 2, 1)
	if tryReadPush(read, 200*time.Millisecond) {
		t.Error("announced a second player joining a started queue")
	}

	if _, err := database.Exec(`DELETE FROM queue_entries`); err != nil {
		t.Fatal(err)
	}
	queue("q3", "c", "TDM", 2) // empty again, but within the cooldown
	announceQueueStarted("TDM", 2, 1)
	if tryReadPush(read, 200*time.Millisecond) {
		t.Error("announced again within the cooldown")
	}

	queue("q4", "d", "BC", 1) // the solo bootcamp: nobody can join
	announceQueueStarted("BC", 1, 1)
	if tryReadPush(read, 200*time.Millisecond) {
		t.Error("announced a solo mode")
	}
	if got := serverChatQueueLine("TE", 3, 3); got != "A squad of 3 is searching for a Legendary Team Elimination match. Queue with your Legendary fleet now to play together!" {
		t.Errorf("squad line %q", got)
	}
}
