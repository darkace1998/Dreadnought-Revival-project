//go:build windows

package main

// The launcher in the default web browser: used when the WebView2 runtime is
// missing (including under Wine, where dn-launcher-linux.sh runs it) or with
// --console. It serves the desktop window's page (desktopPageHTML) with a
// small bridge in front of it, so the browser gets the same screens -- sign-in,
// the certificate, the game folder, the options, news and Play -- through the
// same launcherAPI.
//
// Loopback only, on a port the OS picks. Every API call must carry a random
// per-run key in a custom header: a custom header forces a CORS preflight that
// this server never answers, so no other web page open in the browser can
// drive the launcher.

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
    if (!r.ok) throw new Error('the launcher answered HTTP ' + r.status);
    return r.json();
  }
  window.dnInit = () => dnCall('init');
  window.dnPickGame = () => dnCall('pickGame');
  window.dnSetGameDir = (dir) => dnCall('setGameDir', [dir]);
  window.dnAutoGame = () => dnCall('autoGame');
  window.dnSetOption = (name, on) => dnCall('setOption', [name, on]);
  window.dnInstallCert = () => dnCall('installCert');
  window.dnSignOut = () => dnCall('signOut');
  window.dnOpenLogs = () => dnCall('openLogs');
  window.dnSubmit = (m, u, i, p) => { dnCall('submit', [m, u, i, p]).then(dnAuthResult, e => dnAuthResult({ ok: false, error: String(e) })); };
  window.dnNews = () => { dnCall('news').then(dnNewsResult, e => dnNewsResult({ online: false, error: String(e) })); };
  window.dnPlay = () => {
    dnCall('play').then(r => {
      dnPlayResult(r);
      if (r.ok) setTimeout(() => { document.querySelector('main').innerHTML =
        '<p style="margin:auto;color:#8fe0a8">Dreadnought is starting. You can close this tab.</p>'; }, 2500);
    }, e => dnPlayResult({ ok: false, error: String(e) }));
  };
  // Tells the launcher the page is still open; it exits once nobody is.
  setInterval(() => dnCall('ping').catch(() => {}), 10000);
</script>
`

// browserIdleExit is how long the launcher waits after the page stopped
// pinging (tab closed) before it exits.
const browserIdleExit = 90 * time.Second

func runBrowserLauncher(exeDir string, cfg Config) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("open the launcher page: %w", err)
	}
	defer func() { _ = listener.Close() }()

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return err
	}
	key := hex.EncodeToString(keyBytes)
	page := strings.Replace(desktopPageHTML, "</title>",
		"</title>\n"+strings.Replace(browserBridgeJS, "%KEY%", key, 1), 1)

	api := newLauncherAPI(exeDir, cfg, nil)
	done := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(done) }) }

	var mu sync.Mutex
	var lastSeen time.Time // zero until the page first loads

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
		case "pickGame":
			out = api.PickGame()
		case "setGameDir":
			out = api.SetGameDir(str(0))
		case "autoGame":
			out = api.AutoGame()
		case "setOption":
			out = api.SetOption(str(0), boolean(1))
		case "installCert":
			out = api.InstallCert()
		case "signOut":
			api.SignOut()
			out = true
		case "submit":
			out = api.Submit(str(0), str(1), str(2), str(3))
		case "news":
			out = api.News()
		case "openLogs":
			out = api.OpenLogs()
		case "play":
			res := api.Play()
			if res["ok"] == true {
				// Let the page show the result before the server goes away.
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
	fmt.Printf("[*] Launcher page: %s\n", address)
	fmt.Println("    (open it yourself if no browser appears; this window can stay open)")
	openBrowser(address)

	// Exit once the game is started, or once the page has been closed.
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
				fmt.Println("[*] The launcher page was closed; exiting.")
				break wait
			}
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	return runErr
}
