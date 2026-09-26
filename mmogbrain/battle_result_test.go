package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBattleOutcome(t *testing.T) {
	for _, c := range []struct {
		team, final int
		want        string
	}{
		{1, 1, "win"}, {2, 2, "win"}, {1, 2, "loss"}, {2, 1, "loss"},
		{1, 3, "draw"}, {1, 0, "unknown"}, {0, 1, "loss"},
	} {
		if got := battleOutcome(c.team, c.final); got != c.want {
			t.Errorf("battleOutcome(%d,%d)=%q want %q", c.team, c.final, got, c.want)
		}
	}
}

// An empty or malformed pid must not fall back to the default dev player.
func TestBattleResultRejectsMissingPID(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	for _, pid := range []string{"", "not-a-pid"} {
		req := httptest.NewRequest(http.MethodGet, "/battle/result?match=m&final=1&team=1&pid="+pid, nil)
		req.RemoteAddr = "127.0.0.1:5000"
		rec := httptest.NewRecorder()
		battleResultHandler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("pid %q: status %d, want 400", pid, rec.Code)
		}
	}
}

func TestBattleResultRefusesNonLoopback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/battle/result?match=m&pid=a", nil)
	req.RemoteAddr = "10.0.0.26:5000"
	rec := httptest.NewRecorder()
	battleResultHandler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 for a non-loopback caller", rec.Code)
	}
}

// A win with 3 kills pays 1500+300 credits and 1000+150 XP (the placeholder
// values) to the wallet, free XP, rank XP and the ship flown -- once: the mod
// may report twice, and the second report must grant nothing.
func TestBattleResultAwardsOnce(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "0123456789abcdef0123456789abcdef"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	loadouts := ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(pid), pid)
	if len(loadouts) == 0 {
		t.Fatal("no starter loadouts")
	}
	flown := loadouts[0]
	read := func() (credits, freeXP, currentXP, shipXP int64) {
		t.Helper()
		if err := database.QueryRow(`SELECT soft_currency, free_xp, current_xp FROM player_state WHERE user_id=?`, pid).
			Scan(&credits, &freeXP, &currentXP); err != nil {
			t.Fatal(err)
		}
		_ = database.QueryRow(`SELECT xp FROM player_ship_xp WHERE user_id=? AND ship_id=?`, pid, flown.ship.id).Scan(&shipXP)
		return
	}
	c0, f0, x0, s0 := read()

	url := "/battle/result?match=M1&pid=" + pid + "&team=2&final=2&kills=3&deaths=1&ships=" + flown.entryID()
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.RemoteAddr = "127.0.0.1:5000"
		rec := httptest.NewRecorder()
		battleResultHandler(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "outcome=win") {
			t.Fatalf("report %d: %d %q", i, rec.Code, rec.Body.String())
		}
		wantNew := "new=true"
		if i == 1 {
			wantNew = "new=false"
		}
		if !strings.Contains(rec.Body.String(), wantNew) {
			t.Fatalf("report %d: want %s in %q", i, wantNew, rec.Body.String())
		}
	}
	c1, f1, x1, s1 := read()
	if c1-c0 != 1800 || f1-f0 != 1150 || x1-x0 != 1150 || s1-s0 != 1150 {
		t.Fatalf("deltas credits=%d freeXP=%d rankXP=%d shipXP=%d; want 1800/1150/1150/1150", c1-c0, f1-f0, x1-x0, s1-s0)
	}
	for counter, want := range map[string]int32{"MatchesPlayed": 1, "MatchesWon": 1, "ShipsDestroyed": 3} {
		if got := battleResultCounter(pid, counter); got != want {
			t.Errorf("%s = %d, want %d", counter, got, want)
		}
	}
}
