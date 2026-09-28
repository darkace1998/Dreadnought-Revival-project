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
	if len(users) != 1 || users[0].(map[string]any)["display_name"] != "Some_Tester" {
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
