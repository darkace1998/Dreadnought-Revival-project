//go:build windows

package main

// The launcher's actions, shared by both front ends: the WebView2 window
// (desktop_windows.go binds these as JS functions) and the browser version
// (browser_windows.go serves the SAME page and forwards the same calls over a
// loopback HTTP API). One page and one set of actions, so the two cannot
// drift apart -- the browser version used to be sign-in only, with no game
// folder, options or certificate screen.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

type launcherAPI struct {
	exeDir string
	cfg    Config
	// owner is the window that owns native dialogs (folder picker); 0 in the
	// browser version.
	owner func() uintptr

	mu       sync.Mutex
	token    string
	username string
}

func newLauncherAPI(exeDir string, cfg Config, owner func() uintptr) *launcherAPI {
	a := &launcherAPI{exeDir: exeDir, cfg: cfg, owner: owner}
	if creds, ok := loadCredentials(); ok && !signOutRequested() && !launcherTokenExpired(creds.Token) {
		a.token, a.username = creds.Token, creds.Username
	}
	return a
}

func (a *launcherAPI) Init() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	server := a.cfg.Server
	if server == "" {
		server = a.cfg.GatewayIP
	}
	settings := loadSettings()
	out := map[string]any{"signedIn": a.token != "", "username": a.username, "server": server,
		"game": gameInfo(a.exeDir, a.cfg), "logWindow": settings.LogWindow, "verboseLog": settings.VerboseLog}
	if pendingCA != nil && !caTrusted(pendingCA) {
		name, fp := caSummary(pendingCA)
		out["cert"] = map[string]any{"name": name, "fingerprint": fp}
	}
	return out
}

// PickGame shows Windows' folder picker. The choice is kept per Windows user
// (settings.json).
func (a *launcherAPI) PickGame() map[string]any {
	var owner uintptr
	if a.owner != nil {
		owner = a.owner()
	}
	dir, ok := pickFolder(owner, "Select the folder where Dreadnought is installed")
	if !ok {
		return map[string]any{"ok": false}
	}
	if gameBinaryIn(dir) == "" {
		return map[string]any{"ok": false, "error": "Dreadnought was not found in " + dir +
			". Pick the folder that contains the DreadGame folder."}
	}
	return a.setGameDir(dir)
}

// SetGameDir takes a typed path (the browser version's text field).
func (a *launcherAPI) SetGameDir(dir string) map[string]any {
	dir = strings.Trim(strings.TrimSpace(dir), `"`)
	if gameBinaryIn(dir) == "" {
		return map[string]any{"ok": false, "error": "Dreadnought was not found in " + dir +
			". Enter the folder that contains the DreadGame folder."}
	}
	return a.setGameDir(dir)
}

func (a *launcherAPI) setGameDir(dir string) map[string]any {
	settings := loadSettings()
	settings.GameDir = dir
	if err := saveSettings(settings); err != nil {
		return map[string]any{"ok": false, "error": "Could not save the setting: " + err.Error()}
	}
	return map[string]any{"ok": true, "game": gameInfo(a.exeDir, a.cfg)}
}

// AutoGame goes back to automatic detection: forget the chosen folder.
func (a *launcherAPI) AutoGame() map[string]any {
	settings := loadSettings()
	settings.GameDir = ""
	if err := saveSettings(settings); err != nil {
		return map[string]any{"ok": false, "error": "Could not save the setting: " + err.Error()}
	}
	return map[string]any{"ok": true, "game": gameInfo(a.exeDir, a.cfg)}
}

// SetOption saves a toggle, per Windows user.
func (a *launcherAPI) SetOption(name string, on bool) bool {
	settings := loadSettings()
	switch name {
	case "logWindow":
		settings.LogWindow = on
	case "verboseLog":
		settings.VerboseLog = on
	default:
		return false
	}
	return saveSettings(settings) == nil
}

func (a *launcherAPI) InstallCert() map[string]any {
	if pendingCA == nil || caTrusted(pendingCA) {
		return map[string]any{"ok": true}
	}
	if err := installCA(pendingCA); err != nil || !caTrusted(pendingCA) {
		return map[string]any{"ok": false, "error": "The certificate was not installed. " +
			"Windows asks you to confirm it -- choose Yes to continue."}
	}
	return map[string]any{"ok": true}
}

