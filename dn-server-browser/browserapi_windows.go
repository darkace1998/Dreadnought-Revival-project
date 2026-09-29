//go:build windows

package main

// The browser's actions, shared by both front ends: the WebView2 window
// (desktop) and the loopback browser page (--console). One cluster is active
// at a time; selecting it resets the connection globals (transport, CA pool)
// so the proven single-server flows — sign-in, news, play — work unchanged.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const profileAuthURL = "https://profile-api.prod.greybox.sixfoot.live/auth/"
const newsFeedURL = "https://legacyapi.prod.greybox.sixfoot.live/v2/dreadnought/launcher/dn/tiles/en/"

// browserConfig persists the directory URL and hand-added servers, per
// Windows user.
type browserConfig struct {
	Directory string         `json:"directory"`
	Manual    []manualServer `json:"manual"`
}

type manualServer struct {
	Name   string `json:"name"`
	WebURL string `json:"web_url"`
	CACert string `json:"ca_cert"`
}

func browserConfigPath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "DreadnoughtPrivateServer", "browser.json")
}

func loadBrowserConfig() browserConfig {
	var c browserConfig
	//nolint:gosec // fixed file under the user's own LOCALAPPDATA.
	if data, err := os.ReadFile(browserConfigPath()); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	return c
}

func saveBrowserConfig(c browserConfig) error {
	if err := os.MkdirAll(filepath.Dir(browserConfigPath()), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(browserConfigPath(), data, 0o600)
}

func trustStorePath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "DreadnoughtPrivateServer", "trusted-cas.json")
}

// activeCluster is the selected cluster with everything resolved for use.
type activeCluster struct {
	id      string // directory id, "" for manual servers
	name    string
	webURL  string // https base players authenticate against
	ip      string // resolved IPv4 for dialing + game addresses
	webPort string // outside HTTPS port, "" for 443
	caDER   []byte // cluster CA (nil when unknown)
	dirFP   string // fingerprint the directory reports ("" for manual)
	key     string // trust/credential key
	motd    string // shown on the home screen (listed clusters)
	version string // shown on the home screen (listed clusters)
	players int    // shown on the home screen (listed clusters)
}

// pendingCert is a CA awaiting the player's confirmation. Never installed
// silently: the window shows its fingerprint first.
type pendingCert struct {
	clusterName string
	fp          string // computed from the CA bytes
	dirFP       string // directory's value ("" for manual servers)
	der         []byte
	key         string
}

type browserAPI struct {
	exeDir string
	cfg    browserConfig
	owner  func() uintptr

	mu       sync.Mutex
	dir      *DirectoryClient
	clusters []Cluster
	dirErr   string
	active   *activeCluster
	pending  *pendingCert
	token    string
	username string
	userID   string // auth account id (dashed); normalized for presence checks
	trust    *TrustStore
}

func newBrowserAPI(exeDir, directory string) *browserAPI {
	cfg := loadBrowserConfig()
	if strings.TrimSpace(directory) != "" {
		cfg.Directory = strings.TrimSpace(directory)
	}
	trust, _ := OpenTrustStore(trustStorePath())
	if trust == nil {
		trust, _ = OpenTrustStore(filepath.Join(os.TempDir(), "dn-browser-trusted-cas.json"))
	}
	return &browserAPI{
		exeDir: exeDir,
		cfg:    cfg,
		dir:    &DirectoryClient{BaseURL: cfg.Directory},
		trust:  trust,
	}
}

func (a *browserAPI) clusterView(c Cluster) map[string]any {
	return map[string]any{
		"id": c.ID, "name": c.Name, "url": c.WebURL, "battle": c.BattleIP,
		"version": c.Version, "motd": c.MOTD, "players": c.Players, "servers": c.Servers,
	}
}

// DirectoryURL reports the configured directory (for display + override).
func (a *browserAPI) DirectoryURL() string { return a.cfg.Directory }

