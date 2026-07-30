//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// The sign-in window.
//
// The retail launcher was a Chromium (CEF) shell rendering an HTML page, so a
// browser-based screen is the closest thing to it that does not drag a UI
// toolkit into a single-file Go executable. The launcher binds a loopback-only
// listener on a port the OS picks, opens the default browser at it, and serves
// one page. Nothing is reachable from off the machine and the server stops the
// moment sign-in completes.

const signInPageHTML = `<!doctype html>
<meta charset="utf-8">
<title>Dreadnought — Sign In</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh; display: grid; place-items: center;
    font: 15px/1.5 "Segoe UI", system-ui, sans-serif; color: #d8e2ea;
    background: radial-gradient(120% 90% at 50% 0%, #16283a 0%, #0a1119 60%, #060a0f 100%);
  }
  .panel {
    width: min(400px, 92vw); padding: 32px 30px 26px;
    background: rgba(12, 20, 30, .82); border: 1px solid #21384d;
    border-radius: 10px; box-shadow: 0 18px 50px rgba(0,0,0,.55);
  }
  h1 { margin: 0 0 2px; font-size: 20px; letter-spacing: .16em; text-transform: uppercase; color: #6fd3ff; }
  .sub { margin: 0 0 22px; font-size: 12.5px; color: #7f93a5; letter-spacing: .04em; }
  .tabs { display: flex; gap: 6px; margin-bottom: 20px; }
  .tabs button {
    flex: 1; padding: 9px 0; font: inherit; font-size: 13px; cursor: pointer;
    background: transparent; color: #7f93a5; border: 1px solid #21384d; border-radius: 6px;
  }
  .tabs button[aria-selected="true"] { background: #12293c; color: #9fe4ff; border-color: #2f5a78; }
  label { display: block; margin: 12px 0 5px; font-size: 12px; color: #90a5b7; letter-spacing: .05em; }
  input {
    width: 100%; padding: 10px 12px; font: inherit; color: #e6eef5;
    background: #0c1621; border: 1px solid #24405a; border-radius: 6px;
  }
  input:focus { outline: none; border-color: #3d7fa8; box-shadow: 0 0 0 3px rgba(61,127,168,.18); }
  .go {
    width: 100%; margin-top: 22px; padding: 12px 0; font: inherit; font-weight: 600;
    letter-spacing: .1em; text-transform: uppercase; cursor: pointer; color: #04121c;
    background: linear-gradient(180deg, #7fd8ff, #3ba7d8); border: 0; border-radius: 6px;
  }
  .go:disabled { opacity: .55; cursor: default; }
  .msg { margin-top: 14px; min-height: 19px; font-size: 13px; }
  .msg.bad { color: #ff9b8f; }
  .msg.good { color: #8fe0a8; }
  .hint { margin-top: 16px; font-size: 11.5px; color: #63798c; }
</style>
<div class="panel">
  <h1>Dreadnought</h1>
  <p class="sub">Private server</p>
  <div class="tabs" role="tablist">
    <button role="tab" id="tab-login" aria-selected="true" onclick="pick('login')">Sign in</button>
    <button role="tab" id="tab-register" aria-selected="false" onclick="pick('register')">Create account</button>
  </div>
  <form id="form" onsubmit="submitForm(event)">
    <div id="username-row" hidden>
      <label for="username">Callsign</label>
      <input id="username" autocomplete="username" maxlength="32">
    </div>
    <label for="identifier" id="identifier-label">Email</label>
    <input id="identifier" type="text" autocomplete="email" autofocus>
    <label for="password">Password</label>
    <input id="password" type="password" autocomplete="current-password">
    <button class="go" id="go" type="submit">Sign in &amp; play</button>
  </form>
  <div class="msg" id="msg"></div>
  <p class="hint">Your account lives on the server, so your ships and progress follow you to any PC.</p>
</div>
<script>
  let mode = 'login';
  function pick(next) {
    mode = next;
    document.getElementById('tab-login').setAttribute('aria-selected', next === 'login');
    document.getElementById('tab-register').setAttribute('aria-selected', next === 'register');
    document.getElementById('username-row').hidden = next !== 'register';
    document.getElementById('identifier-label').textContent = next === 'register' ? 'Email' : 'Email or callsign';
    document.getElementById('go').textContent = next === 'register' ? 'Create account & play' : 'Sign in & play';
    say('', '');
  }
  function say(text, kind) {
    const el = document.getElementById('msg');
    el.textContent = text;
    el.className = 'msg ' + (kind || '');
  }
  async function submitForm(event) {
    event.preventDefault();
    const go = document.getElementById('go');
    go.disabled = true;
    say('Contacting the server...', '');
    try {
      const response = await fetch('/submit', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          mode: mode,
          username: document.getElementById('username').value.trim(),
          identifier: document.getElementById('identifier').value.trim(),
          password: document.getElementById('password').value
        })
      });
      const body = await response.json();
      if (!response.ok) { say(body.error || 'Sign-in failed.', 'bad'); go.disabled = false; return; }
      say('Signed in as ' + body.username + '. Launching...', 'good');
      go.disabled = true;
    } catch (err) {
      say('Could not reach the launcher: ' + err, 'bad');
      go.disabled = false;
    }
  }
  pick('login');
</script>
`

// runSignInUI serves the sign-in page until the user completes it, then returns
// the credentials. It listens on loopback only.
func runSignInUI(authURL string) (storedCredentials, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return storedCredentials{}, fmt.Errorf("open the sign-in window: %w", err)
	}
	defer func() { _ = listener.Close() }()

	type outcome struct {
		creds storedCredentials
		err   error
	}
	done := make(chan outcome, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(signInPageHTML))
	})
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Mode       string `json:"mode"`
			Username   string `json:"username"`
			Identifier string `json:"identifier"`
			Password   string `json:"password"`
		}
		w.Header().Set("Content-Type", "application/json")
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req) != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"Could not read the form."}`))
			return
		}

		fail := func(status int, message string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
		}

		req.Identifier = strings.TrimSpace(req.Identifier)
		req.Username = strings.TrimSpace(req.Username)
		if req.Identifier == "" || req.Password == "" {
			fail(http.StatusBadRequest, "Fill in every field.")
			return
		}

		if req.Mode == "register" {
			if req.Username == "" {
				fail(http.StatusBadRequest, "Pick a callsign.")
				return
			}
			if len(req.Password) < 6 {
				fail(http.StatusBadRequest, "Use at least 6 characters for the password.")
				return
			}
			if err := registerAccount(authURL, req.Username, req.Identifier, req.Password); err != nil {
				fail(http.StatusBadRequest, capitalise(err.Error()))
				return
			}
		}

		creds, err := loginAccount(authURL, req.Identifier, req.Password)
		if err != nil {
			fail(http.StatusUnauthorized, capitalise(err.Error()))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"username": creds.Username})
		done <- outcome{creds: creds}
	})

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			done <- outcome{err: serveErr}
		}
	}()

	address := fmt.Sprintf("http://127.0.0.1:%d/", listener.Addr().(*net.TCPAddr).Port)
	fmt.Printf("[*] Sign-in window: %s\n", address)
	openBrowser(address)

	result := <-done
	// Let the browser render the "launching" state before the socket closes.
	time.Sleep(400 * time.Millisecond)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	return result.creds, result.err
}

func openBrowser(address string) {
	//nolint:gosec // Fixed command with a loopback URL this process just built.
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	if err := cmd.Start(); err != nil {
		fmt.Printf("[!] Could not open a browser automatically. Open this address yourself:\n    %s\n", address)
	}
}

func capitalise(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}
