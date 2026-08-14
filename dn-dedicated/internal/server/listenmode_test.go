package server

import (
	"strings"
	"testing"

	"dn-dedicated/internal/gamedata"
)

func argvFor(t *testing.T) string {
	t.Helper()
	return strings.Join(BuildArgs(LaunchConfig{
		Map:      gamedata.Map{Path: "/Game/Maps/MP/Derelict/MP_Derelict_P"},
		GameMode: "BC",
		Port:     7777,
	}, "match-1"), " ")
}

// -server is the default: a battle server should not also be a player.
func TestServerSwitchIsTheDefault(t *testing.T) {
	t.Setenv("DN_LISTEN_SERVER", "")
	if got := argvFor(t); !strings.Contains(got, " -server") {
		t.Errorf("-server missing from the default argv: %s", got)
	}
}

// DN_LISTEN_SERVER=1 drops it, so the host keeps a LocalPlayers[0].
//
// dread-sdk's server mod needs one: ForceSpawnLocalPlayer, ForceStartMatch and
// InitDesyncFix all dereference
// GWorld->OwningGameInstance->LocalPlayers[0]->PlayerController, and its desync
// fix is a listen-mode concept -- "only players that are actively being
// rendered by the server are able to play" -- so there has to be a local player
// to give a view target to.
func TestListenModeDropsTheServerSwitch(t *testing.T) {
	t.Setenv("DN_LISTEN_SERVER", "1")
	got := argvFor(t)
	if strings.Contains(got, " -server") {
		t.Errorf("-server still present with DN_LISTEN_SERVER=1: %s", got)
	}
	// Everything else must survive, especially ?listen -- that is what starts
	// the net driver, and without it nothing can connect at all.
	for _, want := range []string{"?listen", "-port=7777", "-nullrhi", "-unattended", "-forcelogflush"} {
		if !strings.Contains(got, want) {
			t.Errorf("listen mode lost %q from the argv: %s", want, got)
		}
	}
}
