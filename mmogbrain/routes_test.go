package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/handlers"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
)

func playerToken(t *testing.T, secret []byte, userID string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":      userID,
		"user_id":  userID,
		"username": "tester",
		"aud":      []string{"dreadnought"},
		"iss":      protocol.GatewayJWTIssuer,
		"exp":      time.Now().Add(time.Hour).Unix(),
	})
	s, err := tok.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A player token must not be able to award progression. /mmog/progression took
// the target from the body, so any logged-in player could grant any account
// unlimited XP, rank and credits; it is gone, and only the key-guarded internal
// route remains.
func TestPlayerTokenCannotAwardProgression(t *testing.T) {
	secret := []byte("test-secret")
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	// A nil DB is fine: the handler must never be reached in this test.
	r := newRouter(&handlers.Handler{Log: log}, secret, "admin-key", "internal-key", log)

	post := func(path string, headers map[string]string) int {
		req := httptest.NewRequest(http.MethodPost, path,
			strings.NewReader(`{"user_id":"victim","xp":999999}`))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	bearer := map[string]string{"Authorization": "Bearer " + playerToken(t, secret, "attacker")}
	if code := post("/mmog/progression", bearer); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /mmog/progression with a player token = %d, want the route to be gone (404/405)", code)
	}
	for name, headers := range map[string]map[string]string{
		"a player token": bearer,
		"no key":         nil,
		"a wrong key":    {"X-Internal-Key": "nope"},
	} {
		if code := post("/internal/progression", headers); code != http.StatusForbidden {
			t.Errorf("POST /internal/progression with %s = %d, want 403", name, code)
		}
	}
}
