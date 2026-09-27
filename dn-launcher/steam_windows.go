//go:build windows

package main

// Finding a Steam install of Dreadnought (app 835860), wherever the player's
// Steam libraries are. The launcher used to check only the Uninstall registry
// entry, which misses games in a second library folder.
//
// Steam's own records: the Steam folder (HKCU\Software\Valve\Steam SteamPath,
// or HKLM ...\Valve\Steam InstallPath), its library list
// (steamapps\libraryfolders.vdf, one "path" per library), and per game an
// appmanifest_<appid>.acf whose "installdir" names the folder under
// steamapps\common.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const dreadnoughtSteamAppID = "835860"

var vdfValue = func(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*"` + key + `"\s*"((?:[^"\\]|\\.)*)"`)
}

var (
	vdfPath       = vdfValue("path")
	vdfInstallDir = vdfValue("installdir")
)

func vdfUnescape(s string) string { return strings.ReplaceAll(s, `\\`, `\`) }

// steamRoots are the Steam installation folders the registry names.
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

// steamLibraries lists every Steam library folder: each root, plus the paths
// in its libraryfolders.vdf. Duplicates removed, case-insensitively.
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

// steamGameDir finds Dreadnought's folder in the given libraries: through the
// app manifest's installdir first, then any folder under steamapps\common that
// holds the game.
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

// steamUninstallDir is the older lookup: Steam's Uninstall registry entry.
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
