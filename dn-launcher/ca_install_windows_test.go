//go:build windows

package main

import (
	"os"
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
