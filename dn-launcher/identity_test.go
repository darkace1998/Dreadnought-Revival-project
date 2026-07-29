//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPlayerIDIsReproducibleWithoutTheIdentityFile is the regression test for
// losing an account by losing a file.
//
// The launcher's player ID is the whole account: it becomes the Steam ticket,
// which the auth server hashes into a steam_id, and an unrecognised steam_id
// auto-registers a new player. The generated ID used to mix in 16 random bytes,
// so a missing player.json produced a different ID, a different account, and
// the tutorial again with no way back -- both hashes are one-way.
func TestPlayerIDIsReproducibleWithoutTheIdentityFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("DN_PLAYER_ID", "")

	first, err := loadOrCreatePlayerID("")
	if err != nil {
		t.Fatalf("first derive: %v", err)
	}
	if first == "" {
		t.Fatal("derived an empty player ID")
	}

	// Lose the file exactly the way a wiped temp directory or a reinstall does.
	if err := os.Remove(filepath.Join(dir, "DreadnoughtPS", "player.json")); err != nil {
		t.Fatalf("remove identity file: %v", err)
	}

	second, err := loadOrCreatePlayerID("")
	if err != nil {
		t.Fatalf("second derive: %v", err)
	}
	if second != first {
		t.Fatalf("player ID changed after losing the identity file: %q -> %q; the account would be abandoned", first, second)
	}
}

// TestConfiguredPlayerIDWins covers the recovery route: pinning an ID in
// dn-launcher.json (or DN_PLAYER_ID) has to override whatever is on disk, since
// that is the only way to reclaim an account or move one between machines.
func TestConfiguredPlayerIDWins(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)

	t.Setenv("DN_PLAYER_ID", "")
	if got, err := loadOrCreatePlayerID("pinned-by-config"); err != nil || got != "pinned-by-config" {
		t.Fatalf("configured ID = %q (err %v), want pinned-by-config", got, err)
	}

	t.Setenv("DN_PLAYER_ID", "pinned-by-env")
	if got, err := loadOrCreatePlayerID(""); err != nil || got != "pinned-by-env" {
		t.Fatalf("env ID = %q (err %v), want pinned-by-env", got, err)
	}
	// Config still beats the environment.
	if got, err := loadOrCreatePlayerID("pinned-by-config"); err != nil || got != "pinned-by-config" {
		t.Fatalf("config did not win over env: got %q (err %v)", got, err)
	}
}
