//go:build windows

package main

import "testing"

// A server NAME goes to the game as-is (so a changing outside IP does not
// matter), while the launcher's own connections dial the address it resolves.
func TestServerNameIsResolvedForTheGameAndTheLauncher(t *testing.T) {
	defer func() { serverDialIP, serverCAPool = "", nil }()
	cfg := defaultConfig()
	cfg.Server = "localhost"
	if err := runMachineSetup(t.TempDir(), &cfg); err != nil {
		t.Fatal(err)
	}
	// The IP, not the name: the game's gateway parser (FInternetAddr::SetIp)
	// rejects names -- "Invalid address: <name>, Port: 65443" in a live client.
	if cfg.GatewayIP != "127.0.0.1" || cfg.FirmamentHost != "127.0.0.1" {
		t.Errorf("game gets gateway=%q firmament=%q, want the resolved IP", cfg.GatewayIP, cfg.FirmamentHost)
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
