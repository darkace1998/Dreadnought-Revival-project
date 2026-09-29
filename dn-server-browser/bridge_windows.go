//go:build windows

package main

import "strings"

// Small bridge adapters: shapes the browserAPI results for the page's reply
// callbacks.

// Init is the page boot state: game folder and options. Sign-in comes later,
// per selected cluster.
func (a *browserAPI) Init() map[string]any {
	return map[string]any{"ok": true,
		"game": gameInfo(a.exeDir), "logWindow": loadSettings().LogWindow, "verboseLog": loadSettings().VerboseLog}
}

// DirectoryUI returns the cluster list for the page.
func (a *browserAPI) DirectoryUI() map[string]any {
	return a.RefreshDirectory()
}

// SetDirectoryUI changes the directory URL (operator default baked in, user
// override saved). Fetching happens separately via DirectoryUI/refresh, so
// this stays fast enough for the UI thread.
func (a *browserAPI) SetDirectoryUI(url string) map[string]any {
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
	return map[string]any{"ok": true}
}

// AddManualUI adds a hand-entered server; the result feeds enterCluster.
func (a *browserAPI) AddManualUI(name, url, ca string) map[string]any {
	return a.AddManual(name, url, ca)
}

// RemoveManualUI forgets a hand-added server and returns the fresh list.
func (a *browserAPI) RemoveManualUI(url string) map[string]any {
	a.RemoveManual(url)
	return a.RefreshDirectory()
}

// SelectManual activates a previously added manual server.
func (a *browserAPI) SelectManual(url string) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	var found *manualServer
	for i, m := range a.cfg.Manual {
		if m.WebURL == url {
			found = &a.cfg.Manual[i]
			break
		}
	}
	if found == nil {
		return map[string]any{"ok": false, "error": "Unknown server. Add it again."}
	}
	host, port, err := webSplit(found.WebURL)
	if err != nil {
		return map[string]any{"ok": false, "error": "The stored address is invalid."}
	}
	ip, err := resolveServerIP(host)
	if err != nil {
		return map[string]any{"ok": false, "error": "Cannot reach " + host + ": " + err.Error()}
	}
	der, err := parseCAPEM([]byte(found.CACert))
	if err != nil {
		return map[string]any{"ok": false, "error": "The stored certificate is invalid."}
	}
	key := clusterTrustKey("", found.WebURL)
	if _, err := selectClusterTransport(ip, port, der); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	a.active = &activeCluster{name: found.Name, webURL: found.WebURL, ip: ip,
		webPort: port, caDER: der, key: key}
	a.pending = nil
	a.token, a.username, a.userID = "", "", ""
	if creds, ok := loadCredentials(key); ok && !browserTokenExpired(creds.Token) {
		a.token, a.username, a.userID = creds.Token, creds.Username, creds.UserID
	}
	return a.activeView()
}
