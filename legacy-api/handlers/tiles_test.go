package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	legacydb "github.com/darkace1998/Dreadnought-Revival-project/legacy-api/db"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

func tilesTestHandler(t *testing.T) *Handler {
	t.Helper()
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return &Handler{DB: database, Log: logger}
}

func TestTilesServesSeededDefaults(t *testing.T) {
	h := tilesTestHandler(t)
	rec := httptest.NewRecorder()
	h.Tiles(rec, httptest.NewRequest(http.MethodGet, "/v2/dreadnought/launcher/dn/tiles/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var doc struct {
		Result struct {
			Tiles []struct {
				ID     string `json:"id"`
				Active bool   `json:"active"`
			} `json:"tiles"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(doc.Result.Tiles) != 2 || doc.Result.Tiles[0].ID != "welcome" {
		t.Fatalf("seed tiles missing: %s", rec.Body.String())
	}
}

func TestAdminUpsertAndDeleteTile(t *testing.T) {
	h := tilesTestHandler(t)

	rec := httptest.NewRecorder()
	h.AdminUpsertTile(rec, httptest.NewRequest(http.MethodPost, "/admin/tiles",
		strings.NewReader(`{"id":"event-1","title":"Double XP weekend","body":"All matches pay double.","type":"event","section_size":"half"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.Tiles(rec, httptest.NewRequest(http.MethodGet, "/v2/dreadnought/launcher/dn/tiles/", nil))
	if !strings.Contains(rec.Body.String(), "Double XP weekend") {
		t.Fatalf("new tile not served: %s", rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodDelete, "/admin/tiles/event-1", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "event-1"})
	rec = httptest.NewRecorder()
	h.AdminDeleteTile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.Tiles(rec, httptest.NewRequest(http.MethodGet, "/v2/dreadnought/launcher/dn/tiles/", nil))
	if strings.Contains(rec.Body.String(), "Double XP weekend") {
		t.Fatalf("deleted tile still served: %s", rec.Body.String())
	}
}

func TestAdminUpsertTileValidation(t *testing.T) {
	h := tilesTestHandler(t)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"bad id", `{"id":"a/b","title":"t"}`},
		{"empty title", `{"id":"x","title":""}`},
		{"bad type", `{"id":"x","title":"t","type":"popup"}`},
		{"bad size", `{"id":"x","title":"t","section_size":"tiny"}`},
		{"garbage", `not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.AdminUpsertTile(rec, httptest.NewRequest(http.MethodPost, "/admin/tiles",
				strings.NewReader(tc.body)))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("got %d, want 400", rec.Code)
			}
		})
	}
}
