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

// The Havoc unlockable: the Junkyard Prince bundle carries HavocReward (bit 7,
// which makes the client's offer a YPIT_HAVOC_UNLOCKABLE_HERO_SHIP), holds the
// hero and the three Havoc cosmetics, is the only way to buy that hero, and is
// refused until the player's summed Havoc score reaches 10,000.
func TestHavocUnlockableHeroShip(t *testing.T) {
	const junkyardPrince = 67043338
	threshold, ok := havocUnlockThreshold(junkyardPrince)
	if !ok || threshold != 10000 {
		t.Fatalf("threshold %d %v, want 10000", threshold, ok)
	}
	var bundle marketBundle
	for _, b := range marketBundles {
		if b.havocUnlock {
			bundle = b
		}
	}
	heroes, vanity := bundle.contents()
	if len(heroes) != 1 || heroes[0] != junkyardPrince || len(vanity) != 3 {
		t.Fatalf("bundle contents heroes %v vanity %v", heroes, vanity)
	}
	found := false
	for _, s := range marketBundleCatalogSeeds("") {
		if s.itemID == bundle.id {
			found = true
			if s.promotionFlags&promotionFlagHavocReward == 0 || len(s.offerItemIDs) != 4 {
				t.Fatalf("seed flags %d items %v", s.promotionFlags, s.offerItemIDs)
			}
		}
	}
	if !found {
		t.Fatal("Havoc bundle not in the market")
	}
	for _, s := range heroCatalogSeeds("") {
		if s.itemID == junkyardPrince {
			t.Fatal("Junkyard Prince still sold as a plain hero ship")
		}
	}

	database := useTempMmogPlayerStateDB(t)
	const pid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE player_state SET premium_currency=100000 WHERE user_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	if why := havocUnlockRefusal(pid, junkyardPrince); why == "" {
		t.Fatal("bought with no Havoc score")
	}
	if _, _, reason := grantMarketBundle(database, pid, bundle); reason == "" {
		t.Fatal("bundle granted with no Havoc score")
	}
	for i, score := range []int{6000, 4000} { // sums to 10,000 over two runs
		if _, err := database.Exec(`INSERT INTO havoc_runs(match_id,user_id,score) VALUES(?,?,?)`, "r"+string(rune('0'+i)), pid, score); err != nil {
			t.Fatal(err)
		}
	}
	if why := havocUnlockRefusal(pid, junkyardPrince); why != "" {
		t.Fatalf("still refused at 10,000: %s", why)
	}
	if _, got, reason := grantMarketBundle(database, pid, bundle); reason != "" || len(got) != 1 {
		t.Fatalf("bundle after unlock: heroes %v reason %q", got, reason)
	}
}

// plainHeroForTest is a hero ship sold on its own: the first one that is not
// a Havoc unlockable (heroShipLoadouts[0] is the Junkyard Prince, which is).
func plainHeroForTest() int32 {
	for _, h := range heroShipLoadouts {
		if _, havoc := havocUnlockThreshold(h.loadoutID); !havoc {
			return h.loadoutID
		}
	}
	return 0
}
