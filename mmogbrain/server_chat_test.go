package main

import (
	"bufio"
	"encoding/json"
	"testing"
	"time"
)

// pushLines reads every push line of a peer into a channel, so a check for
// "nothing arrived" never leaves a half-finished read behind.
func pushLines(r *bufio.Reader) <-chan map[string]any {
	ch := make(chan map[string]any, 16)
	go func() {
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				close(ch)
				return
			}
			var msg map[string]any
			if json.Unmarshal(line, &msg) == nil {
				ch <- msg
			}
		}
	}()
	return ch
}

func nextPush(t *testing.T, ch <-chan map[string]any) (string, map[string]any) {
	t.Helper()
	select {
	case msg := <-ch:
		typ, _ := msg["type"].(string)
		data, _ := msg["data"].(map[string]any)
		return typ, data
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a push")
		return "", nil
	}
}

func noPush(ch <-chan map[string]any, wait time.Duration) bool {
	select {
	case <-ch:
		return false
	case <-time.After(wait):
		return true
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
// empty to waiting and is STILL waiting after the delay -- not when the
// matchmaker filled it at once, not for the second player, not again within
// the cooldown, never for solo modes.
func TestQueueStartedAnnouncement(t *testing.T) {
	t.Setenv("DN_SERVER_CHAT_QUEUE_DELAY", "50ms")
	database := useTempMmogPlayerStateDB(t)
	hub := socialTestHub(t)
	saved := socialHubInstance
	socialHubInstance = hub
	t.Cleanup(func() { socialHubInstance = saved })
	serverChatQueueMu.Lock()
	serverChatQueueLast = map[string]time.Time{}
	serverChatQueueMu.Unlock()
	_, read := socialTestPeer(t, hub, "listener")
	pushes := pushLines(read)

	queue := func(id, user, mode string, fleet int) {
		if _, err := database.Exec(`INSERT INTO queue_entries(id,user_id,game_mode,tier_min,tier_max,fleet_type,status) VALUES(?,?,?,1,5,?,'waiting')`,
			id, user, mode, fleet); err != nil {
			t.Fatal(err)
		}
	}
	clear := func() {
		if _, err := database.Exec(`DELETE FROM queue_entries`); err != nil {
			t.Fatal(err)
		}
	}

	// Filled at once (the match started before the delay): not announced.
	queue("q0", "z", "TDM", 3)
	announceQueueStarted("TDM", 3, 1)
	clear()
	if !noPush(pushes, 200*time.Millisecond) {
		t.Error("announced a queue the matchmaker had already filled")
	}

	queue("q1", "a", "TDM", 2)
	announceQueueStarted("TDM", 2, 1)
	nextPush(t, pushes) // profile
	_, data := nextPush(t, pushes)
	want := "A player is searching for a Veteran Team Deathmatch match. Queue with your Veteran fleet now to play together!"
	if data["text"] != want {
		t.Errorf("announcement %q, want %q", data["text"], want)
	}

	queue("q2", "b", "TDM", 2) // joins a queue that already started
	announceQueueStarted("TDM", 2, 1)
	if !noPush(pushes, 200*time.Millisecond) {
		t.Error("announced a second player joining a started queue")
	}

	if got := serverChatOpenQueues(); got != "Veteran Team Deathmatch (2 waiting)" {
		t.Errorf("open queues %q", got)
	}
	if got := serverChatOnlineLine(5, 1, 2, serverChatOpenQueues()); got != "5 players online -- 1 in battle, 2 searching for a match. Open queues: Veteran Team Deathmatch (2 waiting)." {
		t.Errorf("online line %q", got)
	}

	clear()
	queue("q3", "c", "TDM", 2) // empty again, but within the cooldown
	announceQueueStarted("TDM", 2, 1)
	if !noPush(pushes, 200*time.Millisecond) {
		t.Error("announced again within the cooldown")
	}

	queue("q4", "d", "BC", 1) // the solo bootcamp: nobody can join
	announceQueueStarted("BC", 1, 1)
	if !noPush(pushes, 200*time.Millisecond) {
		t.Error("announced a solo mode")
	}
	if got := serverChatQueueLine("TE", 3, 3); got != "A squad of 3 is searching for a Legendary Team Elimination match. Queue with your Legendary fleet now to play together!" {
		t.Errorf("squad line %q", got)
	}
}
