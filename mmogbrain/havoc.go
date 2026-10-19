package main

import (
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
	"github.com/sirupsen/logrus"
)

// Havoc scores and weekly rewards (2026-10-19).
//
// A Havoc run's score is the team's total XP (AYGameState_Havoc m_totalXP):
// the client's end-of-match struct YEOMHavocProgressionData carries it as
// m_teamTotalXP, filled from AYPlayerReplicationInfo_Havoc +0xA44, the
// matchScore of ClientUpdateHighscore(lifetimePlayerScore, weeklyPlayerScore,
// matchScore). The same screen (0x449470) compares the DN_HavocRewards_DT
// milestones (m_highscore 1000 .. 25000) with the weekly best (+0xA48) --
// "unlocked" -- and with the match score (+0xA44) -- "this round" -- and
// counts down to the weekly reset (+0xA58, ClientUpdateWeeklyScoreResetTime).
// So a milestone is weekly: it pays once a week, the first time a run's score
// reaches it.
//
// The menu reads the bests from YA_GetPlayerScores (mmog_client 0x2A1FE10
// sends it with no parameters; dispatcher 0x2A25B4B takes the reply's root
// "result" as an ARRAY of {GameMode, TimeSpan, Score} (0x2A73250) into
// FYPlayerScoreData, looked up as ("HAVOC", "LIFETIME") and ("HAVOC",
// "LIFETIMESUM"): "AYMenu::InitializeMenu | Havoc lifetime score is %d; sum
// score %d").
//
// GUESS: the weekly reset is Monday 00:00 UTC, as for the leaderboard; the
// reset time is sent as Unix seconds.
// GUESS: LIFETIME is the best single run, LIFETIMESUM the sum of all runs.

const havocGameModeKey = "HAVOC"

// havocWeekStart is the start of the Havoc week holding now.
func havocWeekStart(now time.Time) time.Time {
	now = now.UTC()
	daysSinceMonday := (int(now.Weekday()) + 6) % 7
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -daysSinceMonday)
}

const havocTimeLayout = "2006-01-02 15:04:05"

// havocBests: the best single run ever and this week, and the sum of all runs,
// leaving out excludeMatch (the run being reported, so its screen can show a
// new record).
func havocBests(database *sql.DB, pid, excludeMatch string, now time.Time) (lifetime, weekly, sum int32) {
	week := havocWeekStart(now).Format(havocTimeLayout)
	_ = database.QueryRow(`SELECT COALESCE(MAX(score),0), COALESCE(SUM(score),0) FROM havoc_runs
		WHERE user_id=? AND match_id<>?`, pid, excludeMatch).Scan(&lifetime, &sum)
	_ = database.QueryRow(`SELECT COALESCE(MAX(score),0) FROM havoc_runs
		WHERE user_id=? AND match_id<>? AND created_at>=?`, pid, excludeMatch, week).Scan(&weekly)
	return lifetime, weekly, sum
}

// havocMilestones are DN_HavocRewards_DT's rows, lowest score first.
func havocMilestones() []dreadconfig.HavocReward {
	rows := append([]dreadconfig.HavocReward(nil), dreadconfig.AllHavocRewards()...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Highscore < rows[j].Highscore })
	return rows
}

type havocPayout struct {
	freeXP, credits int32
	paid            []string
}

// payHavocMilestones pays every milestone the run reached that this player has
// not been paid this week. Idempotent per (player, week, milestone).
func payHavocMilestones(tx *sql.Tx, pid, match string, score int32, now time.Time) (havocPayout, error) {
	var out havocPayout
	week := havocWeekStart(now).Format(havocTimeLayout)
	for _, m := range havocMilestones() {
		if m.Highscore <= 0 || score < m.Highscore || m.Value <= 0 {
			continue
		}
		res, err := tx.Exec(`INSERT OR IGNORE INTO havoc_reward_claims(user_id,week_start,reward,match_id) VALUES(?,?,?,?)`,
			pid, week, m.RowName, match)
		if err != nil {
			return out, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // paid earlier this week
		}
		switch {
		case strings.HasSuffix(m.RewardType, "YHRT_FREEXP"):
			out.freeXP += m.Value
		case strings.HasSuffix(m.RewardType, "YHRT_CREDITS"):
			out.credits += m.Value
		default:
			// YHRT_ITEM has no row in DN_HavocRewards_DT; the hero ship is an
			// unlockable bought in the market (YPIT_HAVOC_UNLOCKABLE_HERO_SHIP).
			continue
		}
		out.paid = append(out.paid, m.RowName)
	}
	if out.freeXP > 0 || out.credits > 0 {
		if _, err := tx.Exec(`UPDATE player_state SET soft_currency=soft_currency+?, free_xp=free_xp+?,
			updated_at=datetime('now') WHERE user_id=?`, out.credits, out.freeXP, pid); err != nil {
			return out, err
		}
	}
	return out, nil
}

