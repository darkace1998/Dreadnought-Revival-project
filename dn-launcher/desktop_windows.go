//go:build windows

package main

// The desktop launcher: one native window hosting the launcher's own HTML in
// Microsoft Edge WebView2 (built into Windows 11, installed with Edge on most
// Windows 10 PCs). Sign-in, account creation, news and PLAY all happen in it --
// no separate browser. Without the WebView2 runtime, runDesktopLauncher
// returns false and the launcher falls back to the console + browser flow.
//
// Threading: WebView2 calls bound functions ON THE UI THREAD, so every one of
// them that touches the network starts a goroutine and reports back through
// w.Dispatch(w.Eval(...)); a blocking call would freeze the window.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// newsURL is the original launcher's tiles feed, still served by legacy-api
// (public route /v2/dreadnought/launcher/dn/tiles/{lang}/).
const newsURL = "https://legacyapi.prod.greybox.sixfoot.live/v2/dreadnought/launcher/dn/tiles/en/"

type desktopState struct {
	mu       sync.Mutex
	token    string
	username string
}

// runDesktopLauncher shows the launcher window and returns once it is closed.
// false means WebView2 is not available and nothing was shown.
func runDesktopLauncher(exeDir string, cfg Config) bool {
	if v := webView2RuntimeVersion(); v == "" {
		return false
	} else {
		fmt.Printf("[*] WebView2 runtime %s\n", v)
	}
	dataDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "DreadnoughtPrivateServer", "WebView2")
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		DataPath:  dataDir,
		WindowOptions: webview2.WindowOptions{
			Title:  "Dreadnought — Private Server",
			Width:  980,
			Height: 620,
			Center: true,
		},
	})
	if w == nil {
		return false
	}
	defer w.Destroy()
	hideConsole()

	st := &desktopState{}
	if creds, ok := loadCredentials(); ok && !signOutRequested() && !launcherTokenExpired(creds.Token) {
		st.token, st.username = creds.Token, creds.Username
	}

	// reply runs js on the UI thread with a JSON argument.
	reply := func(fn string, v any) {
		b, _ := json.Marshal(v)
		w.Dispatch(func() { w.Eval(fn + "(" + string(b) + ")") })
	}

	_ = w.Bind("dnInit", func() map[string]any {
		st.mu.Lock()
		defer st.mu.Unlock()
		server := cfg.Server
		if server == "" {
			server = cfg.GatewayIP
		}
		return map[string]any{"signedIn": st.token != "", "username": st.username, "server": server}
	})

	_ = w.Bind("dnSubmit", func(mode, username, identifier, password string) {
		go func() {
			identifier, username = strings.TrimSpace(identifier), strings.TrimSpace(username)
			fail := func(msg string) { reply("dnAuthResult", map[string]any{"ok": false, "error": msg}) }
			if identifier == "" || password == "" {
				fail("Fill in every field.")
				return
			}
			if mode == "register" {
				if username == "" {
					fail("Pick a callsign.")
					return
				}
				if len(password) < 6 {
					fail("Use at least 6 characters for the password.")
					return
				}
				if err := registerAccount(cfg.AuthURL, username, identifier, password); err != nil {
					fail(capitalise(err.Error()))
					return
				}
			}
			creds, err := loginAccount(cfg.AuthURL, identifier, password)
			if err != nil {
				fail(capitalise(err.Error()))
				return
			}
			if err := saveCredentials(creds); err != nil {
				fmt.Printf("[!] Could not remember this sign-in (%v)\n", err)
			}
			st.mu.Lock()
			st.token, st.username = creds.Token, creds.Username
			st.mu.Unlock()
			reply("dnAuthResult", map[string]any{"ok": true, "username": creds.Username})
		}()
	})

	_ = w.Bind("dnSignOut", func() {
		clearCredentials()
		st.mu.Lock()
		st.token, st.username = "", ""
		st.mu.Unlock()
	})

	_ = w.Bind("dnNews", func() {
		go func() {
			tiles, err := fetchNews()
			if err != nil {
				reply("dnNewsResult", map[string]any{"online": false, "error": err.Error()})
				return
			}
			reply("dnNewsResult", map[string]any{"online": true, "tiles": tiles})
		}()
	})

	_ = w.Bind("dnPlay", func() {
		go func() {
			st.mu.Lock()
			token := st.token
			st.mu.Unlock()
			if token == "" || launcherTokenExpired(token) {
				reply("dnPlayResult", map[string]any{"ok": false, "error": "Your sign-in has expired. Please sign in again.", "signIn": true})
				return
			}
			if _, err := startGame(exeDir, cfg, token); err != nil {
				reply("dnPlayResult", map[string]any{"ok": false, "error": capitalise(err.Error())})
				return
			}
			reply("dnPlayResult", map[string]any{"ok": true})
			time.Sleep(2500 * time.Millisecond)
			w.Dispatch(w.Terminate)
		}()
	})

	w.SetHtml(desktopPageHTML)
	w.Run()
	return true
}

type newsTile struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Active bool   `json:"active"`
	Size   string `json:"section_size"`
}

