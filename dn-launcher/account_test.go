//go:build windows

package main

import "testing"

// The config's auth_url points at the login route itself (".../auth/"), so the
// register endpoint has to be built from a trimmed base rather than appended to
// it -- otherwise it becomes ".../auth//register".
func TestAuthBaseURLTrimsTheTrailingSlash(t *testing.T) {
	for _, tc := range []struct{ in, wantRegister, wantLogin string }{
		{"https://host/auth/", "https://host/auth/register", "https://host/auth/"},
		{"https://host/auth", "https://host/auth/register", "https://host/auth/"},
		{"  https://host/auth/  ", "https://host/auth/register", "https://host/auth/"},
	} {
		base := authBaseURL(tc.in)
		if got := base + "/register"; got != tc.wantRegister {
			t.Errorf("register endpoint for %q = %q, want %q", tc.in, got, tc.wantRegister)
		}
		if got := base + "/"; got != tc.wantLogin {
			t.Errorf("login endpoint for %q = %q, want %q", tc.in, got, tc.wantLogin)
		}
	}
}

func TestFirstNonEmptyPicksTheFirstPopulatedToken(t *testing.T) {
	if got := firstNonEmpty("", "", "jwt-value", "later"); got != "jwt-value" {
		t.Errorf("firstNonEmpty = %q, want jwt-value", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty with nothing set = %q, want empty", got)
	}
}

// Sign-in failures are shown to the player, so the server's own wording is
// preferred over a generic message when it sends one.
func TestServerMessagePrefersTheServersWording(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"greybox error envelope", `{"error":{"code":-32006,"message":"Invalid username or password"}}`, "Invalid username or password"},
		{"plain message", `{"message":"username or email already taken"}`, "username or email already taken"},
		{"unfamiliar shape", `{"weird":true}`, "fallback text"},
		{"not json at all", `<html>502</html>`, "fallback text"},
	} {
		if got := serverMessage([]byte(tc.body), "fallback text"); got != tc.want {
			t.Errorf("%s: serverMessage = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCapitaliseLeavesEmptyAlone(t *testing.T) {
	if got := capitalise("incorrect email or password"); got != "Incorrect email or password" {
		t.Errorf("capitalise = %q", got)
	}
	if got := capitalise(""); got != "" {
		t.Errorf("capitalise(empty) = %q, want empty", got)
	}
}