// GET /battle/havoc?match=&pid=&score=&waves= -- battle-server-mod at Game
// Over, once per player. Records the run, pays the week's new milestones and
// answers the bests before this run plus the reset time:
//
//	lifetime=<n>\nweekly=<n>\nsum=<n>\nreset=<unix seconds>\nfreexp=<n>\ncredits=<n>\n
func battleHavocHandler(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		http.Error(w, "loopback only", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	pid := protocol.NormalizePlayerPID(q.Get("pid"))
	match := strings.TrimSpace(q.Get("match"))
	score64, errScore := strconv.ParseInt(q.Get("score"), 10, 32)
	waves64, _ := strconv.ParseInt(q.Get("waves"), 10, 32)
	if pid == "" || match == "" || errScore != nil || score64 < 0 {
		http.Error(w, "pid, match and score are required", http.StatusBadRequest)
		return
	}
	database := currentMmogPlayerStateDB()
	if database == nil {
		http.Error(w, "no database", http.StatusServiceUnavailable)
		return
	}
	score, waves := int32(score64), int32(waves64)
	now := time.Now()
	lifetime, weekly, sum := havocBests(database, pid, match, now)
	var payout havocPayout
	tx, err := database.Begin()
	if err == nil {
		_, err = tx.Exec(`INSERT OR IGNORE INTO havoc_runs(match_id,user_id,score,waves) VALUES(?,?,?,?)`,
			match, pid, score, waves)
		if err == nil {
			payout, err = payHavocMilestones(tx, pid, match, score, now)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
	}
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{"match": match, "player": pid}).Error("havoc: run not recorded")
		http.Error(w, "not recorded", http.StatusInternalServerError)
		return
	}
	reset := havocWeekStart(now).AddDate(0, 0, 7).Unix()
	logrus.WithFields(logrus.Fields{"match": match, "player": pid, "score": score, "waves": waves,
		"lifetime_best": lifetime, "weekly_best": weekly, "milestones_paid": payout.paid,
		"free_xp": payout.freeXP, "credits": payout.credits}).Info("havoc: run recorded")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprintf(w, "lifetime=%d\nweekly=%d\nsum=%d\nreset=%d\nfreexp=%d\ncredits=%d\n",
		lifetime, weekly, sum+score, reset, payout.freeXP, payout.credits)
}

// havocPlayerScores is the YA_GetPlayerScores result for a player: the
// Havoc best ever (LIFETIME), the sum of all runs (LIFETIMESUM) and the best
// this week (WEEKLY).
func havocPlayerScores(pid string) (lifetime, sum, weekly int32) {
	database := currentMmogPlayerStateDB()
	if database == nil || pid == "" {
		return 0, 0, 0
	}
	lifetime, weekly, sum = havocBests(database, protocol.NormalizePlayerPID(pid), "", time.Now())
	return lifetime, sum, weekly
}

// Havoc unlockable hero ship (DN_HavocUnlockables_DT "UnlockHeroShip":
// m_highscore 10000, YHRT_ITEM 67043338, the Junkyard Prince).
//
// The client sells it as an offer of purchase type
// YPIT_HAVOC_UNLOCKABLE_HERO_SHIP (3): FUN_1404ff770 gives a bundle offer of
// more than one item with a hero ship among them type 2 (hero ship), and 3
// when the offer's PromotionFlags carry bit 7 (EYMarketItemPromotionFlags
// YMIPF_HavocReward). The numeric PromotionFlags field survives the catalog
// converter (0x2A7E1A0 ORs the named flags into it). For types 3 and 5 the
// purchase dialog (RequestBundlePurchaseData 0x4C2920) shows the player's
// ("HAVOC", "LIFETIMESUM") score against the unlockable's m_highscore -- so the
// unlock counts every run's score, summed.
// GUESS: buying stays refused below the threshold; the price is the hero's
// usual tier price; the bundle's cosmetics are the client's Havoc set for this
// hull (havocUnlockBundle in market_offers.go).

// havocUnlockThreshold is the summed Havoc score that unlocks itemID, or
// false when itemID is not a Havoc unlockable.
func havocUnlockThreshold(itemID int32) (int32, bool) {
	for _, u := range dreadconfig.AllHavocUnlockables() {
		if u.Value == itemID && strings.HasSuffix(u.RewardType, "YHRT_ITEM") && u.Highscore > 0 {
			return u.Highscore, true
		}
	}
	return 0, false
}

// havocUnlockRefusal is why pid may not buy itemID yet, or "" when it may.
func havocUnlockRefusal(pid string, itemID int32) string {
	threshold, ok := havocUnlockThreshold(itemID)
	if !ok {
		return ""
	}
	_, sum, _ := havocPlayerScores(pid)
	if sum >= threshold {
		return ""
	}
	return fmt.Sprintf("havoc unlockable: summed Havoc score %d of %d", sum, threshold)
}
