package main

import (
	"strings"
	"testing"
)

// The client sends user.search with the player's own Steam persona seventeen
// times during a login. It used to fall through to the generic
// {"status":"success"} -- the shape of "your request was fine", not "here are
// the users" -- so the client had no profile for itself and the player's name
// was blank in game.
func TestUserSearchReturnsTheRequestingPlayer(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	hub := socialTestHub(t)
	peer, _ := socialTestPeer(t, hub, "650dd79476a1484b8adcd01ac2f17354")

	result, handled := handleSocialMethod(socialRequest{
		method: "user.search",
		params: map[string]any{"terms": "DARKACE", "limit": float64(100)},
		peer:   peer, hub: hub,
	})
	if !handled {
		t.Fatal("user.search was not handled; it falls through to the generic result")
	}
	users, _ := result["users"].([]any)
	if len(users) == 0 {
		t.Fatal("user.search returned no users for the searcher's own persona")
	}
	entry, _ := users[0].(map[string]any)
	if name, _ := entry["name"].(string); name != "DARKACE" {
		t.Errorf("first result name = %q, want %q", name, "DARKACE")
	}
}

// The persona is the only channel that carries a name, so it is stored -- but
// only over a placeholder or a bare login name, never over one the player set.
func TestSteamPersonaDoesNotOverwriteAChosenName(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET display_name='Bards' WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	hub := socialTestHub(t)
	peer, _ := socialTestPeer(t, hub, pid)

	handleSocialMethod(socialRequest{
		method: "user.search",
		params: map[string]any{"terms": "DARKACE"},
		peer:   peer, hub: hub,
	})
	if got := mmogPlayerStateForPID(pid).displayName; got != "Bards" {
		t.Errorf("display name = %q, want %q", got, "Bards")
	}
}

// A claim from a client is still a claim. Junk must not become somebody's name.
func TestSteamPersonaIsSanityChecked(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	hub := socialTestHub(t)
	peer, _ := socialTestPeer(t, hub, pid)

	for _, terms := range []string{strings.Repeat("A", 40), "bad\r\nname", ""} {
		handleSocialMethod(socialRequest{
			method: "user.search",
			params: map[string]any{"terms": terms},
			peer:   peer, hub: hub,
		})
		if got := mmogPlayerStateForPID(pid).displayName; got != "Local" {
			t.Errorf("display name = %q after searching %q; want the placeholder untouched", got, terms)
		}
	}
}

// Every presence entry, chat user and friend listing carried name:"" because
// nothing ever assigned peer.name.
func TestSocialPeerCarriesTheDisplayName(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	rememberPlayerDisplayName(pid, "123")

	peer := newSocialPeer(pid, "peer-1", nil)
	t.Cleanup(peer.close)
	if peer.name != "123" {
		t.Errorf("peer name = %q, want %q", peer.name, "123")
	}
}
