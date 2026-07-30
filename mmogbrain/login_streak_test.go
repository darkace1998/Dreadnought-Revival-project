package main

import (
	"testing"
	"time"
)

// The client shows the daily-bonus screen whenever LoginStreak.loginstreak is
// positive -- FUN_142a3af90 sets its "show the bonus" flag (this+0x4148) purely
// on `0 < streak`, without looking at the reward values. So a repeat login on
// the same day has to report a zero streak, or the bonus appears on every
// launch, which is exactly what was reported.
func TestDailyLoginBonusIsGrantedOncePerDay(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	database := currentMmogPlayerStateDB()
	const pid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}

	streak, credits, freeXP, _ := applyDailyLoginStreak(database, pid)
	if streak <= 0 || credits <= 0 || freeXP <= 0 {
		t.Fatalf("first login of the day gave streak=%d credits=%d freexp=%d, want all positive", streak, credits, freeXP)
	}

	var creditsAfterFirst int32
	if err := database.QueryRow(`SELECT soft_currency FROM player_state WHERE user_id=?`, pid).Scan(&creditsAfterFirst); err != nil {
		t.Fatal(err)
	}

	// Second, third, fourth launch on the same day.
	for attempt := 2; attempt <= 4; attempt++ {
		streak, credits, freeXP, gp := applyDailyLoginStreak(database, pid)
		if streak != 0 {
			t.Errorf("launch %d reported streak=%d; anything positive re-shows the bonus screen", attempt, streak)
		}
		if credits != 0 || freeXP != 0 || gp != 0 {
			t.Errorf("launch %d granted credits=%d freexp=%d gp=%d, want none", attempt, credits, freeXP, gp)
		}
	}

	var creditsNow int32
	if err := database.QueryRow(`SELECT soft_currency FROM player_state WHERE user_id=?`, pid).Scan(&creditsNow); err != nil {
		t.Fatal(err)
	}
	if creditsNow != creditsAfterFirst {
		t.Errorf("currency moved from %d to %d on repeat logins; the bonus must be granted once", creditsAfterFirst, creditsNow)
	}

	// The stored streak must survive, so tomorrow continues the run.
	var storedStreak int32
	if err := database.QueryRow(`SELECT login_streak FROM player_state WHERE user_id=?`, pid).Scan(&storedStreak); err != nil {
		t.Fatal(err)
	}
	if storedStreak != 1 {
		t.Errorf("stored streak = %d, want 1 preserved across same-day logins", storedStreak)
	}
}

// A login the next day continues the run and pays out again.
func TestDailyLoginBonusResumesTheNextDay(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	database := currentMmogPlayerStateDB()
	const pid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	if _, err := database.Exec(`UPDATE player_state SET last_login_date=?, login_streak=? WHERE user_id=?`,
		yesterday, 3, pid); err != nil {
		t.Fatal(err)
	}

	streak, credits, _, _ := applyDailyLoginStreak(database, pid)
	if streak != 4 {
		t.Errorf("streak = %d after logging in a day later, want 4", streak)
	}
	if credits <= 0 {
		t.Error("the next day's login granted no bonus")
	}
}
