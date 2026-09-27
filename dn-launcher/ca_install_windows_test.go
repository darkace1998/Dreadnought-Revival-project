//go:build windows

package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// Real store test, opt-in: DN_LAUNCHER_CA_DIR=<dir holding ca.crt>. Installs
// into the CURRENT USER Trusted Root store of the machine (or Wine prefix) it
// runs on, then checks a second run finds it instead of adding it again.
func TestEnsureCAInstalledAgainstTheRealStore(t *testing.T) {
	dir := os.Getenv("DN_LAUNCHER_CA_DIR")
	if dir == "" {
		t.Skip("set DN_LAUNCHER_CA_DIR to run against the real certificate store")
	}
	der, err := readCACert(dir)
	if err != nil || der == nil {
		t.Fatalf("readCACert: %v (nil=%v)", err, der == nil)
	}
	if err := ensureCAInstalled(der); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := ensureCAInstalled(der); err != nil {
		t.Fatalf("second run: %v", err)
	}
}

// The public launcher is one exe: the CA comes from -X main.defaultCA when no
// ca.crt ships beside it, and runMachineSetup only FINDS it -- the desktop
// window asks before installing (pendingCA), so nothing is installed here.
func TestBuiltInCAIsFoundButNotInstalledBySetup(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "certs", "ca.crt"))
	if err != nil {
		t.Skip("no certs/ca.crt in this checkout")
	}
	defer func(old string) { defaultCA = old }(defaultCA)
	defaultCA = base64.StdEncoding.EncodeToString(raw)
	pendingCA = nil

	cfg := defaultConfig()
	cfg.Server = "127.0.0.1"
	if err := runMachineSetup(t.TempDir(), &cfg); err != nil {
		t.Fatal(err)
	}
	if pendingCA == nil || serverCAPool == nil {
		t.Fatal("built-in CA not picked up")
	}
	if name, fp := caSummary(pendingCA); name == "" || len(fp) != 95 {
		t.Fatalf("caSummary = %q, %q", name, fp)
	}
}
