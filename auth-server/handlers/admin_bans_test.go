package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminBansListsActiveBans(t *testing.T) {
	h := newTestHandler(t)
	register(t, h, "grace", "grace@test.local", "secret123")
	register(t, h, "heidi", "heidi@test.local", "secret123")

	banBody := `{"username":"grace","reason":"griefing"}`
	rec := httptest.NewRecorder()
	h.AdminBan(rec, httptest.NewRequest(http.MethodPost, "/admin/ban", strings.NewReader(banBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("ban status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.AdminBans(rec, httptest.NewRequest(http.MethodGet, "/admin/bans", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var doc struct {
		Bans []struct {
			Username string `json:"username"`
			Reason   string `json:"reason"`
			Since    string `json:"since"`
		} `json:"bans"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Count != 1 || doc.Bans[0].Username != "grace" || doc.Bans[0].Reason != "griefing" {
		t.Fatalf("unexpected bans: %s", rec.Body.String())
	}

	// Unban removes the row: the list must be empty again.
	rec = httptest.NewRecorder()
	h.AdminUnban(rec, httptest.NewRequest(http.MethodPost, "/admin/unban",
		strings.NewReader(`{"username":"grace"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("unban status %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.AdminBans(rec, httptest.NewRequest(http.MethodGet, "/admin/bans", nil))
	doc = struct {
		Bans []struct {
			Username string `json:"username"`
			Reason   string `json:"reason"`
			Since    string `json:"since"`
		} `json:"bans"`
		Count int `json:"count"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Count != 0 {
		t.Fatalf("got %d bans after unban, want 0", doc.Count)
	}
}
