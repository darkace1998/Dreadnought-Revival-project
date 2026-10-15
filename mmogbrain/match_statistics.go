package main

import (
	"database/sql"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// Statistics screen figures beyond wins/kills/deaths/assists (2026-10-15,
// operator: "not all data is getting populated"). YA_GetPlayerStatistics
// entries also carry ScoreSum, HealPoints, DoubleKillNum, FirstStrikeNum and
// seconds per ship type; all were sent as 0. Where each comes from:
//
//   - DoubleKillNum: the scoring event Dominator (ordinal 21), the host's own
//     double kill -- original table Parameters VAL(2):T(15), the game's goal
//     text "Kill 2 enemies within 15 sec. without dying". Counted by the host
//     and reported in the mod's ev=. That the old backend's DoubleKillNum was
//     exactly this count is THEORY.
//   - FirstStrikeNum: the scoring event FirstStrike (ordinal 24), same report.
//   - ScoreSum: the player's match score, AYPlayerReplicationInfo::m_score
//     (+0x7FC, SDK), reported by the mod as score=. Without it (an older mod
//     DLL) the same score is rebuilt from the event counts and the original
//     table's EventScore for the mode -- GUESS that the host scores nothing
//     outside that table.
//   - HealPoints: m_totalHealingDone (+0x924, SDK), reported as heal=. No
//     fallback: the Repair event counts thresholds, not hit points.
//   - <Type>TimeSec: seconds alive in each EYShipClass, sampled once a second
//     by the mod and reported as ct=class.seconds,... Without it, the match's
//     time is split evenly over the ships flown -- GUESS, no data says how long
//     each was flown.
//
// Matches before this change have only their ribbon-derived double kills and
// first strikes (migration "before-2026-10-15" in db/db.go).
const (
	statScore        = "score"
	statHealing      = "healing"
	statDoubleKills  = "double_kills"
	statFirstStrikes = "first_strikes"
	statTimePrefix   = "time_" // + shipTypeStat
)

// shipTypeStat is the Statistics field family of an EYShipClass: the five
// ship types the screen shows, each with its light/medium/heavy hulls
// (EYShipClass from the SDK: 1 DL, 2 SL, 3 SnL, 4 SuL, 5 AL, 6 DM, 7 DH, 8 SM,
// 9 SH, 10 SnM, 11 SnH, 12 SuM, 13 SuH, 14 AM, 15 AH). Assault hulls are the
// game's Destroyers, Sniper its Artillery Cruisers, Support its Tactical
// Cruisers, Scout its Corvettes.
func shipTypeStat(class int) string {
	switch class {
	case 1, 6, 7:
		return "Dreadnought"
	case 2, 8, 9:
		return "Corvette"
	case 3, 10, 11:
		return "ArtilleryCruiser"
	case 4, 12, 13:
		return "TacticalCruiser"
	case 5, 14, 15:
		return "Destroyer"
	}
	return ""
}

// matchStatValues is what one reported result adds to the Statistics totals.
func matchStatValues(q url.Values, counts map[int]int, mode string, ships []string, matchSeconds int) map[string]int64 {
	v := map[string]int64{}
	if n, ok := scoringCount(counts, "Dominator"); ok {
		v[statDoubleKills] = n
	}
	if n, ok := scoringCount(counts, "FirstStrike"); ok {
		v[statFirstStrikes] = n
	}
	if s, err := strconv.ParseInt(strings.TrimSpace(q.Get("score")), 10, 64); err == nil && s >= 0 {
		v[statScore] = s
	} else if s := scoreFromEvents(counts, mode); s > 0 {
		v[statScore] = s
	}
	if h, err := strconv.ParseInt(strings.TrimSpace(q.Get("heal")), 10, 64); err == nil && h > 0 {
		v[statHealing] = h
	}
	if q.Has("ct") {
		for _, part := range strings.Split(q.Get("ct"), ",") {
			a, b, ok := strings.Cut(strings.TrimSpace(part), ".")
			if !ok {
				continue
			}
			class, err1 := strconv.Atoi(a)
			secs, err2 := strconv.ParseInt(b, 10, 64)
			if t := shipTypeStat(class); err1 == nil && err2 == nil && t != "" && secs > 0 && secs <= 6*3600 {
				v[statTimePrefix+t] += secs
			}
		}
	} else if types := flownShipTypes(ships); len(types) > 0 && matchSeconds > 0 {
		for _, t := range types {
			v[statTimePrefix+t] += int64(matchSeconds / len(types))
		}
	}
	return v
}

func scoringCount(counts map[int]int, event string) (int64, bool) {
	ord, ok := scoringEventOrdinal(event)
	if !ok {
		return 0, false
	}
	n, ok := counts[ord]
	return int64(n), ok && n > 0
}

// scoreFromEvents rebuilds a match score from its event counts and the
// original table's EventScore for mode (see the GUESS above).
func scoreFromEvents(counts map[int]int, mode string) int64 {
	var s int64
	for ord, n := range counts {
		if ord < 0 || ord >= len(scoringEventIDs) {
			continue
		}
		if r, ok := findOriginalScoringRow(scoringEventIDs[ord]); ok {
			s += int64(n) * int64(scoringValueFor(r.EventScore, mode))
		}
	}
	return s
}

// flownShipTypes is the ship type of each loadout the mod says was picked
// (blueprint names such as Default__VH_SniperLight_T2_PrecastLoadout_BP_C).
func flownShipTypes(ships []string) []string {
	var out []string
	for _, s := range ships {
		_, rest, ok := strings.Cut(s, "VH_")
		if !ok {
			continue
		}
		key, _, _ := strings.Cut(rest, "_")
		if class, ok := eyShipClassByKey[key]; ok {
			if t := shipTypeStat(int(class)); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// matchPlaySeconds is the match's time so far from its server becoming ready,
// capped at an hour -- the fallback for the per-type split only.
func matchPlaySeconds(database *sql.DB, battleMatchID string) int {
	_, readyAt := matchModeAndStart(database, battleMatchID)
	if readyAt.IsZero() {
		return 0
	}
	secs := int(time.Since(readyAt).Seconds())
	if secs > 3600 {
		secs = 3600
	}
	return secs
}

func recordMatchStats(database *sql.DB, match, pid string, values map[string]int64) {
	if database == nil {
		return
	}
	for stat, value := range values {
		if _, err := database.Exec(`INSERT OR IGNORE INTO battle_result_stats(match_id,user_id,stat,value) VALUES(?,?,?,?)`,
			match, pid, stat, value); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{"match": match, "player": pid, "stat": stat}).Warn("statistics: not recorded")
		}
	}
}

// playerStatTotals sums a player's recorded per-match statistics.
func playerStatTotals(database *sql.DB, pid string) map[string]int64 {
	totals := map[string]int64{}
	if database == nil {
		return totals
	}
	rows, err := database.Query(`SELECT stat, COALESCE(SUM(value),0) FROM battle_result_stats WHERE user_id=? GROUP BY stat`, pid)
	if err != nil {
		logrus.WithError(err).Warn("statistics: read totals")
		return totals
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var stat string
		var v int64
		if rows.Scan(&stat, &v) == nil {
			totals[stat] = v
		}
	}
	return totals
}
