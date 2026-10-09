package main

import (
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
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
	// The payout formula alone: a daily contract completed by this match
	// would add its own credits (daily_contracts_test.go covers those).
	t.Setenv("DN_DAILY_CONTRACTS", "0")
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
	// Free XP is 25% of that (DN_REWARD_FREE_XP_PERCENT), 1006; it was the
	// whole 4025 until 2026-09-29 (operator: "should be way less ... maybe
	// 25 %"). Rank and ship XP still get the whole amount.
	if c1-c0 != 5400 || f1-f0 != 1006 || x1-x0 != 4025 || s1-s0 != 4025 {
		t.Fatalf("deltas credits=%d freeXP=%d rankXP=%d shipXP=%d; want 5400/1006/4025/4025", c1-c0, f1-f0, x1-x0, s1-s0)
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

// The end-of-match screen shows the payout by EYXPPoolType; the pools must add
// up (as the client adds them: all but 1, 2, 10, 11) to exactly what is paid.
func TestRewardPoolsAddUpToThePayout(t *testing.T) {
	r := battleRewards{winCredits: 1500, lossCredits: 750, killCredits: 100, winXP: 1000, lossXP: 500, killXP: 50,
		xpBonuses: []float64{1.25, 0.25}, creditBonuses: []float64{0.75, 0.25}, fleetBonuses: []float64{1.00, 1.25, 1.50}}
	for _, fleet := range []int{1, 2, 3} {
		for _, kills := range []int32{0, 3, 7} {
			credits, xp := r.poolsFor("win", kills, fleet)
			wantC, wantX := r.forOutcome("win", kills, fleet)
			if credits.total() != wantC || xp.total() != wantX {
				t.Errorf("fleet %d kills %d: pools total %d/%d, paid %d/%d", fleet, kills, credits.total(), xp.total(), wantC, wantX)
			}
			if xp[0] != xp[1]+xp[2] || xp[1] != 1000 || xp[2] != 50*kills {
				t.Errorf("fleet %d kills %d: scoring %d = base %d + performance %d?", fleet, kills, xp[0], xp[1], xp[2])
			}
			if xp[6+fleet] == 0 {
				t.Errorf("fleet %d: fleet bonus not in BattleReady pool %d: %v", fleet, 6+fleet, xp)
			}
		}
	}
	// Win + 3 kills, Recruit: credits 1800 scoring + 1350 gold + 450 teammates + 1800 fleet.
	credits, _ := r.poolsFor("win", 3, 1)
	if credits[0] != 1800 || credits[5] != 1350 || credits[6] != 450 || credits[7] != 1800 {
		t.Errorf("credit pools %v", credits)
	}
}

// The rewards screen looks each fleet ship up by its fleet ship id; pawn ids
// made the client file ShipXpError for every ship (2026-09-28).
func TestBattleFleetShipIDsAreFleetShipIDs(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "0123456789abcdef0123456789abcdef"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	state := mmogPlayerStateForPID(pid)
	fleet := state.activeFleet()
	ids := battleFleetShipIDs(pid, int(fleet.fleetType))
	if len(ids) == 0 {
		t.Fatal("no fleet ship ids")
	}
	pawn := map[int32]bool{}
	for _, l := range fleet.shipLoadouts {
		if l.ship.id != fleetShipKey(l) {
			pawn[l.ship.id] = true
		}
	}
	for _, id := range ids {
		if pawn[id] {
			t.Errorf("id %d is a pawn id, not a fleet ship id", id)
		}
	}
}

// The end-of-match screen's free XP (xp_pools -> m_matchXPInfo.m_freeXP) is
// the free share, and its pools add up to exactly what is paid as free XP.
func TestFreeXPPoolsAreTheFreeShare(t *testing.T) {
	r := currentBattleRewards()
	_, xp := r.poolsFor("win", 3, 1)
	free := xp.scaled(r.freeXPPct, r.freeXPOf(xp.total()))
	if free.total() != r.freeXPOf(xp.total()) || free.total() >= xp.total() {
		t.Errorf("free pools total %d, want %d (25%% of %d)", free.total(), r.freeXPOf(xp.total()), xp.total())
	}
}

// Every ship of the fleet earns ship XP, not only the ones flown ("all ship
// need to get xp if u take them in the fleet ... ther is like 10% of the total
// xp earned is for the other ships that were not played", operator
// 2026-09-30; the game: "every ship in that fleet will earn Ship XP"). The
// performance reward goes only to the ships played.
func TestUnplayedFleetShipsEarnAShare(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "0123456789abcdef0123456789abcdef"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	fleet := battleFleetLoadouts(pid, 0)
	if len(fleet) < 2 {
		t.Fatalf("the starter fleet has %d ships; need at least 2", len(fleet))
	}
	flown, benched := fleet[0], fleet[1]
	shipXP := func(id int32) (xp int64) {
		_ = database.QueryRow(`SELECT xp FROM player_ship_xp WHERE user_id=? AND ship_id=?`, pid, id).Scan(&xp)
		return
	}
	req := httptest.NewRequest(http.MethodGet, "/battle/result?match=M2&pid="+pid+"&team=2&final=2&kills=3&ships="+flown.entryID(), nil)
	req.RemoteAddr = "127.0.0.1:5000"
	rec := httptest.NewRecorder()
	battleResultHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	// Flown: the whole match XP, 4025 (see TestBattleResultAwardsOnce).
	// Not flown: 10% of the XP WITHOUT the 3 kills: 1000 x 3.5 = 3500 -> 350.
	if got := shipXP(flown.ship.id); got != 4025 {
		t.Errorf("flown ship earned %d, want 4025", got)
	}
	if got := shipXP(benched.ship.id); got != 350 {
		t.Errorf("unflown fleet ship earned %d, want 350 (10%% of the XP without performance)", got)
	}
	// ...and the end-of-match screen gets the same amount for those ships.
	var line string
	for _, l := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(l, "unplayed_ship_xp_pools=") {
			line = strings.TrimPrefix(l, "unplayed_ship_xp_pools=")
		}
	}
	var p rewardPools
	for i, v := range strings.Split(line, ",") {
		if i < rewardPoolCount {
			n, _ := strconv.Atoi(v)
			p[i] = int32(n)
		}
	}
	if line == "" || p.total() != 350 {
		t.Errorf("unplayed_ship_xp_pools %q totals %d, want 350", line, p.total())
	}
}

// Free XP and ship XP reach the client only through YA_PlayerGet, so after a
// match they stayed stale until a relog (operator 2026-09-30). A fresh result
// queues a YA_ConvertShipXP push: result "bought", FreeXp the new total
// (assigned by the client), and each ship's gain as a NEGATIVE ShipXp (the
// client subtracts), keyed by the hull loadout id the client uses.
func TestBattleResultPushesTheNewXP(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "0123456789abcdef0123456789abcdef"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	squadHubInstance.connected(pid) // pushes go only to players online
	t.Cleanup(func() { squadHubInstance.disconnected(pid) })
	_ = squadHubInstance.drainPushes(pid)
	flown := battleFleetLoadouts(pid, 0)[0]
	req := httptest.NewRequest(http.MethodGet, "/battle/result?match=M3&pid="+pid+"&team=2&final=2&kills=3&ships="+flown.entryID(), nil)
	req.RemoteAddr = "127.0.0.1:5000"
	battleResultHandler(httptest.NewRecorder(), req)

	var push []byte
	for _, p := range squadHubInstance.drainPushes(pid) {
		if protocol.FirstStringField(p, "RT") == "YA_ConvertShipXP" {
			push = p
		}
	}
	if push == nil {
		t.Fatal("no YA_ConvertShipXP push queued after the result")
	}
	var freeXP int32
	if err := database.QueryRow(`SELECT free_xp FROM player_state WHERE user_id=?`, pid).Scan(&freeXP); err != nil {
		t.Fatal(err)
	}
	if got := protocol.ExtractStringField(push, "result"); got != "bought" {
		t.Errorf("result %q, want bought (the handler's success value)", got)
	}
	if got := protocol.ExtractStringField(push, "FreeXp"); got != strconv.Itoa(int(freeXP)) {
		t.Errorf("FreeXp %q, want the new total %d", got, freeXP)
	}
	entry := string(protocol.AppendStringField(nil, "ShipID", strconv.Itoa(int(fleetShipKey(flown))))) +
		string(protocol.AppendStringField(nil, "ShipXp", "-4025"))
	if !strings.Contains(string(push), entry) {
		t.Errorf("no ShipXps entry {ShipID %d, ShipXp -4025} for the flown ship", fleetShipKey(flown))
	}
}

// With the battle server's original event XP the XP base is that sum plus the
// end-of-match awards; credits keep the formula.
func TestEventXPReplacesTheXPBase(t *testing.T) {
	r := currentBattleRewards()
	plainCredits, _ := r.forOutcome("win", 5, 1)
	ev := r.withEventXP(1200)
	credits, xp := ev.forOutcome("win", 5, 1)
	if credits != plainCredits {
		t.Errorf("credits %d, want the formula's %d", credits, plainCredits)
	}
	if want := int32(math.Round(1200 * r.multiplier(r.xpBonuses, 1))); xp != want {
		t.Errorf("xp %d, want 1200 x multiplier = %d", xp, want)
	}
	if got := ev.unplayedShipPools("win", 1).total(); got <= 0 {
		t.Error("unflown ships earn nothing from event XP")
	}
}

// The Match Finish ribbon plus PlayTime every 30 s, by the match's mode.
func TestEndOfMatchEventXP(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	ready := time.Now().Add(-300 * time.Second).UTC().Format(time.RFC3339)
	if _, err := database.Exec(`INSERT INTO matches(id, game_mode, map, battle_match_id, server_ready_at) VALUES('m1','TDM','Gorge','b1',?), ('m2','Onslaught','Gorge','b2',?)`, ready, ready); err != nil {
		t.Fatal(err)
	}
	if xp, mode := endOfMatchEventXP(database, "b1"); mode != "TDM" || xp < 118 || xp > 122 {
		t.Errorf("TDM: %d (%s), want 100 ribbon + 2 x 10 play-time ticks = 120", xp, mode)
	}
	if xp, mode := endOfMatchEventXP(database, "b2"); mode != "IVN" || xp < 108 || xp > 111 {
		t.Errorf("Onslaught: %d (%s), want 100 + 1 x 10 = 110", xp, mode)
	}
}