// SetDirectory changes the directory URL (operator default baked in, user
// override saved).
func (a *browserAPI) SetDirectory(url string) map[string]any {
	url = strings.TrimSpace(strings.TrimRight(url, "/"))
	if url == "" {
		return map[string]any{"ok": false, "error": "Enter the directory address."}
	}
	a.mu.Lock()
	a.cfg.Directory = url
	a.dir = &DirectoryClient{BaseURL: url}
	a.clusters = nil
	a.dirErr = ""
	a.mu.Unlock()
	if err := saveBrowserConfig(a.cfg); err != nil {
		return map[string]any{"ok": false, "error": "Could not save: " + err.Error()}
	}
	return a.RefreshDirectory()
}

// RefreshDirectory reloads the cluster list. Never fatal: an unreachable
// directory leaves the last list (and manual servers) usable.
func (a *browserAPI) RefreshDirectory() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	if strings.TrimSpace(a.cfg.Directory) == "" {
		a.dirErr = "No directory configured. Enter one below, or add a server by hand."
		return a.directoryView()
	}
	list, err := (&DirectoryClient{BaseURL: a.cfg.Directory}).List()
	if err != nil {
		a.dirErr = "Directory unreachable: " + err.Error()
		return a.directoryView()
	}
	a.clusters = list
	a.dirErr = ""
	return a.directoryView()
}

func (a *browserAPI) directoryView() map[string]any {
	out := map[string]any{"directory": a.cfg.Directory, "error": a.dirErr, "clusters": []any{}, "manual": []any{}}
	for _, c := range a.clusters {
		out["clusters"] = append(out["clusters"].( []any), a.clusterView(c))
	}
	for _, m := range a.cfg.Manual {
		out["manual"] = append(out["manual"].( []any), map[string]any{"name": m.Name, "url": m.WebURL})
	}
	return out
}

// webSplit splits an https base URL into host and outside port.
func webSplit(webURL string) (host, port string, err error) {
	u, err := url.Parse(strings.TrimSpace(webURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", "", fmt.Errorf("not an http(s) URL")
	}
	port = u.Port()
	if port == "" || port == "443" {
		port = ""
	}
	return u.Hostname(), port, nil
}

// SelectCluster makes a directory cluster active: resolves its address,
// loads its CA, restores a saved sign-in when still valid.
func (a *browserAPI) SelectCluster(id string) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	var found *Cluster
	for i := range a.clusters {
		if a.clusters[i].ID == id {
			found = &a.clusters[i]
			break
		}
	}
	if found == nil {
		return map[string]any{"ok": false, "error": "Unknown cluster. Refresh the list."}
	}
	host, port, err := webSplit(found.WebURL)
	if err != nil {
		return map[string]any{"ok": false, "error": "The cluster's address is invalid: " + err.Error()}
	}
	ip, err := resolveServerIP(host)
	if err != nil {
		return map[string]any{"ok": false, "error": "Cannot reach " + host + ": " + err.Error()}
	}
	var der []byte
	if strings.TrimSpace(found.CACert) != "" {
		der, err = parseCAPEM([]byte(found.CACert))
		if err != nil {
			return map[string]any{"ok": false, "error": "The cluster's certificate data is invalid."}
		}
		if fp, _ := FingerprintOfCACert([]byte(found.CACert)); normalizeFingerprint(fp) != normalizeFingerprint(found.CAFingerprint) {
			return map[string]any{"ok": false, "error": "The directory's fingerprint does not match its certificate. Stopping."}
		}
	}
	key := clusterTrustKey(found.ID, found.WebURL)
	if _, err := selectClusterTransport(ip, port, der); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	a.active = &activeCluster{id: found.ID, name: found.Name, webURL: found.WebURL,
		ip: ip, webPort: port, caDER: der, dirFP: found.CAFingerprint, key: key,
		motd: found.MOTD, version: found.Version, players: found.Players}
	a.pending = nil
	a.token, a.username, a.userID = "", "", ""
	if creds, ok := loadCredentials(key); ok && !browserTokenExpired(creds.Token) {
		a.token, a.username, a.userID = creds.Token, creds.Username, creds.UserID
	}
	return a.activeView()
}

