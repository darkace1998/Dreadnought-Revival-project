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
		{1, 3, "draw"}, {1, 0, "unknown"}, {0, 1, "unknown"}, {0, 3, "draw"},
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
	// Win + 3 kills: intermediate credits 1500+3*100 = 1800 x (1+0.75+0.25+1.00)
	// = 5400; intermediate XP 1000+3*50 = 1150 x (1+1.25+0.25+1.00) = 4025.
	if c1-c0 != 5400 || f1-f0 != 4025 || x1-x0 != 4025 || s1-s0 != 4025 {
		t.Fatalf("deltas credits=%d freeXP=%d rankXP=%d shipXP=%d; want 5400/4025/4025/4025", c1-c0, f1-f0, x1-x0, s1-s0)
	}
	for counter, want := range map[string]int32{"MatchesPlayed": 1, "MatchesWon": 1, "ShipsDestroyed": 3} {
		if got := battleResultCounter(pid, counter); got != want {
			t.Errorf("%s = %d, want %d", counter, got, want)
		}
	}
}

func TestBattleRewardFormula(t *testing.T) {
	r := battleRewards{winCredits: 1500, lossCredits: 750, killCredits: 100, winXP: 1000, lossXP: 500, killXP: 50,
		xpBonuses: []float64{1.25, 0.25}, creditBonuses: []float64{0.75, 0.25}, fleetBonuses: []float64{1.00, 1.25, 1.50}}
	for _, c := range []struct {
		name        string
		outcome     string
		kills       int32
		fleet       int
		credits, xp int32
	}{
		// Recruit: x3.0 credits, x3.5 XP -- the operator's formula as written.
		{"recruit loss", "loss", 0, 1, 2250, 1750},
		{"unknown match pays recruit", "loss", 0, 0, 2250, 1750},
		// Veteran +0.25: x3.25 / x3.75.
		{"veteran win 2 kills", "win", 2, 2, 5525, 4125},
		// Legendary +0.50: x3.5 / x4.0.
		{"legendary win 2 kills", "win", 2, 3, 5950, 4400},
	} {
		if cr, xp := r.forOutcome(c.outcome, c.kills, c.fleet); cr != c.credits || xp != c.xp {
			t.Errorf("%s: %d credits %d xp, want %d / %d", c.name, cr, xp, c.credits, c.xp)
		}
	}
	r.eliteTeamPct = 50
	if cr, xp := r.forOutcome("win", 2, 1); cr != 5950 || xp != 4400 {
		t.Errorf("recruit win, 2 kills, elite 50%%: %d credits %d xp, want 1700x3.5=5950, 1100x4.0=4400", cr, xp)
	}
}

func TestMatchFleetTypeFromBattleMatchID(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	if _, err := database.Exec(`INSERT INTO matches(id,game_mode,map,battle_match_id,fleet_type) VALUES('m1','TDM','x','dn-1',3)`); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int{"dn-1": 3, "dn-1-r2": 3, "dn-2": 0, "": 0} {
		if got := matchFleetType(database, id); got != want {
			t.Errorf("matchFleetType(%q) = %d, want %d", id, got, want)
		}
	}
}
