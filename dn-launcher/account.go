//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Account sign-in.
//
// The launcher used to have no notion of an account at all. It derived an id
// from the machine and user, hashed it into a fake Steam ticket, and the auth
// server auto-registered whatever it had not seen before. That works for one
// person on one machine and fails everywhere else: the id cannot be carried to
// another PC, two people on one PC share one save, and anything that perturbs
// the derivation silently strands the old account. auth.db has seven
// auto-generated player_XXXXXXXX rows from exactly that.
//
// A real account fixes it at the source: the server already stores a unique
// username and email with a bcrypt password, and issues the same JWT either
// way. The derived-identity path stays as a fallback so existing installs keep
// their account until they choose to sign in.

const credentialFileName = "account.json"

// storedCredentials is what persists between launches. The password is never
// kept -- only the refresh-able token and who it belongs to. The blob is
// DPAPI-encrypted, so it is readable only by this Windows user.
type storedCredentials struct {
	Identifier string `json:"identifier"` // email or username, for display and re-login
	Username   string `json:"username"`
	UserID     string `json:"user_id"`
	Token      string `json:"token"`
	IssuedAt   string `json:"issued_at"`
}

func credentialPath() (string, error) {
	appData := os.Getenv("LOCALAPPDATA")
	if appData == "" {
		appData = os.TempDir()
	}
	dir := filepath.Join(appData, "DreadnoughtPS")
	//nolint:gosec // Confined to this user's private launcher directory.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create account directory: %w", err)
	}
	return filepath.Join(dir, credentialFileName), nil
}

func loadCredentials() (storedCredentials, bool) {
	path, err := credentialPath()
	if err != nil {
		return storedCredentials{}, false
	}
	//nolint:gosec // Path is the launcher's own private credential file.
	blob, err := os.ReadFile(path)
	if err != nil {
		return storedCredentials{}, false
	}
	plain, err := dpapiDecrypt(blob)
	if err != nil {
		// Written by another Windows user, or the profile was rebuilt. Not an
		// error worth failing the launch over -- sign in again.
		return storedCredentials{}, false
	}
	var creds storedCredentials
	if json.Unmarshal(plain, &creds) != nil || creds.Token == "" {
		return storedCredentials{}, false
	}
	return creds, true
}

func saveCredentials(creds storedCredentials) error {
	path, err := credentialPath()
	if err != nil {
		return err
	}
	plain, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	blob, err := dpapiEncrypt(plain)
	if err != nil {
		return fmt.Errorf("DPAPI encrypt credentials: %w", err)
	}
	//nolint:gosec // 0600 under the user's own LOCALAPPDATA.
	return os.WriteFile(path, blob, 0o600)
}

func clearCredentials() {
	if path, err := credentialPath(); err == nil {
		_ = os.Remove(path)
	}
}

// authBaseURL turns the configured auth endpoint into a base we can hang
// /register off. The config points at ".../auth/", which is the login route
// itself, so trailing path elements are trimmed rather than appended to.
func authBaseURL(authURL string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(authURL), "/")
	return trimmed
}

func launcherHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: buildTLSConfig(),
		},
	}
}

// registerAccount creates an account. The server requires a username, an email
// and at least six characters of password, and answers 409 if either unique
// column is taken.
func registerAccount(authURL, username, email, password string) error {
	body, err := json.Marshal(map[string]string{
		"username": username,
		"email":    email,
		"password": password,
	})
	if err != nil {
		return fmt.Errorf("marshal registration: %w", err)
	}

	endpoint := authBaseURL(authURL) + "/register"
	resp, err := launcherHTTPClient().Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("contact %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode == http.StatusCreated:
		return nil
	case resp.StatusCode == http.StatusConflict:
		return fmt.Errorf("that username or email is already registered")
	case resp.StatusCode >= 400:
		return fmt.Errorf("%s", serverMessage(payload, fmt.Sprintf("registration failed (HTTP %d)", resp.StatusCode)))
	}
	return nil
}

// loginAccount exchanges an identifier and password for a JWT. The identifier
// may be the email or the username -- the server accepts either.
func loginAccount(authURL, identifier, password string) (storedCredentials, error) {
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", identifier)
	form.Set("password", password)

	endpoint := authBaseURL(authURL) + "/"
	resp, err := launcherHTTPClient().Post(endpoint, "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()))
	if err != nil {
		return storedCredentials{}, fmt.Errorf("contact %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return storedCredentials{}, fmt.Errorf("%s", serverMessage(payload, "incorrect email or password"))
	}

	// The password grant answers with the plain Greybox object rather than the
	// JSON-RPC envelope the Steam-ticket path uses.
	var user userObj
	if err := json.Unmarshal(payload, &user); err != nil {
		return storedCredentials{}, fmt.Errorf("could not read the sign-in response: %w", err)
	}
	token := firstNonEmpty(user.JWT, user.AccessToken, user.Token)
	if token == "" {
		// Fall back to the enveloped shape in case the server wrapped it.
		var enveloped authResponse
		if json.Unmarshal(payload, &enveloped) == nil && len(enveloped.Result) >= 2 {
			var inner userObj
			if json.Unmarshal(enveloped.Result[1], &inner) == nil {
				token = firstNonEmpty(inner.JWT, inner.AccessToken, inner.Token)
				user = inner
			}
		}
	}
	if token == "" {
		return storedCredentials{}, fmt.Errorf("the server did not return a session token")
	}

	return storedCredentials{
		Identifier: identifier,
		Username:   user.Username,
		UserID:     user.UserID,
		Token:      token,
		IssuedAt:   time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// serverMessage digs a human-readable reason out of an error body, falling back
// to the supplied default when the shape is unfamiliar.
func serverMessage(payload []byte, fallback string) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(payload, &body) == nil {
		if body.Error.Message != "" {
			return body.Error.Message
		}
		if body.Message != "" {
			return body.Message
		}
	}
	return fallback
}

// launcherTokenExpired reports whether a stored JWT is past (or within a minute
// of) its expiry.
//
// The launcher cannot VERIFY the token -- it has no signing secret -- but it can
// read the claim, and that is enough to decide whether reusing it is pointless.
// A malformed or claim-less token counts as expired: if we cannot tell when it
// dies, re-authenticating is the safe answer.
//
// This exists because the saved-credentials path reused creds.Token verbatim, so
// once that token aged out every launcher restart rewrote the SAME dead token to
// the registry. The game then failed with an opaque "Could not create session.
// Error Code: 401" and restarting the launcher -- the obvious remedy -- changed
// nothing.
func launcherTokenExpired(token string) bool {
	const skew = time.Minute

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return true
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return true
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return true
	}
	return time.Now().Add(skew).After(time.Unix(claims.Exp, 0))
}
