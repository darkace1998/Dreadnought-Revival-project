//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Players pick whichever folder looks right: the install root, or a folder on
// the way down to the executable. All of them must resolve, and the display
// must show the install root again.
func TestGameFolderAcceptsAnyLevelOfTheInstall(t *testing.T) {
	root := t.TempDir()
	win64 := filepath.Join(root, "DreadGame", "DreadGame", "Binaries", "Win64")
	if err := os.MkdirAll(win64, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(win64, "DreadGame-Win64-Shipping.exe")
	if err := os.WriteFile(exe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, filepath.Join(root, "DreadGame"),
		filepath.Join(root, "DreadGame", "DreadGame"), win64} {
		if got := gameBinaryIn(dir); got != exe {
			t.Errorf("gameBinaryIn(%s) = %q, want %q", dir, got, exe)
		}
	}
	if got := gameBinaryIn(t.TempDir()); got != "" {
		t.Errorf("empty folder resolved to %q", got)
	}
	if got := gameInstallRoot(exe); got != root {
		t.Errorf("gameInstallRoot = %q, want %q", got, root)
	}
}

// The saved folder is used when the launcher is NOT in the game folder.
func TestSavedGameFolderIsFound(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	root := t.TempDir()
	win64 := filepath.Join(root, "DreadGame", "DreadGame", "Binaries", "Win64")
	_ = os.MkdirAll(win64, 0o755)
	exe := filepath.Join(win64, "DreadGame-Win64-Shipping.exe")
	_ = os.WriteFile(exe, nil, 0o644)

	elsewhere := t.TempDir() // e.g. Downloads
	if err := saveSettings(launcherSettings{GameDir: root}); err != nil {
		t.Fatal(err)
	}
	if got := findGameBinary(elsewhere, defaultConfig()); got != exe {
		t.Fatalf("findGameBinary = %q, want %q", got, exe)
	}
}

// The toggles and the game folder share settings.json; saving one must not
// drop the others.
func TestSettingsKeepEveryField(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if err := saveSettings(launcherSettings{GameDir: `C:\Games\Dreadnought`, LogWindow: true, VerboseLog: true}); err != nil {
		t.Fatal(err)
	}
	s := loadSettings()
	s.GameDir = `D:\Dreadnought`
	if err := saveSettings(s); err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(); !got.LogWindow || !got.VerboseLog || got.GameDir != `D:\Dreadnought` {
		t.Fatalf("settings after update = %+v", got)
	}
}

// "Open game log folder" picks the folder with the newest DreadGame.log.
func TestNewestLogFolder(t *testing.T) {
	la := t.TempDir()
	t.Setenv("LOCALAPPDATA", la)
	root := t.TempDir()
	win64 := filepath.Join(root, "DreadGame", "DreadGame", "Binaries", "Win64")
	_ = os.MkdirAll(win64, 0o755)
	_ = os.WriteFile(filepath.Join(win64, "DreadGame-Win64-Shipping.exe"), nil, 0o644)
	if err := saveSettings(launcherSettings{GameDir: root}); err != nil {
		t.Fatal(err)
	}
	api := &launcherAPI{exeDir: t.TempDir(), cfg: defaultConfig()}
	if got := api.newestLogFolder(); got != "" {
		t.Fatalf("no logs yet, got %q", got)
	}
	userLogs := filepath.Join(la, "DreadGame", "Saved", "Logs")
	gameLogs := filepath.Join(root, "DreadGame", "Saved", "Logs")
	_ = os.MkdirAll(userLogs, 0o755)
	_ = os.MkdirAll(gameLogs, 0o755)
	_ = os.WriteFile(filepath.Join(userLogs, "DreadGame.log"), []byte("old"), 0o644)
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(userLogs, "DreadGame.log"), old, old)
	_ = os.WriteFile(filepath.Join(gameLogs, "DreadGame.log"), []byte("new"), 0o644)
	if got := api.newestLogFolder(); got != gameLogs {
		t.Fatalf("newestLogFolder = %q, want %q", got, gameLogs)
	}
}
