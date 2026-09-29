//go:build windows

package main

// The browser in the default web browser: used when the WebView2 runtime is
// missing or with --console. It serves the desktop window's page
// (browserPageHTML) with a small bridge in front of it, so the browser gets
// the same screens through the same browserAPI.
//
// Loopback only, on a port the OS picks. Every API call must carry a random
// per-run key in a custom header: a custom header forces a CORS preflight
// that this server never answers, so no other web page open in the browser
// can drive the browser.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const browserBridgeJS = `<script>
  window.dnBrowser = true;
  const DN_KEY = "%KEY%";
  async function dnCall(name, args) {
    const r = await fetch('/api/' + name, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-DN-Key': DN_KEY },
      body: JSON.stringify(args || []),
    });
    if (!r.ok) throw new Error('the browser answered HTTP ' + r.status);
    return r.json();
  }
  window.dnInit = () => dnCall('init');
  window.dnRefreshDir = () => { dnCall('directory').then(dnDirectoryResult, e => say('dirmsg', String(e), 'bad')); };
  window.dnSetDirectory = (u) => { dnCall('setDirectory', [u]).then(dnSetDirectoryResult, e => say('dirmsg', String(e), 'bad')); };
  window.dnSelectCluster = (id) => { dnCall('selectCluster', [id]).then(dnEnterCluster, e => say('dirmsg', String(e), 'bad')); };
  window.dnSelectManual = (url) => { dnCall('selectManual', [url]).then(dnEnterCluster, e => say('dirmsg', String(e), 'bad')); };
  window.dnAddManual = (n, u, c) => { dnCall('addManual', [n, u, c]).then(dnAddManualResult, e => say('mmsg', String(e), 'bad')); };
  window.dnRemoveManual = (u) => dnCall('removeManual', [u]);
  window.dnConfirmCert = () => { dnCall('confirmCert').then(dnConfirmCertResult, e => say('certmsg', String(e), 'bad')); };
  window.dnPickGame = () => dnCall('pickGame');
  window.dnSetOption = (name, on) => dnCall('setOption', [name, on]);
  window.dnSignOut = () => dnCall('signOut');
  window.dnOpenLogs = () => dnCall('openLogs');
  window.dnSubmit = (m, u, i, p) => { dnCall('submit', [m, u, i, p]).then(dnAuthResult, e => dnAuthResult({ ok: false, error: String(e) })); };
  window.dnNews = () => { dnCall('news').then(dnNewsResult, e => dnNewsResult({ online: false, error: String(e) })); };
  window.dnCheckPresence = () => { dnCall('checkPresence').then(dnPresenceResult, e => dnPresenceResult({ checked: false })); };
  window.dnPlay = () => {
    dnCall('play').then(r => {
      dnPlayResult(r);
      if (r.ok) setTimeout(() => { document.querySelector('main').innerHTML =
        '<p style="margin:auto;color:#8fe0a8">Dreadnought is starting. You can close this tab.</p>'; }, 2500);
    }, e => dnPlayResult({ ok: false, error: String(e) }));
  };
  // Tells the browser the page is still open; it exits once nobody is.
  setInterval(() => dnCall('ping').catch(() => {}), 10000);
</script>
`

// browserIdleExit is how long the browser waits after the page stopped
// pinging (tab closed) before it exits.
const browserIdleExit = 90 * time.Second

func runConsoleBrowser(exeDir string, api *browserAPI) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("open the browser page: %w", err)
	}
	defer func() { _ = listener.Close() }()

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return err
	}
	key := hex.EncodeToString(keyBytes)
	page := strings.Replace(browserPageHTML, "</title>",
		"</title>\n"+strings.Replace(browserBridgeJS, "%KEY%", key, 1), 1)

	done := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(done) }) }

	var mu sync.Mutex
	var lastSeen time.Time

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-DN-Key") != key {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mu.Lock()
		lastSeen = time.Now()
		mu.Unlock()
		var args []json.RawMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&args); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		str := func(i int) string {
			var v string
			if i < len(args) {
				_ = json.Unmarshal(args[i], &v)
			}
			return v
		}
		boolean := func(i int) bool {
			var v bool
			if i < len(args) {
				_ = json.Unmarshal(args[i], &v)
			}
			return v
		}
		var out any
		switch strings.TrimPrefix(r.URL.Path, "/api/") {
		case "init":
			out = api.Init()
		case "directory":
			out = api.DirectoryUI()
		case "setDirectory":
			out = api.SetDirectoryUI(str(0))
		case "selectCluster":
			out = api.SelectCluster(str(0))
		case "selectManual":
			out = api.SelectManual(str(0))
		case "addManual":
			out = api.AddManualUI(str(0), str(1), str(2))
		case "removeManual":
			out = api.RemoveManualUI(str(0))
		case "confirmCert":
			out = api.ConfirmCert()
		case "pickGame":
			out = api.PickGame()
		case "setOption":
			out = api.SetOption(str(0), boolean(1))
		case "signOut":
			api.SignOut()
			out = true
		case "submit":
			out = api.Submit(str(0), str(1), str(2), str(3))
		case "news":
			out = api.ClusterNews()
		case "checkPresence":
			out = api.CheckPresence()
		case "openLogs":
			out = api.OpenLogs()
		case "play":
			res := api.Play()
			if res["ok"] == true {
				time.AfterFunc(3*time.Second, finish)
			}
			out = res
		case "ping":
			out = true
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			serveErr <- err
		}
	}()

	address := fmt.Sprintf("http://127.0.0.1:%d/", listener.Addr().(*net.TCPAddr).Port)
	fmt.Printf("[*] Server browser page: %s\n", address)
	fmt.Println("    (open it yourself if no browser appears; this window can stay open)")
	openBrowser(address)

	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	var runErr error
wait:
	for {
		select {
		case <-done:
			break wait
		case runErr = <-serveErr:
			break wait
		case <-tick.C:
			mu.Lock()
			idle := !lastSeen.IsZero() && time.Since(lastSeen) > browserIdleExit
			mu.Unlock()
			if idle {
				fmt.Println("[*] The browser page was closed; exiting.")
				break wait
			}
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	return runErr
}