// AddManual adds a hand-entered (unlisted, opt-out) server: name, https URL
// and the operator's ca.crt contents (pasted once, stored locally).
func (a *browserAPI) AddManual(name, webURL, caPEM string) map[string]any {
	name = strings.TrimSpace(name)
	if name == "" {
		return map[string]any{"ok": false, "error": "Give the server a name."}
	}
	host, port, err := webSplit(webURL)
	if err != nil {
		return map[string]any{"ok": false, "error": "Not an https URL (https://host or https://host:port)."}
	}
	webURL = "https://" + host
	if port != "" {
		webURL += ":" + port
	}
	der, err := parseCAPEM([]byte(caPEM))
	if err != nil {
		return map[string]any{"ok": false, "error": "Paste the server's ca.crt text. " + err.Error()}
	}
	ip, err := resolveServerIP(host)
	if err != nil {
		return map[string]any{"ok": false, "error": "Cannot reach " + host + ": " + err.Error()}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	webURLNorm := strings.ToLower(webURL)
	kept := a.cfg.Manual[:0]
	for _, m := range a.cfg.Manual {
		if !strings.EqualFold(m.WebURL, webURLNorm) {
			kept = append(kept, m)
		}
	}
	a.cfg.Manual = append(kept, manualServer{Name: name, WebURL: webURLNorm, CACert: strings.TrimSpace(caPEM)})
	if err := saveBrowserConfig(a.cfg); err != nil {
		return map[string]any{"ok": false, "error": "Could not save: " + err.Error()}
	}
	key := clusterTrustKey("", webURLNorm)
	if _, err := selectClusterTransport(ip, port, der); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	a.active = &activeCluster{name: name, webURL: webURLNorm, ip: ip, webPort: port,
		caDER: der, key: key}
	a.pending = nil
	a.token, a.username, a.userID = "", "", ""
	if creds, ok := loadCredentials(key); ok && !browserTokenExpired(creds.Token) {
		a.token, a.username, a.userID = creds.Token, creds.Username, creds.UserID
	}
	return a.activeView()
}

// RemoveManual forgets a hand-added server (and its saved sign-in).
func (a *browserAPI) RemoveManual(webURL string) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	kept := a.cfg.Manual[:0]
	for _, m := range a.cfg.Manual {
		if !strings.EqualFold(m.WebURL, webURL) {
			kept = append(kept, m)
		}
	}
	a.cfg.Manual = kept
	_ = saveBrowserConfig(a.cfg)
	if a.active != nil && a.active.id == "" && strings.EqualFold(a.active.webURL, webURL) {
		a.active, a.token, a.username, a.pending = nil, "", "", nil
	}
	return map[string]any{"ok": true}
}

// activeView describes the selected cluster for the page: sign-in state,
// game folder, and whether the certificate still needs its one confirmation.
func (a *browserAPI) activeView() map[string]any {
	out := map[string]any{"ok": true, "signedIn": a.token != "", "username": a.username,
		"game": gameInfo(a.exeDir), "logWindow": loadSettings().LogWindow, "verboseLog": loadSettings().VerboseLog}
	if a.active == nil {
		return out
	}
	out["cluster"] = map[string]any{"name": a.active.name, "url": a.active.webURL,
		"motd": a.active.motd, "version": a.active.version, "players": a.active.players}
	out["cert"] = a.certState()
	return out
}

