package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func havocReport(t *testing.T, pid, match, score string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/battle/havoc?pid="+pid+"&match="+match+"&score="+score+"&waves=4", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	battleHavocHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d %s", match, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// A milestone (DN_HavocRewards_DT) pays once a week, the first time a run
// reaches it; the answer carries the bests from BEFORE the run, so the
// client's end-of-match screen can show a new record.
func TestHavocRunPaysWeeklyMilestonesOnce(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	balance := func() (credits, freeXP int64) {
		t.Helper()
		if err := database.QueryRow(`SELECT soft_currency, free_xp FROM player_state WHERE user_id=?`, pid).Scan(&credits, &freeXP); err != nil {
			t.Fatal(err)
		}
		return
	}
	c0, x0 := balance()

	// 1600 reaches "Beginner" (1000: 500 free XP) and "2" (1500: 750 credits).
	body := havocReport(t, pid, "run-1", "1600")
	if !strings.Contains(body, "lifetime=0\n") || !strings.Contains(body, "weekly=0\n") ||
		!strings.Contains(body, "freexp=500\n") || !strings.Contains(body, "credits=750\n") {
		t.Fatalf("first run: %q", body)
	}
	c1, x1 := balance()
	if c1-c0 != 750 || x1-x0 != 500 {
		t.Fatalf("paid credits %d free XP %d, want 750 / 500", c1-c0, x1-x0)
	}

	// The same report again (a retry) pays nothing more.
	havocReport(t, pid, "run-1", "1600")
	// A better run the same week: only the newly reached "3" (2500: 1000 credits).
	body = havocReport(t, pid, "run-2", "2600")
	if !strings.Contains(body, "lifetime=1600\n") || !strings.Contains(body, "weekly=1600\n") ||
		!strings.Contains(body, "freexp=0\n") || !strings.Contains(body, "credits=1000\n") {
		t.Fatalf("second run: %q", body)
	}
	if c2, x2 := balance(); c2-c1 != 1000 || x2 != x1 {
		t.Fatalf("second run paid credits %d free XP %d, want 1000 / 0", c2-c1, x2-x1)
	}

	// Last week's run counts for the lifetime best only.
	old := time.Now().UTC().AddDate(0, 0, -8).Format(havocTimeLayout)
	if _, err := database.Exec(`INSERT INTO havoc_runs(match_id,user_id,score,waves,created_at) VALUES('run-0',?,9000,9,?)`, pid, old); err != nil {
		t.Fatal(err)
	}
	lifetime, sum, weekly := havocPlayerScores(pid)
	if lifetime != 9000 || weekly != 2600 || sum != 9000+1600+2600 {
		t.Fatalf("scores lifetime %d sum %d weekly %d", lifetime, sum, weekly)
	}
}

func TestHavocWeekStartsMonday(t *testing.T) {
	sun := time.Date(2026, 10, 18, 23, 0, 0, 0, time.UTC)
	if got := havocWeekStart(sun).Format(havocTimeLayout); got != "2026-10-12 00:00:00" {
		t.Fatalf("week of Sunday 18th starts %s", got)
	}
}
