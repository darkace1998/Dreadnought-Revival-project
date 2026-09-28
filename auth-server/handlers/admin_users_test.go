package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminUsersListsEveryRegistration(t *testing.T) {
	h := newTestHandler(t)
	register(t, h, "alice", "alice@test.local", "secret123")
	register(t, h, "bob", "bob@test.local", "secret123")

	rec := httptest.NewRecorder()
	h.AdminUsers(rec, httptest.NewRequest(http.MethodGet, "/admin/users", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var doc struct {
		Users []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Email    string `json:"email"`
			Banned   bool   `json:"banned"`
		} `json:"users"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Count != 2 || len(doc.Users) != 2 {
		t.Fatalf("got %d users, want 2 (%s)", doc.Count, rec.Body.String())
	}
	// A registered account that never entered the game has no player_state
	// anywhere; it must still show up here with banned=false.
	for _, u := range doc.Users {
		if u.ID == "" || u.Email == "" {
			t.Errorf("user %+v missing id or email", u)
		}
		if u.Banned {
			t.Errorf("fresh user %s reported banned", u.Username)
		}
	}
}

func TestAdminUsersReportsBanState(t *testing.T) {
	h := newTestHandler(t)
	register(t, h, "mallory", "mallory@test.local", "secret123")

	banBody := `{"username":"mallory","reason":"testing"}`
	rec := httptest.NewRecorder()
	h.AdminBan(rec, httptest.NewRequest(http.MethodPost, "/admin/ban", strings.NewReader(banBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("ban status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.AdminUsers(rec, httptest.NewRequest(http.MethodGet, "/admin/users", nil))
	var doc struct {
		Users []struct {
			Username  string `json:"username"`
			Banned    bool   `json:"banned"`
			BanReason string `json:"ban_reason"`
		} `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(doc.Users) != 1 || !doc.Users[0].Banned || doc.Users[0].BanReason != "testing" {
		t.Fatalf("ban state not reported: %s", rec.Body.String())
	}
}
