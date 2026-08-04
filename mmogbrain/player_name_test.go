package main

import "testing"

// Every account on this server was called "Local". player_state seeds
// display_name to that constant and nothing ever wrote over it, so the client
// had no name to show -- reported as "the user name is not displayed in the
// game". The name is known: it arrives in the JWT's "username" claim at gateway
// login, which is also where the gateway's own response already echoes it.
func TestLoginNameReplacesTheLocalPlaceholder(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"

	if got := mmogPlayerStateForPID(pid).displayName; got != "Local" {
		t.Fatalf("precondition: a fresh row is named %q, expected the %q placeholder", got, "Local")
	}

	rememberPlayerDisplayName(pid, "123")
	if got := mmogPlayerStateForPID(pid).displayName; got != "123" {
		t.Errorf("display name = %q, want %q", got, "123")
	}
}

// A name the player chose outranks their login name; the captain customisation
// screen writes the same column.
func TestAChosenNameIsNotOverwrittenByTheLoginName(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET display_name='Bards' WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}

	rememberPlayerDisplayName(pid, "123")
	if got := mmogPlayerStateForPID(pid).displayName; got != "Bards" {
		t.Errorf("display name = %q, want %q -- a chosen name must survive the next login", got, "Bards")
	}
}

func TestRememberPlayerDisplayNameIgnoresJunk(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	for _, name := range []string{"", "   "} {
		rememberPlayerDisplayName(pid, name)
		if got := mmogPlayerStateForPID(pid).displayName; got != "Local" {
			t.Errorf("display name = %q after storing %q; want the placeholder untouched", got, name)
		}
	}
}

// The self profile the client receives on the Firmament socket sent name and
// nickname as "" for everyone.
func TestFirmamentSelfProfileCarriesTheName(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	rememberPlayerDisplayName(pid, "123")

	profile := firmamentSelfProfile(pid, "peer-1")
	for _, key := range []string{"name", "nickname"} {
		if got, _ := profile[key].(string); got != "123" {
			t.Errorf("profile[%q] = %q, want %q", key, got, "123")
		}
	}
}
