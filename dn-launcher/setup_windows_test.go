//go:build windows

package main

import "testing"

// A server NAME goes to the game as-is (so a changing outside IP does not
// matter), while the launcher's own connections dial the address it resolves.
func TestServerNameReachesTheGameAndTheLauncherDialsItsAddress(t *testing.T) {
	defer func() { serverDialIP, serverCAPool = "", nil }()
	cfg := defaultConfig()
	cfg.Server = "localhost"
	if err := runMachineSetup(t.TempDir(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.GatewayIP != "localhost" || cfg.FirmamentHost != "localhost" {
		t.Errorf("game gets gateway=%q firmament=%q, want the name", cfg.GatewayIP, cfg.FirmamentHost)
	}
	if serverDialIP != "127.0.0.1" {
		t.Errorf("launcher dials %q, want the resolved 127.0.0.1", serverDialIP)
	}
}

// Built without -X main.defaultServer, a launcher points nowhere by default.
func TestDefaultServerIsABuildTimeSetting(t *testing.T) {
	if defaultConfig().Server != defaultServer {
		t.Fatal("defaultConfig ignores defaultServer")
	}
}
