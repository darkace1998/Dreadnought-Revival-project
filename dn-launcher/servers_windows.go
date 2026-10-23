//go:build windows

package main

// Server picker: the launcher window's Server / Change... A player stays on
// the server this launcher was built or configured for (Config.Server --
// "Default server") or adds any other server by address ("localhost" for one
// on the same computer). The choice is kept per Windows user in settings.json
// and beats dn-launcher.json's server, which stays the default entry. A
// launcher with no server at all asks for one first.
//
// A server other than the default brings its own CA. The launcher fetches it
// from that server's gateway (GET /dn/ca.crt, gateway/main.go), checks that it
// really issued the certificate the gateway presented, and keeps it as
// servers\<key>.crt. The usual certificate screen then shows its fingerprint
// before anything is installed: trust on first use, with the fingerprint as
// the player's check, exactly as for the built-in CA.
//
// Accounts live on a server, so each server keeps its own remembered sign-in
// (credentialServer in account.go).

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type savedServer struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	WebPort string `json:"web_port,omitempty"`
}

// baseConfig is the configuration before any choice made in the window
// (dn-launcher.json + build-time defaults): the Default server entry.
var baseConfig Config

// id is how the page and settings.json name a server: "" for the default,
// else address[:web port].
func (s savedServer) id() string {
	if s.Address == "" {
		return ""
	}
	if s.WebPort != "" && s.WebPort != "443" {
		return s.Address + ":" + s.WebPort
	}
	return s.Address
}

// key is id() made safe for a file name.
func (s savedServer) key() string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			return r
		}
		return '_'
	}, s.id())
}

func (s savedServer) caFile() string {
	return filepath.Join(filepath.Dir(settingsPath()), "servers", s.key()+".crt")
}

// parseServerAddress takes what a player types: "host", "host:port", with or
// without https:// and a trailing slash. The port is the web (sign-in) port.
func parseServerAddress(text string) (savedServer, error) {
	t := strings.TrimSpace(text)
	t = strings.TrimPrefix(strings.TrimPrefix(t, "https://"), "http://")
	t = strings.TrimSuffix(t, "/")
	if t == "" {
		return savedServer{}, errors.New("enter the server's address")
	}
	host, port := t, ""
	if h, p, err := net.SplitHostPort(t); err == nil {
		host, port = h, p
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return savedServer{}, fmt.Errorf("%q is not a port number", p)
		}
	}
	if strings.ContainsAny(host, " /\\?#@[]") {
		return savedServer{}, fmt.Errorf("%q is not a server address", host)
	}
	if port == "443" {
		port = ""
	}
	return savedServer{Address: strings.ToLower(host), WebPort: port}, nil
}

// serverChoices is every entry the picker shows, default first.
func serverChoices(settings launcherSettings) []savedServer {
	out := []savedServer{{Name: "Default server", Address: ""}}
	for _, s := range settings.Servers {
		if s.Address != "" {
			out = append(out, s)
		}
	}
	return out
}

func findServer(settings launcherSettings, id string) (savedServer, bool) {
	for _, s := range serverChoices(settings) {
		if s.id() == id {
			return s, true
		}
	}
	return savedServer{}, false
}

// configFor is baseConfig pointed at choice.
func configFor(choice savedServer) Config {
	cfg := baseConfig
	if choice.Address == "" {
		return cfg
	}
	cfg.Server, cfg.WebPort = choice.Address, choice.WebPort
	// runMachineSetup fills both from the resolved address; a firmament_host
	// in dn-launcher.json belongs to the default server.
	cfg.GatewayIP, cfg.FirmamentHost = "", ""
	cfg.caFile = choice.caFile()
	return cfg
}

// selectServer points the launcher at choice: fetches the server's CA when it
// has none yet, then reruns runMachineSetup. On failure the previous setup is
// put back by the caller.
func selectServer(exeDir string, choice savedServer) (Config, error) {
	cfg := configFor(choice)
	if choice.Address != "" {
		ip, err := resolveServerIP(choice.Address)
		if err != nil {
			return cfg, err
		}
		if _, err := os.Stat(cfg.caFile); errors.Is(err, os.ErrNotExist) {
			der, err := fetchServerCA(ip, choice.WebPort)
			if err != nil {
				return cfg, err
			}
			if err := os.MkdirAll(filepath.Dir(cfg.caFile), 0o700); err != nil {
				return cfg, err
			}
			if err := os.WriteFile(cfg.caFile, der, 0o600); err != nil {
				return cfg, err
			}
		}
	}
	if err := runMachineSetup(exeDir, &cfg); err != nil {
		return cfg, err
	}
	credentialServer = choice.key()
	return cfg, nil
}

// fetchServerCA asks a server's gateway for its CA. The connection cannot be
// verified yet -- that CA is what would verify it -- so the check is that the
// CA offered really issued the certificate presented; the player then
// compares the fingerprint on the certificate screen before installing.
func fetchServerCA(ip, webPort string) ([]byte, error) {
	port := webPort
	if port == "" {
		port = "443"
	}
	var leaf *x509.Certificate
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		//nolint:gosec // see above: checked against the CA below, fingerprint confirmed by the player.
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) > 0 {
				leaf = cs.PeerCertificates[0]
			}
			return nil
		},
	}}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	resp, err := client.Get("https://" + net.JoinHostPort(ip, port) + "/dn/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("cannot reach the server at %s: %w", net.JoinHostPort(ip, port), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the server at %s does not offer its certificate (HTTP %d) -- is it a Dreadnought private server, and up to date?",
			net.JoinHostPort(ip, port), resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	der, err := parseCACert(raw)
	if err != nil {
		return nil, fmt.Errorf("the server's certificate: %w", err)
	}
	ca, _ := x509.ParseCertificate(der)
	if leaf == nil || leaf.CheckSignatureFrom(ca) != nil {
		return nil, errors.New("the server's connection is not signed by the certificate it offers; not trusting it")
	}
	return der, nil
}

