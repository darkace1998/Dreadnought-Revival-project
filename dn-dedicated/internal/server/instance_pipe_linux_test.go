//go:build linux

package server

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A host whose output pipe outlives it -- the first host in a Wine prefix
// launches wineserver and the prefix services, which inherit the pipe and are
// never killed -- must still finish when its own process exits. It did not:
// the instance stayed "running" with its port reserved (port 7900,
// 2026-09-30).
func TestInstanceFinishesWhenAChildKeepsItsOutputOpen(t *testing.T) {
	dir := t.TempDir()
	fakeWine := filepath.Join(dir, "wine")
	// Exec'd as: wine <binary> <args...>. Leaves a child holding stdout.
	script := "#!/bin/sh\n(sleep 20) &\necho started\nexit 0\n"
	if err := os.WriteFile(fakeWine, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "DreadGame-Win64-Shipping.exe")
	if err := os.WriteFile(binary, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	inst, err := Launch(LaunchConfig{GameBinary: binary, WineExe: fakeWine, Port: 7999, MaxPlayers: 2, LogTo: io.Discard, ShowWindow: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killTaggedProcesses(inst.ID) })
	select {
	case <-inst.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the instance did not finish after its process exited")
	}
}
