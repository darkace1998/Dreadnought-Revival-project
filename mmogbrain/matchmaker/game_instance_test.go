package matchmaker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func testMatchmaker(url string) *Matchmaker {
	log := logrus.New()
	log.SetOutput(newDiscard())
	return &Matchmaker{Log: log, GameMgrURL: url, InternalKey: "k", PlayersPerMatch: 1}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func newDiscard() discard { return discard{} }

// A game-manager that accepts the connection and never answers used to hang the
// single matchmaker goroutine forever, which stops every tick: no new matches
// for anyone, no stale-match sweep, and the queue entries of the in-flight match
// stuck in 'matched' with nothing left to roll them back.
func TestRequestGameInstanceTimesOut(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() {
		close(block)
		srv.Close()
	}()

	saved := gameManagerHTTPClient
	gameManagerHTTPClient = &http.Client{Timeout: 200 * time.Millisecond}
	defer func() { gameManagerHTTPClient = saved }()

	done := make(chan error, 1)
	go func() {
		_, _, _, err := testMatchmaker(srv.URL).requestGameInstance("TM", "Highlands", "/Game/x", []string{"p1"})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want an error from a hung game manager, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("requestGameInstance did not return; the client has no timeout")
	}
}

// A 201 carrying no address must not produce a match: the player would be sent
// to nothing and wait on "Battle server starting" while every server-side view
// of the match looks healthy.
func TestRequestGameInstanceRejectsUnusableAddress(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"empty ip", `{"ip":"","port":7777,"instance_id":"i"}`},
		{"missing ip", `{"port":7777,"instance_id":"i"}`},
		{"zero port", `{"ip":"10.0.0.73","port":0,"instance_id":"i"}`},
		{"missing port", `{"ip":"10.0.0.73","instance_id":"i"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, _, _, err := testMatchmaker(srv.URL).requestGameInstance("TM", "Highlands", "/Game/x", []string{"p1"})
			if err == nil {
				t.Fatalf("want an error for %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), "no usable address") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRequestGameInstanceAcceptsUsableAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Key") != "k" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ip":"10.0.0.73","port":7777,"instance_id":"abc"}`))
	}))
	defer srv.Close()

	ip, port, id, err := testMatchmaker(srv.URL).requestGameInstance("TM", "Highlands", "/Game/x", []string{"p1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.0.73" || port != 7777 || id != "abc" {
		t.Fatalf("got %s:%d id=%s", ip, port, id)
	}
}

func TestGameManagerRequestTimeoutFromEnv(t *testing.T) {
	if got := gameManagerRequestTimeout(); got != 30*time.Second {
		t.Fatalf("default timeout = %v, want 30s", got)
	}
	t.Setenv("DN_GAME_MGR_TIMEOUT", "90s")
	if got := gameManagerRequestTimeout(); got != 90*time.Second {
		t.Fatalf("env timeout = %v, want 90s", got)
	}
	// A malformed or non-positive value falls back rather than disabling the
	// timeout, which is what a bare 0 would do to an http.Client.
	t.Setenv("DN_GAME_MGR_TIMEOUT", "0")
	if got := gameManagerRequestTimeout(); got != 30*time.Second {
		t.Fatalf("zero timeout = %v, want the 30s default", got)
	}
	t.Setenv("DN_GAME_MGR_TIMEOUT", "not-a-duration")
	if got := gameManagerRequestTimeout(); got != 30*time.Second {
		t.Fatalf("garbage timeout = %v, want the 30s default", got)
	}
}