// applySavedServer runs at start-up, after loadConfig: the window's choice,
// or the default when that server cannot be reached right now.
func applySavedServer(exeDir string, cfg Config) (Config, error) {
	baseConfig = cfg
	settings := loadSettings()
	if settings.Server == "" {
		return cfg, runMachineSetup(exeDir, &cfg)
	}
	choice, ok := findServer(settings, settings.Server)
	if ok {
		picked, err := selectServer(exeDir, choice)
		if err == nil {
			return picked, nil
		}
		fmt.Printf("[!] %s: %v -- using the default server for now.\n", settings.Server, err)
	}
	credentialServer = ""
	return cfg, runMachineSetup(exeDir, &cfg)
}

// ---- launcher window actions ---------------------------------------------------

func (a *launcherAPI) serverInfo() map[string]any {
	settings := loadSettings()
	current := credentialServer // key of the server in use; "" = default
	list := []map[string]any{}
	def := baseConfig.Server
	if def == "" {
		def = baseConfig.GatewayIP
	}
	currentName := "Default server"
	for _, s := range serverChoices(settings) {
		kind, addr := "custom", s.id()
		selected := s.key() == current
		if s.Address == "" {
			if defaultUnset() {
				continue // nothing built in: the player types a server instead
			}
			kind, addr = "default", def
		}
		if selected {
			currentName = s.Name
		}
		list = append(list, map[string]any{"id": s.id(), "name": s.Name, "address": addr, "kind": kind, "selected": selected})
	}
	return map[string]any{"list": list, "current": currentName, "unset": current == "" && defaultUnset()}
}

// defaultUnset: no server built in or in dn-launcher.json, only the old
// built-in gateway address. The window then asks for a server first.
func defaultUnset() bool {
	return baseConfig.Server == "" && baseConfig.GatewayIP == defaultConfig().GatewayIP
}

// UseServer switches to a listed server (id "" = the default).
func (a *launcherAPI) UseServer(id string) map[string]any {
	settings := loadSettings()
	choice, ok := findServer(settings, id)
	if !ok {
		return map[string]any{"ok": false, "error": "That server is not in the list."}
	}
	prevCfg, prevKey := a.cfg, credentialServer
	cfg, err := selectServer(a.exeDir, choice)
	if err != nil {
		restore := prevCfg
		credentialServer = prevKey
		_ = runMachineSetup(a.exeDir, &restore)
		return map[string]any{"ok": false, "error": capitalise(err.Error())}
	}
	settings.Server = choice.id()
	if err := saveSettings(settings); err != nil {
		return map[string]any{"ok": false, "error": "Could not save the setting: " + err.Error()}
	}
	a.mu.Lock()
	a.cfg = cfg
	a.token, a.username = "", ""
	if creds, ok := loadCredentials(); ok && !launcherTokenExpired(creds.Token) {
		a.token, a.username = creds.Token, creds.Username
	}
	a.mu.Unlock()
	out := a.Init()
	out["ok"] = true
	return out
}

// AddServer saves a server typed by the player and switches to it. A server
// that cannot be reached is not kept.
func (a *launcherAPI) AddServer(name, address string) map[string]any {
	s, err := parseServerAddress(address)
	if err != nil {
		return map[string]any{"ok": false, "error": capitalise(err.Error())}
	}
	s.Name = strings.TrimSpace(name)
	if len([]rune(s.Name)) > 40 {
		s.Name = string([]rune(s.Name)[:40])
	}
	if s.Name == "" {
		s.Name = s.Address
	}
	settings := loadSettings()
	if _, exists := findServer(settings, s.id()); !exists {
		settings.Servers = append(settings.Servers, s)
		if err := saveSettings(settings); err != nil {
			return map[string]any{"ok": false, "error": "Could not save the server: " + err.Error()}
		}
	}
	r := a.UseServer(s.id())
	if r["ok"] != true {
		a.forgetServer(s.id())
	}
	return r
}

// RemoveServer drops a saved server (not Default) and its CA file;
// removing the one in use switches back to the default.
func (a *launcherAPI) RemoveServer(id string) map[string]any {
	settings := loadSettings()
	s, ok := findServer(settings, id)
	if !ok || s.Address == "" {
		return map[string]any{"ok": false, "error": "That server cannot be removed."}
	}
	inUse := s.key() == credentialServer
	a.forgetServer(id)
	_ = os.Remove(s.caFile())
	if inUse {
		return a.UseServer("")
	}
	out := a.Init()
	out["ok"] = true
	return out
}

func (a *launcherAPI) forgetServer(id string) {
	settings := loadSettings()
	kept := settings.Servers[:0]
	for _, s := range settings.Servers {
		if s.id() != id {
			kept = append(kept, s)
		}
	}
	settings.Servers = kept
	_ = saveSettings(settings)
}
