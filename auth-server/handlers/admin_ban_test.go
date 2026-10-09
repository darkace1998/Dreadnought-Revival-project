package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mmogbrain's admin dashboard bans by player id -- the account id without
// dashes -- since it does not know the launcher username.
func TestAdminBanByUserID(t *testing.T) {
	h := newTestHandler(t)
	register(t, h, "testpilot", "pilot@example.com", "hunter2x")
	var id string
	if err := h.DB.QueryRow(`SELECT id FROM users WHERE username='testpilot'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	pid := strings.ToLower(strings.ReplaceAll(id, "-", ""))

	rec := httptest.NewRecorder()
	h.AdminBan(rec, httptest.NewRequest(http.MethodPost, "/admin/ban", strings.NewReader(`{"user_id":"`+pid+`","reason":"test"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "testpilot") {
		t.Fatalf("ban by id: %d %s", rec.Code, rec.Body.String())
	}
	if rec := passwordLogin(t, h, "testpilot", "hunter2x"); rec.Code == http.StatusOK {
		t.Fatal("a banned account can still sign in")
	}

	rec = httptest.NewRecorder()
	h.AdminUnban(rec, httptest.NewRequest(http.MethodPost, "/admin/unban", strings.NewReader(`{"user_id":"`+pid+`"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("unban by id: %d %s", rec.Code, rec.Body.String())
	}
	if rec := passwordLogin(t, h, "testpilot", "hunter2x"); rec.Code != http.StatusOK {
		t.Fatalf("unbanned account cannot sign in: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.AdminBan(rec, httptest.NewRequest(http.MethodPost, "/admin/ban", strings.NewReader(`{"user_id":"ffffffffffffffffffffffffffffffff","reason":"x"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: %d, want 404", rec.Code)
	}
}
