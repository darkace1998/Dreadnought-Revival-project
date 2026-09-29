package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestAdminCatalogListsShipsAndHeroes(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	rec := httptest.NewRecorder()
	adminCatalog(rec, httptest.NewRequest(http.MethodGet, "/admin/catalog", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"name":"Gora"`, `"name":"Athos"`, `"hero":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("catalog missing %s", want)
		}
	}
	// Tier-5 hull price follows the doubling curve (25000 << 4).
	if !strings.Contains(body, `"price_credits":400000`) {
		t.Errorf("no 400000 tier-5 price in catalog")
	}
}

func TestAdminPlayerProgressShape(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	progressReq := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/admin/player/"+id+"/progress", nil)
		req = mux.SetURLVars(req, map[string]string{"id": id})
		rec := httptest.NewRecorder()
		adminPlayerProgress(rec, req)
		return rec
	}
	rec := progressReq("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"goals"`, `"seasons"`, `"contracts"`, `"counters"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("progress missing %s", want)
		}
	}

	if rec := progressReq("nope"); rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400 for bad pid", rec.Code)
	}
}
