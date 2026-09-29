//go:build windows

package main

// Game discovery and launch. The game folder, log toggles and Steam lookup
// are shared with dn-launcher through the same settings file
// (%LOCALAPPDATA%\DreadnoughtPrivateServer\settings.json), so choosing the
// folder once counts for both programs. Per-cluster state (server address,
// CA trust, account) lives in the browserAPI, not here.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)
type browserSettings struct {
	GameDir    string `json:"game_dir"`
	LogWindow  bool   `json:"log_window"`
	VerboseLog bool   `json:"verbose_log"`
}

func settingsPath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "DreadnoughtPrivateServer", "settings.json")
}

func loadSettings() browserSettings {
	var s browserSettings
	//nolint:gosec // fixed file under the user's own LOCALAPPDATA.
	if data, err := os.ReadFile(settingsPath()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func saveSettings(s browserSettings) error {
	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(settingsPath(), data, 0o600)
}

// gameBinaryIn finds the game executable in dir, accepting the install root
// or any folder on the way down to Binaries\Win64.
func gameBinaryIn(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	win64 := filepath.Join("Binaries", "Win64")
	for _, sub := range []string{
		filepath.Join("DreadGame", "DreadGame", win64),
		filepath.Join("DreadGame", win64),
		win64,
		"Win64",
		"",
	} {
		for _, exe := range []string{"DreadGame-Win64-Shipping-patched.exe", "DreadGame-Win64-Shipping.exe"} {
			p := filepath.Join(dir, sub, exe)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// gameInstallRoot turns an executable path back into the folder a player
// recognises (the one holding DreadGame\), for display.
func gameInstallRoot(exePath string) string {
	dir := filepath.Dir(exePath)
	suffix := string(filepath.Separator) + filepath.Join("DreadGame", "DreadGame", "Binaries", "Win64")
	if strings.HasSuffix(strings.ToLower(dir), strings.ToLower(suffix)) {
		return dir[:len(dir)-len(suffix)]
	}
	return dir
}

// pickFolder shows Windows' folder picker, owned by the browser window.
func pickFolder(owner uintptr, title string) (string, bool) {
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	const (
		bifReturnOnlyFSDirs  = 0x0001
		bifNewDialogStyle    = 0x0040
		bifNoNewFolderButton = 0x0200
	)
	var display [windows.MAX_PATH]uint16
	t, _ := windows.UTF16PtrFromString(title)
	bi := struct {
		Owner       uintptr
		Root        uintptr
		DisplayName *uint16
		Title       *uint16
		Flags       uint32
		Callback    uintptr
		LParam      uintptr
		Image       int32
	}{Owner: owner, DisplayName: &display[0], Title: t,
		Flags: bifReturnOnlyFSDirs | bifNewDialogStyle | bifNoNewFolderButton}
	pidl, _, _ := shell32.NewProc("SHBrowseForFolderW").Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", false // cancelled
	}
	defer func() { _, _, _ = windows.NewLazySystemDLL("ole32.dll").NewProc("CoTaskMemFree").Call(pidl) }()
	var path [windows.MAX_PATH]uint16
	if r, _, _ := shell32.NewProc("SHGetPathFromIDListW").Call(pidl, uintptr(unsafe.Pointer(&path[0]))); r == 0 {
		return "", false
	}
	return windows.UTF16ToString(path[:]), true
}

// gameLocation finds the game executable: saved folder first, then the
// browser's own folder, then Steam's libraries. A manual choice always wins.
func gameLocation(exeDir string) (path, source string) {
	if p := gameBinaryIn(loadSettings().GameDir); p != "" {
		return p, "chosen"
	}
	if p := gameBinaryIn(exeDir); p != "" {
		return p, "browser folder"
	}
	if p := gameBinaryIn(steamGameDir(steamLibraries(steamRoots()))); p != "" {
		return p, "Steam"
	}
	if p := gameBinaryIn(steamUninstallDir()); p != "" {
		return p, "Steam"
	}
	return "", ""
}

func findGameBinary(exeDir string) string {
	p, _ := gameLocation(exeDir)
	return p
}

// ---- Steam lookup (app 835860) ------------------------------------------

const dreadnoughtSteamAppID = "835860"

var (
	vdfPath       = regexp.MustCompile(`(?m)^\s*"path"\s*"((?:[^"\\]|\\.)*)"`)
	vdfInstallDir = regexp.MustCompile(`(?m)^\s*"installdir"\s*"((?:[^"\\]|\\.)*)"`)
)

func vdfUnescape(s string) string { return strings.ReplaceAll(s, `\\`, `\`) }

func steamRoots() []string {
	var roots []string
	add := func(root registry.Key, path, value string) {
		k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
		if err != nil {
			return
		}
		defer func() { _ = k.Close() }()
		if v, _, err := k.GetStringValue(value); err == nil && v != "" {
			roots = append(roots, filepath.Clean(filepath.FromSlash(v)))
		}
	}
	add(registry.CURRENT_USER, `Software\Valve\Steam`, "SteamPath")
	add(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Valve\Steam`, "InstallPath")
	add(registry.LOCAL_MACHINE, `SOFTWARE\Valve\Steam`, "InstallPath")
	return roots
}

func steamLibraries(roots []string) []string {
	seen := map[string]bool{}
	var libs []string
	add := func(p string) {
		key := strings.ToLower(filepath.Clean(p))
		if p != "" && !seen[key] {
			seen[key] = true
			libs = append(libs, filepath.Clean(p))
		}
	}
	for _, root := range roots {
		add(root)
		//nolint:gosec // Steam's own library list under its install folder.
		if data, err := os.ReadFile(filepath.Join(root, "steamapps", "libraryfolders.vdf")); err == nil {
			for _, m := range vdfPath.FindAllStringSubmatch(string(data), -1) {
				add(vdfUnescape(m[1]))
			}
		}
	}
	return libs
}

func steamGameDir(libs []string) string {
	for _, lib := range libs {
		//nolint:gosec // Steam's own app manifest.
		data, err := os.ReadFile(filepath.Join(lib, "steamapps", "appmanifest_"+dreadnoughtSteamAppID+".acf"))
		if err != nil {
			continue
		}
		if m := vdfInstallDir.FindStringSubmatch(string(data)); m != nil {
			dir := filepath.Join(lib, "steamapps", "common", vdfUnescape(m[1]))
			if gameBinaryIn(dir) != "" {
				return dir
			}
		}
	}
	for _, lib := range libs {
		entries, err := os.ReadDir(filepath.Join(lib, "steamapps", "common"))
		if err != nil {
			continue
		}
		for _, e := range entries {
			dir := filepath.Join(lib, "steamapps", "common", e.Name())
			if e.IsDir() && gameBinaryIn(dir) != "" {
				return dir
			}
		}
	}
	return ""
}

func steamUninstallDir() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Steam App `+dreadnoughtSteamAppID, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer func() { _ = k.Close() }()
	dir, _, _ := k.GetStringValue("InstallLocation")
	return dir
}

// ---- game launch ----------------------------------------------------------

const regPath = `SOFTWARE\Grey Box\Dreadnought`

func writeAuthToken(jwtToken string) error {
	encrypted, err := dpapiEncrypt([]byte(jwtToken))
	if err != nil {
		return fmt.Errorf("DPAPI encrypt: %w", err)
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, regPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create registry key: %w", err)
	}
	defer func() {
		_ = k.Close()
	}()
	return k.SetBinaryValue("AuthToken", encrypted)
}

// launchConfig carries everything startGame needs for the selected cluster.
type launchConfig struct {
	gatewayIP     string
	gatewayPort   string
	firmamentHost string
	firmamentPort string
	allowSteam    bool
	logWindow     bool
	verboseLog    bool
}

// startGame hands the token to the game (registry, DPAPI) and starts it with
// the selected cluster's addresses. Shared by both front ends.
func startGame(exeDir string, cfg launchConfig, jwtToken string) (int, error) {
	fmt.Println("[*] Writing auth token to registry...")
	if err := writeAuthToken(jwtToken); err != nil {
		return 0, fmt.Errorf("could not store the sign-in for the game: %w", err)
	}
	fmt.Println("[+] Auth token written.")

	gamePath := findGameBinary(exeDir)
	if gamePath == "" {
		fmt.Fprintln(os.Stderr, "[!] Could not find the game.")
		return 0, fmt.Errorf("could not find Dreadnought -- choose your game folder")
	}
	fmt.Printf("[*] Game binary: %s\n", gamePath)

	firmamentHost := cfg.firmamentHost
	if firmamentHost == "" {
		firmamentHost = cfg.gatewayIP
	}
	var args []string
	args = append(args,
		"-GatewayAddress="+cfg.gatewayIP,
		"-GatewayPort="+cfg.gatewayPort,
		"-YFirmamentAddress="+firmamentHost,
		"-YFirmamentPort="+cfg.firmamentPort,
		"-noeac",
	)
	if cfg.allowSteam {
		fmt.Println("[+] Steam: leaving the client's Steam subsystem enabled (allow_steam)")
	} else {
		args = append(args, "-NoSteam")
	}
	if cfg.logWindow {
		args = append(args, "-LOG")
	}
	if cfg.verboseLog {
		// LogYComVOComponent stays at Log: above Verbose the client crashes
		// in PlayVoiceLineInternal (dn-launcher documents the dump).
		args = append(args, `-LogCmds=global verbose, LogYComVOComponent log`, "-forcelogflush")
	}

	fmt.Printf("[*] Launching: %s\n", gamePath)
	fmt.Printf("    Args: %s\n", strings.Join(args, " "))
	fmt.Println()

	//nolint:gosec // gamePath is resolved from the local install and args are explicit argv values, not shell-expanded input.
	cmd := exec.Command(gamePath, args...)
	cmd.Dir = filepath.Dir(gamePath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("could not start the game: %w", err)
	}
	fmt.Printf("[+] Game launched (PID %d). Browser exiting.\n", cmd.Process.Pid)
	return cmd.Process.Pid, nil
}

// ---- cluster news ---------------------------------------------------------

type newsTile struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Active bool   `json:"active"`
	Size   string `json:"section_size"`
}

// fetchNews reads the active cluster's launcher tiles through the
// cluster-bound transport (Host-header routing does the rest).
func fetchNews() ([]newsTile, error) {
	resp, err := browserHTTPClient().Get(newsFeedURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("news: HTTP %d", resp.StatusCode)
	}
	var doc struct {
		Result struct {
			Tiles []newsTile `json:"tiles"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("news: %w", err)
	}
	var out []newsTile
	for _, t := range doc.Result.Tiles {
		if t.Active {
			out = append(out, t)
		}
	}
	return out, nil
}