// certState answers what the game needs: TRUSTED (nothing to do), PUBLIC
// (system roots already cover it), or PENDING (show the fingerprint screen).
func (a *browserAPI) certState() map[string]any {
	if a.active == nil {
		return map[string]any{"state": "none"}
	}
	if len(a.active.caDER) > 0 {
		if caTrusted(a.active.caDER) {
			return map[string]any{"state": "trusted"}
		}
		name, fp := caSummary(a.active.caDER)
		return map[string]any{"state": "pending", "name": name, "fingerprint": fp,
			"remembered": a.trust.Trusted(a.active.key, fp),
			"directory": a.active.dirFP != "" && normalizeFingerprint(a.active.dirFP) == normalizeFingerprint(fp),
			"dirfp":     FingerprintDisplay(a.active.dirFP)}
	}
	if probePublicTrust(a.active.webURL) {
		return map[string]any{"state": "public"}
	}
	return map[string]any{"state": "missing",
		"error": "No certificate for this server. Paste its ca.crt when adding it (ask the operator)."}
}

// ConfirmCert records and installs the pending CA after the player compared
// fingerprints. Directory clusters additionally require the computed
// fingerprint to equal the directory's value (checked at select time too).
func (a *browserAPI) ConfirmCert() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil || len(a.active.caDER) == 0 {
		return map[string]any{"ok": false, "error": "Nothing to install."}
	}
	if err := installCA(a.active.caDER); err != nil || !caTrusted(a.active.caDER) {
		return map[string]any{"ok": false, "error": "The certificate was not installed. Windows asks you to confirm it -- choose Yes to continue."}
	}
	_ = a.trust.Remember(a.active.key, fingerprintHex(a.active.caDER))
	return map[string]any{"ok": true}
}

// accountTakenMessage is the one answer for every "already registered"
// path: directory pre-check, same-cluster 409, or roamed-account 409.
// Always names the way out (sign in), never which field collided.
const accountTakenMessage = "That callsign or email is already registered — sign in instead of creating a new account."

// Submit signs in (or registers then signs in) on the active cluster.
// Credentials are stored per cluster, never shared between clusters.
func (a *browserAPI) Submit(mode, username, identifier, password string) map[string]any {
	a.mu.Lock()
	active := a.active
	a.mu.Unlock()
	fail := func(msg string) map[string]any { return map[string]any{"ok": false, "error": msg} }
	if active == nil {
		return fail("Pick a cluster first.")
	}
	identifier, username = strings.TrimSpace(identifier), strings.TrimSpace(username)
	if identifier == "" || password == "" {
		return fail("Fill in every field.")
	}
	if mode == "register" {
		if username == "" {
			return fail("Pick a callsign.")
		}
		if len(password) < 6 {
			return fail("Use at least 6 characters for the password.")
		}
		// Global pre-check: is the name or address taken on ANY cluster?
		// Fail-open — an unreachable directory just yields to the cluster's
		// own 409 below, which also covers roamed accounts once pulled.
		a.mu.Lock()
		directory := strings.TrimSpace(a.cfg.Directory)
		a.mu.Unlock()
		if directory != "" {
			if taken, err := (&DirectoryClient{BaseURL: directory}).RegisterCheck(username, identifier); err == nil && taken {
				return fail(accountTakenMessage)
			}
		}
		if err := registerAccount(profileAuthURL, username, identifier, password); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "already registered") {
				return fail(accountTakenMessage)
			}
			return fail(capitalise(err.Error()))
		}
	}
	creds, err := loginAccount(profileAuthURL, identifier, password)
	if err != nil {
		return fail(capitalise(err.Error()))
	}
	if err := saveCredentials(active.key, creds); err != nil {
		fmt.Printf("[!] Could not remember this sign-in (%v)\n", err)
	}
	a.mu.Lock()
	a.token, a.username, a.userID = creds.Token, creds.Username, creds.UserID
	a.mu.Unlock()
	return map[string]any{"ok": true, "username": creds.Username}
}

func (a *browserAPI) SignOut() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active != nil {
		clearCredentials(a.active.key)
	}
	a.token, a.username, a.userID = "", "", ""
}