func (a *launcherAPI) Submit(mode, username, identifier, password string) map[string]any {
	identifier, username = strings.TrimSpace(identifier), strings.TrimSpace(username)
	fail := func(msg string) map[string]any { return map[string]any{"ok": false, "error": msg} }
	if identifier == "" || password == "" {
		return fail("Fill in every field.")
	}
	if mode == "register" {
		if username == "" {
			return fail("Pick a callsign.")
		}
		if len(password) < 6 {
			return fail("Use at least 6 characters for the password.")
		}
		if err := registerAccount(a.cfg.AuthURL, username, identifier, password); err != nil {
			return fail(capitalise(err.Error()))
		}
	}
	creds, err := loginAccount(a.cfg.AuthURL, identifier, password)
	if err != nil {
		return fail(capitalise(err.Error()))
	}
	if err := saveCredentials(creds); err != nil {
		fmt.Printf("[!] Could not remember this sign-in (%v)\n", err)
	}
	a.mu.Lock()
	a.token, a.username = creds.Token, creds.Username
	a.mu.Unlock()
	return map[string]any{"ok": true, "username": creds.Username}
}

func (a *launcherAPI) SignOut() {
	clearCredentials()
	a.mu.Lock()
	a.token, a.username = "", ""
	a.mu.Unlock()
}

func (a *launcherAPI) News() map[string]any {
	tiles, err := fetchNews()
	if err != nil {
		return map[string]any{"online": false, "error": err.Error()}
	}
	return map[string]any{"online": true, "tiles": tiles}
}

// Play starts the game. ok means the game was started and the front end
// should close.
func (a *launcherAPI) Play() map[string]any {
	a.mu.Lock()
	token := a.token
	a.mu.Unlock()
	if pendingCA != nil && !caTrusted(pendingCA) {
		return map[string]any{"ok": false, "cert": true,
			"error": "Install the public testing certificate first; the game cannot connect without it."}
	}
	if findGameBinary(a.exeDir, a.cfg) == "" {
		return map[string]any{"ok": false, "game": true,
			"error": "Dreadnought was not found. Choose your game folder above."}
	}
	if token == "" || launcherTokenExpired(token) {
		return map[string]any{"ok": false, "error": "Your sign-in has expired. Please sign in again.", "signIn": true}
	}
	if _, err := startGame(a.exeDir, a.cfg, token); err != nil {
		return map[string]any{"ok": false, "error": capitalise(err.Error())}
	}
	return map[string]any{"ok": true}
}

// gameLogFolders are where the game may write DreadGame.log: the per-user
// Saved folder of a shipping build, then the Saved folder inside the install.
func (a *launcherAPI) gameLogFolders() []string {
	var dirs []string
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		dirs = append(dirs, filepath.Join(la, "DreadGame", "Saved", "Logs"))
	}
	if exe := findGameBinary(a.exeDir, a.cfg); exe != "" {
		dirs = append(dirs, filepath.Join(gameInstallRoot(exe), "DreadGame", "Saved", "Logs"))
	}
	return dirs
}

// OpenLogs opens the folder holding the game's newest log in Explorer, so a
// player can send it with a bug report.
func (a *launcherAPI) OpenLogs() map[string]any {
	best := a.newestLogFolder()
	if best == "" {
		return map[string]any{"ok": false, "error": "No game logs yet. Start the game once, then try again."}
	}
	verb, _ := windows.UTF16PtrFromString("open")
	target, _ := windows.UTF16PtrFromString(best)
	if err := windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		return map[string]any{"ok": false, "error": "Could not open " + best + ": " + err.Error()}
	}
	return map[string]any{"ok": true, "path": best}
}

// newestLogFolder is the log folder whose DreadGame.log is newest, else the
// first log folder that exists, else "".
func (a *launcherAPI) newestLogFolder() string {
	best, bestTime := "", time.Time{}
	for _, dir := range a.gameLogFolders() {
		info, err := os.Stat(filepath.Join(dir, "DreadGame.log"))
		if err == nil && info.ModTime().After(bestTime) {
			best, bestTime = dir, info.ModTime()
		} else if best == "" {
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				best = dir
			}
		}
	}
	return best
}
