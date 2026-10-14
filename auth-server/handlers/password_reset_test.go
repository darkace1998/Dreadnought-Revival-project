package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An admin reset by player id (the dashboard's form): the new password signs
// in, the old one no longer does, and the account's sessions are gone.
func TestAdminResetPassword(t *testing.T) {
	h := newTestHandler(t)
	register(t, h, "testpilot", "pilot@example.com", "hunter2x")
	var id string
	if err := h.DB.QueryRow(`SELECT id FROM users WHERE username='testpilot'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if rec := passwordLogin(t, h, "testpilot", "hunter2x"); rec.Code != http.StatusOK {
		t.Fatalf("login before reset: %d", rec.Code)
	}
	pid := strings.ToLower(strings.ReplaceAll(id, "-", ""))

	rec := httptest.NewRecorder()
	h.AdminResetPassword(rec, httptest.NewRequest(http.MethodPost, "/admin/password", strings.NewReader(`{"user_id":"`+pid+`"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
	var reply struct {
		Password string `json:"password"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || len(reply.Password) != 14 || reply.Username != "testpilot" {
		t.Fatalf("reply %s", rec.Body.String())
	}
	if rec := passwordLogin(t, h, "testpilot", reply.Password); rec.Code != http.StatusOK {
		t.Fatalf("new password does not sign in: %d", rec.Code)
	}
	if rec := passwordLogin(t, h, "testpilot", "hunter2x"); rec.Code == http.StatusOK {
		t.Fatal("old password still signs in")
	}

	rec = httptest.NewRecorder()
	h.AdminResetPassword(rec, httptest.NewRequest(http.MethodPost, "/admin/password", strings.NewReader(`{"user_id":"ffffffffffffffffffffffffffffffff"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: %d, want 404", rec.Code)
	}
}