// SignOutAll forgets every saved sign-in on this machine (all clusters).
func (a *browserAPI) SignOutAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	appData := os.Getenv("LOCALAPPDATA")
	if appData == "" {
		appData = os.TempDir()
	}
	entries, err := os.ReadDir(filepath.Join(appData, "DreadnoughtPS"))
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "account-") && strings.HasSuffix(name, ".json") {
			_ = os.Remove(filepath.Join(appData, "DreadnoughtPS", name))
		}
	}
	a.token, a.username, a.userID = "", "", ""
}

// CheckPresence asks the directory whether this account is already in a
// match on another cluster. checked=false means "unknown" (no directory, not
// signed in, directory unreachable) — the page then lets the player through,
// because a dead directory must not strand anyone. Only checked + inMatch
// blocks. Hand-added servers exclude by name (they have no directory id).
func (a *browserAPI) CheckPresence() map[string]any {
	a.mu.Lock()
	active, token, userID := a.active, a.token, a.userID
	directory := strings.TrimSpace(a.cfg.Directory)
	a.mu.Unlock()
	if active == nil || token == "" || browserTokenExpired(token) || userID == "" || directory == "" {
		return map[string]any{"checked": false}
	}
	except := active.id
	if except == "" {
		except = active.name
	}
	inMatch, cluster, err := (&DirectoryClient{BaseURL: directory}).Presence(userID, except)
	if err != nil {
		return map[string]any{"checked": false, "error": err.Error()}
	}
	return map[string]any{"checked": true, "inMatch": inMatch, "cluster": cluster}
}

// presenceBlockMessage is the one-account-one-match rule, shown when the
// directory reports this account mid-match elsewhere.
func presenceBlockMessage(cluster string) string {
	if strings.TrimSpace(cluster) == "" {
		cluster = "another server"
	}
	return "You're already connected to a match on '" + cluster + "'. " +
		"Finish or leave it there first — one account can only be in one match at a time."
}

// ClusterNews fetches the active cluster's launcher tiles through the
// cluster-bound transport (Host-header routing does the rest).
func (a *browserAPI) ClusterNews() map[string]any {
	tiles, err := fetchNews()
	if err != nil {
		return map[string]any{"online": false, "error": err.Error()}
	}
	return map[string]any{"online": true, "tiles": tiles}
}

// PickGame shows Windows' folder picker. The choice is shared with
// dn-launcher (same settings file), so choosing once counts for both.
func (a *browserAPI) PickGame() map[string]any {
	var owner uintptr
	if a.owner != nil {
		owner = a.owner()
	}
	dir, ok := pickFolder(owner, "Select the folder where Dreadnought is installed")
	if !ok {
		return map[string]any{"ok": false}
	}
	if gameBinaryIn(dir) == "" {
		return map[string]any{"ok": false, "error": "Dreadnought was not found in " + dir +
			". Pick the folder that contains the DreadGame folder."}
	}
	return a.setGameDir(dir)
}

func (a *browserAPI) SetGameDir(dir string) map[string]any {
	dir = strings.Trim(strings.TrimSpace(dir), `"`)
	if gameBinaryIn(dir) == "" {
		return map[string]any{"ok": false, "error": "Dreadnought was not found in " + dir +
			". Enter the folder that contains the DreadGame folder."}
	}
	return a.setGameDir(dir)
}

func (a *browserAPI) setGameDir(dir string) map[string]any {
	settings := loadSettings()
	settings.GameDir = dir
	if err := saveSettings(settings); err != nil {
		return map[string]any{"ok": false, "error": "Could not save the setting: " + err.Error()}
	}
	return map[string]any{"ok": true, "game": gameInfo(a.exeDir)}
}

func (a *browserAPI) AutoGame() map[string]any {
	settings := loadSettings()
	settings.GameDir = ""
	if err := saveSettings(settings); err != nil {
		return map[string]any{"ok": false, "error": "Could not save the setting: " + err.Error()}
	}
	return map[string]any{"ok": true, "game": gameInfo(a.exeDir)}
}

