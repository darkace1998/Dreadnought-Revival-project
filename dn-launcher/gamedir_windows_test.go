//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
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
