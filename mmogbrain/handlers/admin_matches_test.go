package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	_ "github.com/mattn/go-sqlite3"
)

func matchesTestHandler(t *testing.T) *Handler {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE matches(id TEXT PRIMARY KEY, game_mode TEXT, map TEXT, server_ip TEXT,
			server_port INTEGER, status TEXT, created_at TEXT, instance_id TEXT, server_ready_at TEXT)`,
		`CREATE TABLE match_slots(match_id TEXT, user_id TEXT, team INTEGER,
			PRIMARY KEY (match_id, user_id))`,
		`INSERT INTO matches(id,game_mode,map,server_ip,server_port,status,created_at,instance_id)
			VALUES('mm1','TDM','Highlands','10.0.0.73',7777,'active','2026-06-01 10:00:00','inst-1')`,
		`INSERT INTO match_slots(match_id,user_id,team) VALUES('mm1','alice',1),('mm1','bob',2)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	log := logrus.New()
	log.SetOutput(nopWriter{})
	return &Handler{DB: db, Log: log}
}

func TestAdminMatchesListsWithPlayerCounts(t *testing.T) {
	h := matchesTestHandler(t)
	rec := httptest.NewRecorder()
	h.AdminMatches(rec, httptest.NewRequest(http.MethodGet, "/admin/matches", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var doc struct {
		Matches []struct {
			ID      string `json:"id"`
			Players int    `json:"players"`
		} `json:"matches"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Count != 1 || doc.Matches[0].Players != 2 {
		t.Fatalf("unexpected matches: %s", rec.Body.String())
	}
}

func TestAdminMatchDetailListsSlots(t *testing.T) {
	h := matchesTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/match/mm1", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "mm1"})
	rec := httptest.NewRecorder()
	h.AdminMatchDetail(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var doc struct {
		Match struct {
			ID   string `json:"id"`
			Mode string `json:"game_mode"`
		} `json:"match"`
		Slots []struct {
			UserID string `json:"user_id"`
			Team   int    `json:"team"`
		} `json:"slots"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Match.Mode != "TDM" || len(doc.Slots) != 2 {
		t.Fatalf("unexpected detail: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/match/nope", nil)
	req = mux.SetURLVars(req, map[string]string{"id": "nope"})
	rec = httptest.NewRecorder()
	h.AdminMatchDetail(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}