func (a *browserAPI) SetOption(name string, on bool) bool {
	settings := loadSettings()
	switch name {
	case "logWindow":
		settings.LogWindow = on
	case "verboseLog":
		settings.VerboseLog = on
	default:
		return false
	}
	return saveSettings(settings) == nil
}

// gameInfo is the install folder the browser will use and how it was found.
func gameInfo(exeDir string) map[string]any {
	p, source := gameLocation(exeDir)
	if p == "" {
		return map[string]any{"path": "", "source": ""}
	}
	return map[string]any{"path": gameInstallRoot(p), "source": source}
}

// Play starts the game on the active cluster. ok means the game was started
// and the front end should close.
func (a *browserAPI) Play() map[string]any {
	a.mu.Lock()
	active, token := a.active, a.token
	a.mu.Unlock()
	if active == nil {
		return map[string]any{"ok": false, "error": "Pick a cluster first."}
	}
	switch cert := a.certState(); cert["state"] {
	case "trusted", "public":
	case "pending":
		return map[string]any{"ok": false, "cert": true,
			"error": "Confirm the cluster certificate first; the game cannot connect without it."}
	default:
		return map[string]any{"ok": false, "cert": true,
			"error": "This server has no certificate for the game to trust. Ask its operator for ca.crt."}
	}
	if findGameBinary(a.exeDir) == "" {
		return map[string]any{"ok": false, "game": true,
			"error": "Dreadnought was not found. Choose your game folder above."}
	}
	if token == "" || browserTokenExpired(token) {
		return map[string]any{"ok": false, "error": "Your sign-in has expired. Please sign in again.", "signIn": true}
	}
	// Fresh presence check at launch moment: the home screen polls, but the
	// match could have started since. Blocked launches stay on the page with
	// the reason (and a way to re-check), they never start the game.
	if p := a.CheckPresence(); p["checked"] == true && p["inMatch"] == true {
		cluster, _ := p["cluster"].(string)
		return map[string]any{"ok": false, "blocked": true, "error": presenceBlockMessage(cluster)}
	}
	settings := loadSettings()
	cfg := launchConfig{
		gatewayIP:     active.ip,
		gatewayPort:   "65443",
		firmamentHost: active.ip,
		firmamentPort: "48843",
		logWindow:     settings.LogWindow,
		verboseLog:    settings.VerboseLog,
	}
	if v := strings.TrimSpace(os.Getenv("DN_ALLOW_STEAM")); v != "" && v != "0" {
		cfg.allowSteam = true
	}
	if _, err := startGame(a.exeDir, cfg, token); err != nil {
		return map[string]any{"ok": false, "error": capitalise(err.Error())}
	}
	return map[string]any{"ok": true}
}

// OpenLogs opens the folder holding the game's newest log in Explorer.
func (a *browserAPI) OpenLogs() map[string]any {
	best := newestLogFolder(a.exeDir)
	if best == "" {
		return map[string]any{"ok": false, "error": "No game logs yet. Start the game once, then try again."}
	}
	verb, _ := windows.UTF16PtrFromString("open")
	target, _ := windows.UTF16PtrFromString(best)
	if err := windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		return map[string]any{"ok": false, "error": "Could not open " + best + ": " + err.Error()}
	}
	return map[string]any{"ok": true, "path": best}
}

func newestLogFolder(exeDir string) string {
	var dirs []string
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		dirs = append(dirs, filepath.Join(la, "DreadGame", "Saved", "Logs"))
	}
	if exe := findGameBinary(exeDir); exe != "" {
		dirs = append(dirs, filepath.Join(gameInstallRoot(exe), "DreadGame", "Saved", "Logs"))
	}
	best, bestTime := "", time.Time{}
	for _, dir := range dirs {
		info, err := os.Stat(filepath.Join(dir, "DreadGame.log"))
		if err == nil && info.ModTime().After(bestTime) {
			best, bestTime = dir, info.ModTime()
		} else if best == "" {
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				best = dir
			}
		}
	}
	return best
}
