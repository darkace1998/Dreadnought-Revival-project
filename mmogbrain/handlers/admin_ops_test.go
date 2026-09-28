package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"

	_ "github.com/mattn/go-sqlite3"
)

func resultsTestHandler(t *testing.T) *Handler {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE battle_results(
			match_id TEXT NOT NULL, user_id TEXT NOT NULL, team INTEGER NOT NULL DEFAULT 0,
			outcome TEXT NOT NULL, kills INTEGER NOT NULL DEFAULT 0, deaths INTEGER NOT NULL DEFAULT 0,
			assists INTEGER NOT NULL DEFAULT 0, damage INTEGER NOT NULL DEFAULT 0,
			credits INTEGER NOT NULL DEFAULT 0, xp INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (match_id, user_id))`,
		`INSERT INTO battle_results(match_id,user_id,team,outcome,kills,deaths,assists,damage,credits,xp,created_at)
			VALUES('m-old','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',1,'loss',0,3,0,1000,750,500,'2026-01-01 00:00:00')`,
		`INSERT INTO battle_results(match_id,user_id,team,outcome,kills,deaths,assists,damage,credits,xp,created_at)
			VALUES('m-new','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',2,'win',4,1,2,9000,1900,1300,'2026-06-01 00:00:00')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	log := logrus.New()
	log.SetOutput(nopWriter{})
	return &Handler{DB: db, Log: log}
}

func TestAdminResultsNewestFirst(t *testing.T) {
	h := resultsTestHandler(t)
	rec := httptest.NewRecorder()
	h.AdminResults(rec, httptest.NewRequest(http.MethodGet, "/admin/results", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var doc struct {
		Results []struct {
			MatchID string `json:"match_id"`
			Outcome string `json:"outcome"`
			Credits int    `json:"credits"`
		} `json:"results"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Count != 2 || len(doc.Results) != 2 {
		t.Fatalf("got %d results, want 2", doc.Count)
	}
	if doc.Results[0].MatchID != "m-new" || doc.Results[0].Outcome != "win" || doc.Results[0].Credits != 1900 {
		t.Errorf("newest first violated: %+v", doc.Results[0])
	}
}

func TestAdminResultsLimitClamped(t *testing.T) {
	h := resultsTestHandler(t)
	rec := httptest.NewRecorder()
	h.AdminResults(rec, httptest.NewRequest(http.MethodGet, "/admin/results?limit=1", nil))
	var doc struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Count != 1 {
		t.Errorf("count = %d, want 1", doc.Count)
	}
}
