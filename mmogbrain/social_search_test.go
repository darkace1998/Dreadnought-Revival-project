package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

type bufWriter struct{ bytes.Buffer }

// user.search: the client reads root.data.users[].guid/display_name
// (0x142A3AB80), and offline players are found by a loose name match.
func TestUserSearchReplyShapeAndOfflineMatch(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "0123456789abcdef0123456789abcdef"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET display_name='Some_Tester' WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	hub := socialHubInstance
	users := hub.searchUsers("some tester", "ffffffffffffffffffffffffffffffff")
	if len(users) != 1 {
		t.Fatalf("found %d users, want the offline Some_Tester", len(users))
	}

	var w bufWriter
	result := socialOK(map[string]any{firmamentRootData: map[string]any{"users": users}})
	if err := writeFirmamentResult(&w, "req-1", result); err != nil {
		t.Fatal(err)
	}
	var msg struct {
		Data struct {
			Users []map[string]any `json:"users"`
		} `json:"data"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(w.Bytes(), &msg); err != nil {
		t.Fatal(err)
	}
	if len(msg.Data.Users) != 1 || msg.Data.Users[0]["guid"] != dashedPlayerGUID(pid) ||
		msg.Data.Users[0]["display_name"] != "Some_Tester" {
		t.Fatalf("root data.users = %v", msg.Data.Users)
	}
	if _, leaked := msg.Result[firmamentRootData]; leaked {
		t.Fatalf("%s leaked into result", firmamentRootData)
	}
}

// user.whois: a known id comes back in root data.users with its name; an
// unknown one in users_not_found (a reply without users made the client ask
// again every ~60 ms forever).
func TestUserWhoisResolvesNames(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "0123456789abcdef0123456789abcdef"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET display_name='Some_Tester' WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	res := handleUserMethod(socialRequest{method: "user.whois", hub: socialHubInstance,
		params: map[string]any{"users": []any{dashedPlayerGUID(pid), "ffffffff-ffff-ffff-ffff-ffffffffffff"}}})
	data := res[firmamentRootData].(map[string]any)
	users := data["users"].([]any)
	// An unknown id still gets a record carrying its own guid: without one the
	// client re-asks for it ~15 times a second, forever (2026-09-28).
	if len(users) != 2 || users[0].(map[string]any)["display_name"] != "Some_Tester" ||
		users[1].(map[string]any)["guid"] != "ffffffff-ffff-ffff-ffff-ffffffffffff" {
		t.Fatalf("users = %v", users)
	}
	if nf := data["users_not_found"].([]any); len(nf) != 1 {
		t.Fatalf("users_not_found = %v", nf)
	}
}

// Every user record carries a stable 4-digit number (the client showed 0).
func TestPlayerNumberIsStableAndFourDigits(t *testing.T) {
	const pid = "0123456789abcdef0123456789abcdef"
	n := playerNumber(pid)
	if len(n) != 4 || n != playerNumber(dashedPlayerGUID(pid)) {
		t.Fatalf("playerNumber = %q (dashed form %q)", n, playerNumber(dashedPlayerGUID(pid)))
	}
	if socialHubInstance.presenceEntry(pid)["number"] != n {
		t.Fatalf("presence entry number = %v", socialHubInstance.presenceEntry(pid)["number"])
	}
}

// A whois reply is routed by its root "type"; without one the client never
// delivered it and re-asked forever (2026-09-28).
func TestWhoisReplyCarriesRootType(t *testing.T) {
	var buf bytes.Buffer
	res := handleUserMethod(socialRequest{method: "user.whois", hub: socialHubInstance,
		params: map[string]any{"users": []any{"ffffffff-ffff-ffff-ffff-ffffffffffff"}}})
	if err := writeFirmamentResult(&buf, "req-1", res); err != nil {
		t.Fatal(err)
	}
	var msg map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &msg); err != nil {
		t.Fatal(err)
	}
	if msg["type"] != "user.whois" {
		t.Errorf("reply type = %v, want user.whois", msg["type"])
	}
	data, _ := msg["data"].(map[string]any)
	if users, _ := data["users"].([]any); len(users) != 1 {
		t.Errorf("data.users = %v", data["users"])
	}
	if result, _ := msg["result"].(map[string]any); result[firmamentRootType] != nil || result[firmamentRootData] != nil {
		t.Errorf("internal keys leaked into result: %v", result)
	}
}

// The add result carries data.notice.target, the target's GUID string.
func TestFriendAddReplyCarriesTarget(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const a = "0123456789abcdef0123456789abcdef"
	const b = "fedcba9876543210fedcba9876543210"
	for _, pid := range []string{a, b} {
		if err := seedMmogPlayerState(database, pid); err != nil {
			t.Fatal(err)
		}
	}
	peer := newSocialPeer(a, "peer-a", nil)
	res := handlePresenceSocialMethod(socialRequest{method: "presence.friends.add", hub: socialHubInstance, peer: peer,
		params: map[string]any{"user": dashedPlayerGUID(b)}})
	var buf bytes.Buffer
	if err := writeFirmamentResult(&buf, "r", res); err != nil {
		t.Fatal(err)
	}
	var msg map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &msg); err != nil {
		t.Fatal(err)
	}
	data, _ := msg["data"].(map[string]any)
	notice, _ := data["notice"].(map[string]any)
	if notice["target"] != dashedPlayerGUID(b) {
		t.Fatalf("data.notice.target = %v (reply %s)", notice["target"], buf.String())
	}
}
