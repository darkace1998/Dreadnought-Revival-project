package server

import (
	"strings"
	"testing"

	"dn-dedicated/internal/gamedata"
)

// UE4 does not read "-GameMode=X". It picks the game mode from the URL's "game"
// option and otherwise falls back to the map's World Settings default, which is
// how a match requested as BC ended up running Derelict's GameMode_Turbo_TDM_BP.
func TestBuildArgsPutsTheGameModeInTheURL(t *testing.T) {
	args := BuildArgs(LaunchConfig{
		Map:      gamedata.Map{Name: "Derelict", Path: "/Game/Maps/MP/Derelict/MP_Derelict_P"},
		GameMode: "BC",
		Port:     7777,
	}, "match-1")

	if !strings.Contains(args[0], "?game=BC") {
		t.Fatalf("map URL %q carries no ?game= option, so the map's default mode wins", args[0])
	}
	if !strings.Contains(args[0], "?listen") {
		t.Error("map URL lost ?listen")
	}
}

// An operator passing their own game= must not end up with two of them.
func TestBuildArgsExplicitGameOptionWins(t *testing.T) {
	args := BuildArgs(LaunchConfig{
		Map:        gamedata.Map{Name: "Derelict", Path: "/Game/Maps/MP/Derelict/MP_Derelict_P"},
		GameMode:   "BC",
		URLOptions: []string{"game=TurboTDM"},
		Port:       7777,
	}, "match-1")

	if strings.Count(args[0], "game=") != 1 {
		t.Fatalf("map URL %q has more than one game= option", args[0])
	}
	if !strings.Contains(args[0], "game=TurboTDM") {
		t.Error("the explicit URLOptions game= should win over GameMode")
	}
}

// No mode configured must not produce a dangling "?game=".
func TestBuildArgsOmitsEmptyGameMode(t *testing.T) {
	args := BuildArgs(LaunchConfig{
		Map:  gamedata.Map{Name: "Derelict", Path: "/Game/Maps/MP/Derelict/MP_Derelict_P"},
		Port: 7777,
	}, "match-1")

	if strings.Contains(args[0], "game=") {
		t.Errorf("map URL %q has a game= option with no mode to put in it", args[0])
	}
}
