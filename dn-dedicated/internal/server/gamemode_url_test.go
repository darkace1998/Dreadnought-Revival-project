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

// This build writes no log file of its own, so the captured stdout stream is
// the only record of a battle server that exists -- and UE4.13's stdout device
// caps itself at Display without this switch. Losing it means losing every
// Log-verbosity line, which is where the loadout and spawn failures explain
// themselves.
func TestBuildArgsRaisesStdoutVerbosity(t *testing.T) {
	args := BuildArgs(LaunchConfig{
		Map:      gamedata.Map{Name: "Highlands", Path: "/Game/Maps/MP/Highlands/MP_Highlands_P"},
		GameMode: "TM",
		Port:     7777,
	}, "match-1")

	if !containsArg(args, "-AllowStdOutLogVerbosity") {
		t.Error("-AllowStdOutLogVerbosity missing; stdout stays capped at Display")
	}
	// -FullStdOutLogOutput would give All, but it does not exist in 4.13 and
	// the string is absent from the binary. Passing it would be a silent no-op
	// that looks like it works.
	if containsArg(args, "-FullStdOutLogOutput") {
		t.Error("-FullStdOutLogOutput is not a switch this build has")
	}
}

func TestBuildArgsPassesEngineLogCmdsOnlyWhenAsked(t *testing.T) {
	base := LaunchConfig{
		Map:      gamedata.Map{Name: "Highlands", Path: "/Game/Maps/MP/Highlands/MP_Highlands_P"},
		GameMode: "TM",
		Port:     7777,
	}
	for _, arg := range BuildArgs(base, "m") {
		if strings.HasPrefix(arg, "-LogCmds=") {
			t.Fatalf("unrequested %q; verbose engine logging is opt-in", arg)
		}
	}

	base.EngineLogCmds = "global verbose, LogYComVOComponent log"
	if !containsArg(BuildArgs(base, "m"), "-LogCmds=global verbose, LogYComVOComponent log") {
		t.Error("-LogCmds was not passed through")
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
