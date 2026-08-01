//go:build windows

package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func makeTestToken(t *testing.T, exp int64) string {
	t.Helper()
	payload := map[string]any{"sub": "player-1"}
	if exp != 0 {
		payload["exp"] = exp
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return strings.Join([]string{
		base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)),
		base64.RawURLEncoding.EncodeToString(body),
		base64.RawURLEncoding.EncodeToString([]byte("not-a-real-signature")),
	}, ".")
}

// A saved token is only worth replaying while it is alive. Reusing an expired
// one is what made every launcher restart rewrite the same dead token, leaving
// the game with an opaque 401 and the player with no way out.
func TestLauncherTokenExpiry(t *testing.T) {
	cases := []struct {
		name  string
		token string
		want  bool
	}{
		{"fresh 24h token", makeTestToken(t, time.Now().Add(24*time.Hour).Unix()), false},
		{"expired an hour ago", makeTestToken(t, time.Now().Add(-time.Hour).Unix()), true},
		{"expiring within the skew", makeTestToken(t, time.Now().Add(30*time.Second).Unix()), true},
		{"no exp claim", makeTestToken(t, 0), true},
		{"not a jwt", "garbage", true},
		{"empty", "", true},
		{"undecodable payload", "aaa.!!!not-base64!!!.ccc", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := launcherTokenExpired(tc.token); got != tc.want {
				t.Errorf("launcherTokenExpired() = %v, want %v", got, tc.want)
			}
		})
	}
}
