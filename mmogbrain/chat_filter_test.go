package main

import (
	"testing"
	"time"
)

func TestFilterChatText(t *testing.T) {
	for _, c := range []struct {
		in, want string
		hits     int
	}{
		{"gg wp", "gg wp", 0},
		{"what the fuck", "what the ####", 1},
		{"FUCKING hell", "####### hell", 1},
		{"sh1t happens", "#### happens", 1},
		{"you a$$hole!", "you ########", 1},
		// Whole-word entries do not hit innocent words.
		{"first class assassin, as planned", "first class assassin, as planned", 0},
		{"hello there, entering orbit", "hello there, entering orbit", 0},
		{"du hurensohn", "du #########", 1},
		{"tering", "######", 1},
	} {
		got, hits := filterChatText(c.in)
		if got != c.want || hits != c.hits {
			t.Errorf("%q -> %q (%d hits), want %q (%d)", c.in, got, hits, c.want, c.hits)
		}
		if len([]rune(got)) != len([]rune(c.in)) {
			t.Errorf("%q changed length", c.in)
		}
	}
}

// Every chat line carries the verdict the client reads (data.filter.sift);
// the sender's own copy of a filtered line also the game's warning event,
// the other copies not.
func TestChatLinesCarryTheFilterVerdict(t *testing.T) {
	hub := socialTestHub(t)
	a, aRead := socialTestPeer(t, hub, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	_, bRead := socialTestPeer(t, hub, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	aCh, bCh := pushLines(aRead), pushLines(bRead)
	handleChatMethod(socialRequest{method: "chat.channel.message", peer: a, hub: hub,
		params: map[string]any{"channel": "dreadnought.global", "text": "fuck this"}})
	sift := func(ch <-chan map[string]any, who string) map[string]any {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			select {
			case msg := <-ch:
				if msg["type"] != "chat.channel.message" {
					continue
				}
				data, _ := msg["data"].(map[string]any)
				filter, _ := data["filter"].(map[string]any)
				s, _ := filter["sift"].(map[string]any)
				if s == nil || data["text"] != "fuck this" || s["hashed"] != "#### this" {
					t.Fatalf("%s got %v", who, data)
				}
				return s
			case <-deadline:
				t.Fatalf("%s got no message", who)
				return nil
			}
		}
	}
	other := sift(bCh, "other player")
	if other["events"] != nil {
		t.Errorf("the other player got the sender's warning: %v", other["events"])
	}
	own := sift(aCh, "sender")
	events, _ := own["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("sender events %v, want the warning", own["events"])
	}
	ev := events[0].(map[string]any)
	if ev["trust_level"] != "UNTRUSTED" || ev["message"] != chatProfanityWarning {
		t.Errorf("warning event %v", ev)
	}

	// A clean line: hashed equals the text, no events.
	handleChatMethod(socialRequest{method: "chat.channel.message", peer: a, hub: hub,
		params: map[string]any{"channel": "dreadnought.global", "text": "gg"}})
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-aCh:
			if msg["type"] != "chat.channel.message" {
				continue
			}
			data := msg["data"].(map[string]any)
			s := data["filter"].(map[string]any)["sift"].(map[string]any)
			if s["hashed"] != "gg" || s["events"] != nil {
				t.Errorf("clean line verdict %v", s)
			}
			return
		case <-deadline:
			t.Fatal("no clean line")
		}
	}
}
