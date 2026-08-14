package server

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The loadout mod deploys AS wer.dll, and this repo also ships
// bin/wer-proxy/wer.dll -- a WER LOGGING shim with no loadout code in it. The
// two are byte-different but same-named, so a presence check alone reports
// "present and enabled" while nobody can spawn. That happened: the operator's
// install had the proxy, the host logged "Active Loadout not found. Can't
// spawn", and the tooling would have said everything was fine.
func TestModStateRejectsTheWerProxy(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "DreadGame-Win64-Shipping.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The real proxy's contents, near enough: WER exports, no loadout marker.
	if err := os.WriteFile(filepath.Join(dir, "wer.dll"),
		[]byte("Dreadnought WER proxy diagnostics\x00WerReportCreate\x00WerReportSubmit"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dn_server_loadout.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	reportHostLoadoutModState(LaunchConfig{GameBinary: exe}, &out)
	got := out.String()
	if !bytes.Contains([]byte(got), []byte("NOT the loadout mod")) {
		t.Errorf("the WER proxy was accepted as the loadout mod; report said: %q", got)
	}
	if bytes.Contains([]byte(got), []byte("present and enabled")) {
		t.Errorf("reported the fix as working with only the proxy installed: %q", got)
	}
}

// And the real thing must still be accepted.
func TestModStateAcceptsTheRealMod(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "DreadGame-Win64-Shipping.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wer.dll"),
		[]byte("...binary...[dn-host-loadout] %s %s\n...more..."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dn_server_loadout.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	reportHostLoadoutModState(LaunchConfig{GameBinary: exe}, &out)
	if got := out.String(); !bytes.Contains([]byte(got), []byte("present and enabled")) {
		t.Errorf("the real mod was rejected: %q", got)
	}
}

// A marker split across the reader's chunk boundary must still be found, or a
// real mod could be rejected depending on where the bytes happen to land.
func TestFileContainsFindsAMarkerAcrossChunks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	marker := []byte("dn-host-loadout")
	// Land the marker so it straddles the 64KiB read boundary.
	pad := make([]byte, (64<<10)-len(marker)/2)
	for i := range pad {
		pad[i] = 'A'
	}
	if err := os.WriteFile(path, append(append(pad, marker...), 'B'), 0o600); err != nil {
		t.Fatal(err)
	}
	if !fileContains(path, marker) {
		t.Error("marker straddling a chunk boundary was not found")
	}
	if fileContains(path, []byte("not-in-this-file")) {
		t.Error("found a marker that is not present")
	}
}
