package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

func TestAdminSessionsListsAndRevokes(t *testing.T) {
	h := newTestHandler(t)
	register(t, h, "ivan", "ivan@test.local", "secret123")
	if rec := passwordLogin(t, h, "ivan", "secret123"); rec.Code != http.StatusOK {
		t.Fatalf("login status %d, body %s", rec.Code, rec.Body.String())
	}

	rec := httptest.NewRecorder()
	h.AdminSessions(rec, httptest.NewRequest(http.MethodGet, "/admin/sessions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var doc struct {
		Sessions []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Expired  bool   `json:"expired"`
		} `json:"sessions"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Count != 1 || doc.Sessions[0].Username != "ivan" || doc.Sessions[0].Expired {
		t.Fatalf("unexpected sessions: %s", rec.Body.String())
	}

	// Revoke it: the session must be gone afterwards.
	req := httptest.NewRequest(http.MethodDelete, "/admin/sessions/"+doc.Sessions[0].ID, nil)
	req = mux.SetURLVars(req, map[string]string{"id": doc.Sessions[0].ID})
	rec = httptest.NewRecorder()
	h.AdminDeleteSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.AdminSessions(rec, httptest.NewRequest(http.MethodGet, "/admin/sessions", nil))
	doc = struct {
		Sessions []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Expired  bool   `json:"expired"`
		} `json:"sessions"`
		Count int `json:"count"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Count != 0 {
		t.Fatalf("got %d sessions after revoke, want 0", doc.Count)
	}
}

func TestAdminDeleteSessionValidation(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodDelete, "/admin/sessions/nope", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "nope"})
	rec := httptest.NewRecorder()
	h.AdminDeleteSession(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/admin/sessions/00000000-0000-0000-0000-000000000000", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "00000000-0000-0000-0000-000000000000"})
	rec = httptest.NewRecorder()
	h.AdminDeleteSession(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}