func fetchNews() ([]newsTile, error) {
	resp, err := launcherHTTPClient().Get(newsURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("news: HTTP %d", resp.StatusCode)
	}
	var doc struct {
		Result struct {
			Tiles []newsTile `json:"tiles"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("news: %w", err)
	}
	var out []newsTile
	for _, t := range doc.Result.Tiles {
		if t.Active {
			out = append(out, t)
		}
	}
	return out, nil
}

// hideConsole detaches the console the launcher was started with, so the
// desktop window stands alone. A launcher started from a terminal just stops
// printing there.
func hideConsole() {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("FreeConsole")
	_, _, _ = proc.Call()
}

const desktopPageHTML = `<!doctype html>
<meta charset="utf-8">
<title>Dreadnought</title>
<style>
  :root { color-scheme: dark; --line:#21384d; --dim:#7f93a5; --accent:#6fd3ff; }
  * { box-sizing: border-box; }
  html, body { margin:0; height:100%; }
  body {
    font: 15px/1.5 "Segoe UI", system-ui, sans-serif; color:#d8e2ea; user-select:none;
    background: radial-gradient(120% 90% at 50% 0%, #16283a 0%, #0a1119 60%, #060a0f 100%);
    display:flex; flex-direction:column;
  }
  header { display:flex; align-items:center; gap:14px; padding:16px 26px; border-bottom:1px solid var(--line); }
  header h1 { margin:0; font-size:20px; letter-spacing:.2em; text-transform:uppercase; color:var(--accent); }
  header .sub { color:var(--dim); font-size:12.5px; letter-spacing:.06em; }
  header .spacer { flex:1; }
  .pill { font-size:12px; padding:3px 10px; border-radius:99px; border:1px solid var(--line); color:var(--dim); }
  .pill.on { color:#8fe0a8; border-color:#2e5a3f; }
  .pill.off { color:#ff9b8f; border-color:#5a2e2e; }
  main { flex:1; display:flex; min-height:0; }
  .view { flex:1; display:none; min-height:0; }
  .view.shown { display:flex; }
  /* sign-in */
  #signin { align-items:center; justify-content:center; }
  .panel { width:min(400px,92vw); padding:28px 28px 22px; background:rgba(12,20,30,.82);
    border:1px solid var(--line); border-radius:10px; box-shadow:0 18px 50px rgba(0,0,0,.55); }
  .tabs { display:flex; gap:6px; margin-bottom:16px; }
  .tabs button { flex:1; padding:9px 0; font:inherit; font-size:13px; cursor:pointer; background:transparent;
    color:var(--dim); border:1px solid var(--line); border-radius:6px; }
  .tabs button[aria-selected="true"] { background:#12293c; color:#9fe4ff; border-color:#2f5a78; }
  label { display:block; margin:12px 0 5px; font-size:12px; color:#90a5b7; letter-spacing:.05em; }
  input { width:100%; padding:10px 12px; font:inherit; color:#e6eef5; background:#0c1621;
    border:1px solid #24405a; border-radius:6px; user-select:text; }
  input:focus { outline:none; border-color:#3d7fa8; box-shadow:0 0 0 3px rgba(61,127,168,.18); }
  .go { width:100%; margin-top:20px; padding:12px 0; font:inherit; font-weight:600; letter-spacing:.1em;
    text-transform:uppercase; cursor:pointer; color:#04121c; background:linear-gradient(180deg,#7fd8ff,#3ba7d8);
    border:0; border-radius:6px; }
  .go:disabled { opacity:.55; cursor:default; }
  .msg { margin-top:12px; min-height:19px; font-size:13px; }
  .msg.bad { color:#ff9b8f; } .msg.good { color:#8fe0a8; }
  /* home */
  #home { flex-direction:row; }
  .news { flex:1; overflow:auto; padding:22px 26px; display:grid; grid-template-columns:1fr 1fr; gap:14px; align-content:start; }
  .tile { background:rgba(12,20,30,.82); border:1px solid var(--line); border-radius:8px; padding:16px 18px; }
  .tile.full { grid-column:1 / -1; }
  .tile h3 { margin:0 0 6px; font-size:15px; color:#9fe4ff; letter-spacing:.04em; }
  .tile p { margin:0; color:#b8c7d3; font-size:13.5px; white-space:pre-wrap; }
  aside { width:300px; border-left:1px solid var(--line); padding:26px 24px; display:flex; flex-direction:column; gap:10px; }
  aside .who { font-size:12px; color:var(--dim); letter-spacing:.08em; text-transform:uppercase; }
  aside .name { font-size:22px; color:#e6eef5; margin-top:-6px; word-break:break-all; }
  aside .spacer { flex:1; }
  .play { padding:18px 0; font-size:20px; }
  .link { background:none; border:0; color:var(--dim); font:inherit; font-size:12.5px; cursor:pointer; text-decoration:underline; padding:0; align-self:flex-start; }
</style>
<header>
  <h1>Dreadnought</h1><span class="sub">Private server</span>
  <span class="spacer"></span>
  <span class="pill" id="status">Connecting…</span>
</header>
<main>
  <section class="view" id="signin">
    <div class="panel">
      <div class="tabs" role="tablist">
        <button role="tab" id="tab-login" aria-selected="true" onclick="pick('login')">Sign in</button>
        <button role="tab" id="tab-register" aria-selected="false" onclick="pick('register')">Create account</button>
      </div>
      <form id="form" onsubmit="submitForm(event)">
        <div id="username-row" hidden>
          <label for="username">Callsign</label>
          <input id="username" autocomplete="username" maxlength="32">
        </div>
        <label for="identifier" id="identifier-label">Email or callsign</label>
        <input id="identifier" type="text" autocomplete="email">
        <label for="password">Password</label>
        <input id="password" type="password" autocomplete="current-password">
        <button class="go" id="go" type="submit">Sign in</button>
      </form>
      <div class="msg" id="msg"></div>
    </div>
  </section>
  <section class="view" id="home">
    <div class="news" id="news"></div>
    <aside>
      <span class="who">Captain</span>
      <span class="name" id="name"></span>
      <button class="link" onclick="signOut()">Sign out</button>
      <span class="spacer"></span>
      <div class="msg" id="playmsg"></div>
      <button class="go play" id="play" onclick="play()">Play</button>
    </aside>
  </section>
</main>
<script>
  let mode = 'login';
  const $ = id => document.getElementById(id);
  function show(view) {
    for (const v of ['signin', 'home']) $(v).classList.toggle('shown', v === view);
    if (view === 'signin') setTimeout(() => $(mode === 'register' ? 'username' : 'identifier').focus(), 50);
  }
  function say(el, text, kind) { $(el).textContent = text; $(el).className = 'msg ' + (kind || ''); }
  function pick(next) {
    mode = next;
    $('tab-login').setAttribute('aria-selected', next === 'login');
    $('tab-register').setAttribute('aria-selected', next === 'register');
    $('username-row').hidden = next !== 'register';
    $('identifier-label').textContent = next === 'register' ? 'Email' : 'Email or callsign';
    $('go').textContent = next === 'register' ? 'Create account' : 'Sign in';
    say('msg', '');
  }
  function submitForm(e) {
    e.preventDefault();
    $('go').disabled = true;
    say('msg', 'Contacting the server…');
    dnSubmit(mode, $('username').value, $('identifier').value, $('password').value);
  }
  function dnAuthResult(r) {
    $('go').disabled = false;
    if (!r.ok) { say('msg', r.error || 'Sign-in failed.', 'bad'); return; }
    $('password').value = '';
    home(r.username);
  }
  function home(username) {
    $('name').textContent = username;
    say('playmsg', '');
    $('play').disabled = false;
    show('home');
  }
  function signOut() { dnSignOut(); pick('login'); show('signin'); }
  function play() {
    $('play').disabled = true;
    say('playmsg', 'Launching Dreadnought…');
    dnPlay();
  }
  function dnPlayResult(r) {
    if (r.ok) { say('playmsg', 'Game started. Good hunting, Captain.', 'good'); return; }
    say('playmsg', r.error, 'bad');
    $('play').disabled = false;
    if (r.signIn) signOut();
  }
  function dnNewsResult(r) {
    const s = $('status');
    s.textContent = r.online ? 'Server online' : 'Server unreachable';
    s.className = 'pill ' + (r.online ? 'on' : 'off');
    const news = $('news');
    news.textContent = '';
    for (const t of (r.tiles || [])) {
      const el = document.createElement('article');
      el.className = 'tile' + (t.section_size === 'full' ? ' full' : '');
      const h = document.createElement('h3'); h.textContent = t.title;
      const p = document.createElement('p'); p.textContent = t.body;
      el.append(h, p);
      news.append(el);
    }
    if (!r.online) {
      const el = document.createElement('article');
      el.className = 'tile full';
      el.innerHTML = '<h3>Cannot reach the server</h3><p></p>';
      el.querySelector('p').textContent = r.error || '';
      news.append(el);
    }
  }
  document.addEventListener('keydown', e => {
    if (e.key === 'Enter' && $('home').classList.contains('shown') && !$('play').disabled) play();
  });
  (async () => {
    pick('login');
    const s = await dnInit();
    if (s.signedIn) home(s.username); else show('signin');
    dnNews();
  })();
</script>
`

// webView2RuntimeVersion is Microsoft's documented detection for the Evergreen
// WebView2 runtime: a non-empty "pv" under the EdgeUpdate client key, machine-
// or per-user-wide. Checked BEFORE creating anything, because the binding's
// Embed waits for the environment's completion callback with no timeout -- a
// runtime that is absent or broken must not get that far (under Wine, which
// has none, it hung there).
func webView2RuntimeVersion() string {
	const client = `Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	for _, loc := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\` + client},
		{registry.LOCAL_MACHINE, `SOFTWARE\` + client},
		{registry.CURRENT_USER, `Software\` + client},
	} {
		k, err := registry.OpenKey(loc.root, loc.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("pv")
		_ = k.Close()
		if err == nil && v != "" && v != "0.0.0.0" {
			return v
		}
	}
	return ""
}
