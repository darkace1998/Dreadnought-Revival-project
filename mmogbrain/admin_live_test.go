package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSnapshotPeersOnFreshHub(t *testing.T) {
	hub := &socialHub{
		peers: map[string]*socialPeer{
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {
				playerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				name:     "Alice",
				channels: map[string]bool{"dreadnought.global": true},
			},
		},
		channels: map[string]map[string]bool{},
	}
	got := hub.snapshotPeers()
	if len(got) != 1 {
		t.Fatalf("got %d peers, want 1", len(got))
	}
	if got[0].name != "Alice" || len(got[0].channels) != 1 || got[0].channels[0] != "dreadnought.global" {
		t.Errorf("unexpected snapshot: %+v", got[0])
	}
}

func TestAdminBroadcastValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"empty content", `{"channel":"dreadnought.global","content":""}`},
		{"missing content", `{"channel":"dreadnought.global"}`},
		{"unknown channel", `{"channel":"nope","content":"hi"}`},
		{"overlong", `{"channel":"dreadnought.global","content":"` + strings.Repeat("x", 501) + `"}`},
		{"garbage", `not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			adminBroadcast(rec, httptest.NewRequest(http.MethodPost, "/admin/broadcast", strings.NewReader(tc.body)))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("got %d, want 400", rec.Code)
			}
		})
	}
}

// A valid broadcast must persist nothing without a DB (nil-safe) and touch no
// sockets when nobody is connected. It must not mutate the global hub.
func TestAdminBroadcastValidWithoutPeers(t *testing.T) {
	rec := httptest.NewRecorder()
	adminBroadcast(rec, httptest.NewRequest(http.MethodPost, "/admin/broadcast",
		strings.NewReader(`{"content":"restart in 5 minutes"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "dreadnought.global") {
		t.Errorf("default channel not used: %s", rec.Body.String())
	}
}

func TestAdminOnlineShape(t *testing.T) {
	rec := httptest.NewRecorder()
	adminOnline(rec, httptest.NewRequest(http.MethodGet, "/admin/online", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"online"`) || !strings.Contains(body, `"count"`) {
		t.Errorf("unexpected shape: %s", body)
	}
}
