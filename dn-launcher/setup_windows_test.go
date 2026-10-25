//go:build windows

package main

import "testing"

// A server NAME goes to the game as-is (so a changing outside IP does not
// matter), while the launcher's own connections dial the address it resolves.
func TestServerNameIsResolvedForTheGameAndTheLauncher(t *testing.T) {
	defer func() { serverDialIP, serverCAPool, serverConfigured = "", nil, false }()
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

// With a server setting, BOTH game addresses follow the DNS answer: a stale
// firmament_host / gateway_ip left in an old dn-launcher.json (they used to
// carry the server's outside IP of the day) no longer wins. And every refresh
// re-points the launcher's own connections.
func TestServerSettingOverridesStaleAddresses(t *testing.T) {
	defer func() { serverDialIP, serverConfigured = "", false }()
	cfg := defaultConfig()
	cfg.Server = "198.51.100.20"
	cfg.GatewayIP, cfg.FirmamentHost = "198.51.100.10", "198.51.100.10"
	ip, err := refreshServerIP(&cfg)
	if err != nil || ip != "198.51.100.20" {
		t.Fatalf("refresh: %q %v", ip, err)
	}
	if cfg.GatewayIP != ip || cfg.FirmamentHost != ip || dialIP() != ip {
		t.Fatalf("gateway=%q firmament=%q dial=%q, want %s", cfg.GatewayIP, cfg.FirmamentHost, dialIP(), ip)
	}
	// The server "moves": the next refresh follows it.
	cfg.Server = "198.51.100.30"
	if ip, _ = refreshServerIP(&cfg); ip != "198.51.100.30" || dialIP() != ip || cfg.FirmamentHost != ip {
		t.Fatalf("after the move: %q dial=%q firmament=%q", ip, dialIP(), cfg.FirmamentHost)
	}
}

// A name that does not resolve is no longer fatal at start: the launcher opens
// and tries again later.
func TestUnresolvableServerIsNotFatalAtStart(t *testing.T) {
	defer func() { serverDialIP, serverCAPool, serverConfigured = "", nil, false }()
	saved := publicResolvers
	publicResolvers = nil // no network in tests
	defer func() { publicResolvers = saved }()
	cfg := defaultConfig()
	cfg.Server = "no-such-host.invalid"
	if err := runMachineSetup(t.TempDir(), &cfg); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if !serverConfigured || dialIP() != "" {
		t.Fatalf("configured=%v dial=%q", serverConfigured, dialIP())
	}
	if _, err := refreshServerIP(&cfg); err == nil {
		t.Fatal("an unresolvable name resolved")
	}
}
