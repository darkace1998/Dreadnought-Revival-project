//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A game in a SECOND Steam library, named by its app manifest: the case the
// old Uninstall-registry lookup missed.
func TestSteamGameFoundInASecondLibrary(t *testing.T) {
	root := t.TempDir()
	lib2 := t.TempDir()
	mustWrite := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// libraryfolders.vdf escapes backslashes.
	vdfLib := strings.ReplaceAll(lib2, `\`, `\\`)
	mustWrite(filepath.Join(root, "steamapps", "libraryfolders.vdf"),
		"\"libraryfolders\"\n{\n\t\"0\"\n\t{\n\t\t\"path\"\t\t\""+strings.ReplaceAll(root, `\`, `\\`)+
			"\"\n\t}\n\t\"1\"\n\t{\n\t\t\"path\"\t\t\""+vdfLib+"\"\n\t}\n}\n")
	mustWrite(filepath.Join(lib2, "steamapps", "appmanifest_835860.acf"),
		"\"AppState\"\n{\n\t\"appid\"\t\t\"835860\"\n\t\"installdir\"\t\t\"Dreadnought\"\n}\n")
	exe := filepath.Join(lib2, "steamapps", "common", "Dreadnought", "DreadGame", "DreadGame", "Binaries", "Win64", "DreadGame-Win64-Shipping.exe")
	mustWrite(exe, "")

	libs := steamLibraries([]string{root})
	if len(libs) != 2 {
		t.Fatalf("libraries = %v, want the root and the second library", libs)
	}
	dir := steamGameDir(libs)
	if gameBinaryIn(dir) != exe {
		t.Fatalf("steamGameDir = %q, want the folder holding %s", dir, exe)
	}
}

// A chosen folder wins over detection.
func TestChosenFolderWinsOverDetection(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	chosen := t.TempDir()
	exe := filepath.Join(chosen, "DreadGame", "DreadGame", "Binaries", "Win64", "DreadGame-Win64-Shipping.exe")
	_ = os.MkdirAll(filepath.Dir(exe), 0o755)
	_ = os.WriteFile(exe, nil, 0o644)
	if err := saveSettings(launcherSettings{GameDir: chosen}); err != nil {
		t.Fatal(err)
	}
	if p, src := gameLocation(t.TempDir(), defaultConfig()); p != exe || src != gameSourceChosen {
		t.Fatalf("gameLocation = %q (%s), want the chosen folder", p, src)
	}
}
