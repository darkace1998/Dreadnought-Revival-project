package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	legacydb "github.com/darkace1998/Dreadnought-Revival-project/legacy-api/db"
	"github.com/sirupsen/logrus"
)

func historyTestHandler(t *testing.T) *Handler {
	t.Helper()
	database, err := legacydb.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`INSERT INTO match_history(id,mode,map,started_at,ended_at)
		VALUES('mh1','TDM','Highlands','2026-01-01 10:00:00','2026-01-01 10:15:00')`); err != nil {
		t.Fatalf("seed history: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO match_players(match_id,user_id,team,kills,deaths,damage)
		VALUES('mh1','user-1',1,5,2,9000),('mh1','user-2',2,3,4,7000)`); err != nil {
		t.Fatalf("seed players: %v", err)
	}
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return &Handler{DB: database, Log: logger}
}

func TestAdminHistoryListsMatchesWithRoster(t *testing.T) {
	h := historyTestHandler(t)
	rec := httptest.NewRecorder()
	h.AdminHistory(rec, httptest.NewRequest(http.MethodGet, "/admin/matches", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var doc struct {
		Matches []struct {
			ID      string `json:"id"`
			Mode    string `json:"mode"`
			Players []struct {
				UserID string `json:"user_id"`
				Team   int    `json:"team"`
				Kills  int    `json:"kills"`
			} `json:"players"`
		} `json:"matches"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Count != 1 || len(doc.Matches[0].Players) != 2 {
		t.Fatalf("unexpected history: %s", rec.Body.String())
	}
	if doc.Matches[0].Players[0].Kills != 5 {
		t.Errorf("roster not ordered by kills: %+v", doc.Matches[0].Players[0])
	}
}
