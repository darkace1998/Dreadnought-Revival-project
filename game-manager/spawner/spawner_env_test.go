package spawner

import (
	"testing"
)

// envValue returns the LAST value for key, which is what exec uses when a
// slice carries duplicates -- battleServerEnv appends on top of os.Environ().
func envValue(env []string, key string) string {
	value := ""
	prefix := key + "="
	for _, entry := range env {
		if len(entry) > len(prefix) && entry[:len(prefix)] == prefix {
			value = entry[len(prefix):]
		} else if entry == prefix {
			value = ""
		}
	}
	return value
}

// The battle server links CEF, which aborts the process with an "Invalid window
// handle" fatal (surfacing as a libcef.dll call stack) when it has no display.
// A spawn inheriting an environment without DISPLAY therefore has to be given
// one, or every match dies seconds after the client is told to connect.
func TestBattleServerEnvSuppliesDisplayWhenUnset(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("GAME_DISPLAY", "")

	if got := envValue(battleServerEnv(t.TempDir()), "DISPLAY"); got != ":99" {
		t.Fatalf("DISPLAY = %q, want the :99 fallback", got)
	}
}

func TestBattleServerEnvKeepsInheritedDisplay(t *testing.T) {
	t.Setenv("DISPLAY", ":7")
	t.Setenv("GAME_DISPLAY", "")

	if got := envValue(battleServerEnv(t.TempDir()), "DISPLAY"); got != ":7" {
		t.Fatalf("DISPLAY = %q, want the inherited :7", got)
	}
}

func TestBattleServerEnvGameDisplayOverridesInherited(t *testing.T) {
	t.Setenv("DISPLAY", ":7")
	t.Setenv("GAME_DISPLAY", ":42")

	if got := envValue(battleServerEnv(t.TempDir()), "DISPLAY"); got != ":42" {
		t.Fatalf("DISPLAY = %q, want the GAME_DISPLAY override :42", got)
	}
}

// UE4 does not read "-GameMode=X"; it takes the mode from the URL's "game"
// option and otherwise falls back to the map's World Settings default. That is
// how a match requested as BC came up running Derelict's GameMode_Turbo_TDM_BP.
// DefaultGame.ini registers GameModeClassAliases for the matchmaker's names, so
// the short name is all the URL needs.
func TestMapURLCarriesTheGameModeOption(t *testing.T) {
	url := battleServerMapURL("/Game/Maps/MP/Derelict/MP_Derelict_P", "Derelict", "BC")

	if url != "/Game/Maps/MP/Derelict/MP_Derelict_P?listen?game=BC" {
		t.Fatalf("map URL = %q, want the path with ?listen and ?game=BC", url)
	}
}

func TestMapURLFallsBackToTheBareMapName(t *testing.T) {
	if url := battleServerMapURL("", "Derelict", "TDM"); url != "Derelict?listen?game=TDM" {
		t.Fatalf("map URL = %q, want the bare name with the options", url)
	}
}

// No mode must not produce a dangling "?game=".
func TestMapURLOmitsAnEmptyGameMode(t *testing.T) {
	if url := battleServerMapURL("/Game/Maps/MP/Derelict/MP_Derelict_P", "Derelict", ""); url != "/Game/Maps/MP/Derelict/MP_Derelict_P?listen" {
		t.Fatalf("map URL = %q, want no game= option at all", url)
	}
}
