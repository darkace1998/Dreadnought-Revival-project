// Command dn-dedicated runs headless Dreadnought battle servers locally.
//
// Dreadnought never shipped a dedicated-server build. The battle server is the
// ordinary client executable launched as a headless listen server, which is the
// method the Dreadnought-Revival-project's game-manager established by
// experiment; this tool uses the identical argv. See internal/server for the
// details and for what the non-working variants got wrong.
//
// Two ways to run it:
//
//	dn-dedicated run      one server in the foreground, until Ctrl+C  (headless)
//	dn-dedicated serve    an HTTP control plane compatible with game-manager
//
// Plus client commands (start/stop/list) that drive a running `serve`.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const usage = `dn-dedicated - local headless Dreadnought dedicated server

USAGE
  dn-dedicated <command> [flags]

SERVER COMMANDS
  run       Launch one battle server in the foreground and block until it exits.
            This is the headless dedicated-server mode: no window, no GPU, no
            Steam client. Ctrl+C stops it.
  serve     Run the HTTP control plane (game-manager compatible) so matches can
            be requested over the API, e.g. by mmogbrain's matchmaker.

CLIENT COMMANDS (talk to a running "serve")
  start     Request a new battle server.
  stop      Stop a battle server by instance id.
  list      List running battle servers.
  status    Show control-plane health.

INFORMATIONAL
  maps      List playable maps and their package paths.
  modes     List game modes and aliases.
  args      Print the exact argv that "run" would use, without launching.
  help      Show this help. "dn-dedicated <command> -h" for a command's flags.

Run "dn-dedicated maps" first if you are unsure what to pass to --map.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(os.Args[2:])
	case "serve":
		err = cmdServe(os.Args[2:])
	case "start":
		err = cmdStart(os.Args[2:])
	case "stop":
		err = cmdStop(os.Args[2:])
	case "list":
		err = cmdList(os.Args[2:])
	case "status":
		err = cmdStatus(os.Args[2:])
	case "maps":
		err = cmdMaps(os.Args[2:])
	case "modes":
		err = cmdModes(os.Args[2:])
	case "args":
		err = cmdArgs(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "dn-dedicated: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "dn-dedicated: %v\n", err)
		os.Exit(1)
	}
}

// getenv returns the environment value for key, or fallback when unset/empty.
// Matches the getenv helper every Revival service uses.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// internalKeyFromEnv mirrors game-manager's requireInternalKey precedence:
// INTERNAL_API_KEY, then ADMIN_KEY. Unlike game-manager this does not abort when
// unset, because the local single-server "run" mode needs no key at all -- it
// talks to nothing. Only "serve" insists on one.
func internalKeyFromEnv() string {
	if k := os.Getenv("INTERNAL_API_KEY"); k != "" {
		return k
	}
	return os.Getenv("ADMIN_KEY")
}

// placeholderKeys are the values game-manager explicitly refuses to start with.
var placeholderKeys = map[string]bool{
	"":                   true,
	"changeme-admin-key": true,
	"changeme":           true,
	"change-me":          true,
}

// findGameBinary resolves the game executable.
//
// Precedence: the explicit flag, then GAME_BINARY, then a search of locations
// relative to the working directory. The relative search exists because this
// tool is expected to sit beside the game in a modding tree, and typing the
// 70-character path on every invocation is how operators end up scripting
// around a CLI instead of using it.
func findGameBinary(explicit string) (string, error) {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", fmt.Errorf("resolve --game-binary: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("game binary not found at %s", abs)
		}
		return abs, nil
	}
	if env := os.Getenv("GAME_BINARY"); env != "" {
		abs, err := filepath.Abs(env)
		if err != nil {
			return "", fmt.Errorf("resolve GAME_BINARY: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("GAME_BINARY points at a missing file: %s", abs)
		}
		return abs, nil
	}

	const leaf = "DreadGame/DreadGame/Binaries/Win64/" + binaryName
	candidates := []string{
		leaf,
		"Dreadnought/" + leaf,
		"../Dreadnought/" + leaf,
		"../../Dreadnought/" + leaf,
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(filepath.FromSlash(c))
		if err != nil {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			return abs, nil
		}
	}
	return "", fmt.Errorf(
		"could not find %s\n"+
			"  Pass --game-binary <path>, or set GAME_BINARY.\n"+
			"  Searched relative to %s:\n    %s",
		binaryName, mustGetwd(), strings.Join(candidates, "\n    "))
}

const binaryName = "DreadGame-Win64-Shipping.exe"

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "(unknown)"
	}
	return wd
}

// defaultLogDir returns where engine logs go: a "logs" directory beside the
// dn-dedicated executable.
//
// It deliberately does NOT default to the engine's own
// %LOCALAPPDATA%\DreadGame\Saved\Logs. Every process using this install writes
// DreadGame.log there, so a battle server started while the operator's game
// client is running interleaves with it and each rotates the other's log away.
// Keeping battle-server logs in the project tree makes them separable and
// disposable.
func defaultLogDir() string {
	if v := os.Getenv("DN_LOG_DIR"); v != "" {
		return v
	}
	exe, err := os.Executable()
	if err != nil {
		return filepath.FromSlash("./logs")
	}
	return filepath.Join(filepath.Dir(exe), "logs")
}

// defaultWineExe returns the Wine executable to use. On Windows the game runs
// directly, so this is "none"; elsewhere it honours WINE_EXE and defaults to
// "wine", matching game-manager.
func defaultWineExe() string {
	if runtime.GOOS == "windows" {
		return getenv("WINE_EXE", "none")
	}
	return getenv("WINE_EXE", "wine")
}

// stringList collects a repeatable flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, " ") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
