package main

// YA_GetLeaderboardData -- the Leaderboards screen.
//
// Until 2026-10-16 this request had no handler and got the generic fallback, so
// the screen was always empty (operator: "the leaderboard is not working").
//
// Request (captured 2026-10-16): PID (GUID), modename ("TDM", "Onslaught", ...:
// the same mode-name table, 0x143D25250, that matchmaking sends as its game
// type), timespan ("Weekly" or "Lifetime", UUI_Leaderboard_Screen::
// RequestLeaderboardData 0xADC5A0), fleettier (1 Recruit / 2 Veteran /
// 3 Legendary) and prevweek (bool).
//
// Reply parser 0x2A6E840 (mmog_client.cpp), read from the decompile:
//   - the ROOT must carry modename, fleettier and timespan, or it logs "Json
//     doesn't include the correct keys" and drops the reply; prevweek is read
//     from the root too.
//   - result.leaderboard is an ARRAY of entries ("Leaderboard has no entries"
//     otherwise).
//   - result.playerrank is an ARRAY too; its first element is the player's own
//     row. Anything else leaves the client's default row, "NOT RANKED!".
//   - each entry (0x2A6F280): PID (any type, made an FName), Rank (int),
//     SID (string), Score (int).
// The result is cached under modename+fleet tier name+timespan(+"Prev")
// (0x2A6F4D0). Names come with the entry: YA_GetPlayersInformation, which the
// client sends straight after, carries no name field at all (arm 0x2A28230:
// ID, DisplayInfo, Rank, UnlockedFleetType, Elite).
//
// GUESS: SID is the player's "Name#1234" tag, the same form the login reply's
// Username has. It is the entry's only string, and the screen's row
// (FLeaderboardUIEntry) has m_playerName and m_playerNumber.
// GUESS: the score is the sum of the player's match scores (PRI m_score,
// reported by the battle-server mod since 2026-10-15; earlier matches have no
// score and do not count).
// GUESS: the weekly board resets Monday 00:00 UTC.

import (
	"strings"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

const leaderboardSize = 100

type leaderboardRow struct {
	pid   string
	score int64
}

// leaderboardWindow is the [from, to) created_at range for a timespan, in the
// "YYYY-MM-DD HH:MM:SS" UTC form SQLite's datetime('now') writes; empty
// strings mean unbounded.
func leaderboardWindow(timespan string, prevWeek bool, now time.Time) (from, to string) {
	if !strings.EqualFold(strings.TrimSpace(timespan), "Weekly") {
		return "", ""
	}
	now = now.UTC()
	daysSinceMonday := (int(now.Weekday()) + 6) % 7
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -daysSinceMonday)
	end := start.AddDate(0, 0, 7)
	if prevWeek {
		start, end = start.AddDate(0, 0, -7), start
	}
	const layout = "2006-01-02 15:04:05"
	return start.Format(layout), end.Format(layout)
}

// leaderboardRows ranks every player with a score in the mode, fleet tier and
// window, best first (ties: the earlier first result).
func leaderboardRows(mode string, fleetTier int32, from, to string) []leaderboardRow {
	database := currentMmogPlayerStateDB()
	if database == nil || strings.TrimSpace(mode) == "" {
		return nil
	}
	query := `SELECT r.user_id, SUM(s.value) AS total, MIN(r.created_at) AS first
		FROM battle_result_stats s
		JOIN battle_results r ON r.match_id = s.match_id AND r.user_id = s.user_id
		JOIN matches m ON m.battle_match_id = r.match_id
		WHERE s.stat = 'score' AND m.game_mode = ? COLLATE NOCASE AND r.fleet_type = ?
		  AND r.user_id NOT IN (SELECT user_id FROM admin_bans)`
	args := []any{mode, fleetTier}
	if from != "" {
		query += ` AND r.created_at >= ? AND r.created_at < ?`
		args = append(args, from, to)
	}
	query += ` GROUP BY r.user_id HAVING total > 0 ORDER BY total DESC, first ASC`
	rows, err := database.Query(query, args...)
	if err != nil {
		logrus.WithError(err).Warn("leaderboard: query failed")
		return nil
	}
	// Read everything before any other DB call: the database has one
	// connection, and a query inside this loop would deadlock the server.
	var out []leaderboardRow
	for rows.Next() {
		var r leaderboardRow
		var first string
		if rows.Scan(&r.pid, &r.score, &first) == nil {
			out = append(out, r)
		}
	}
	_ = rows.Close()
	return out
}

func appendLeaderboardEntry(b []byte, stack []int, row leaderboardRow, rank int) ([]byte, []int) {
	pid := normalizedPlayerStatePID(row.pid)
	score := row.score
	if score > 1<<31-1 {
		score = 1<<31 - 1
	}
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "PID", pid)
	b = protocol.AppendInt32Field(b, "Rank", int32(rank))
	b = protocol.AppendStringField(b, "SID", gatewayUsernameTag(mmogPlayerStateForPID(pid).displayName, pid))
	b = protocol.AppendInt32Field(b, "Score", int32(score))
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func buildMmogLeaderboardPayload(playerPID string, payload []byte) []byte {
	mode := strings.TrimSpace(protocol.ExtractStringField(payload, "modename"))
	timespan := strings.TrimSpace(protocol.ExtractStringField(payload, "timespan"))
	fleetTier, ok := protocol.ExtractInt32Field(payload, "fleettier")
	if !ok || fleetTier < 1 || fleetTier > 3 {
		fleetTier = 1
	}
	// prevweek is a bool (tag 0x05), which ExtractInt32Field skips.
	var prev int64
	for _, f := range protocol.Scalars(payload) {
		if f.Name == "prevweek" && !f.IsStr {
			prev = f.Num
		}
	}
	from, to := leaderboardWindow(timespan, prev != 0, time.Now())
	rows := leaderboardRows(mode, fleetTier, from, to)

	self := normalizedPlayerStatePID(playerPID)
	selfRank := 0
	for i, r := range rows {
		if normalizedPlayerStatePID(r.pid) == self {
			selfRank = i + 1
			break
		}
	}

	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_GetLeaderboardData")
	b = protocol.AppendStringField(b, "modename", mode)
	b = protocol.AppendInt32Field(b, "fleettier", fleetTier)
	b = protocol.AppendStringField(b, "timespan", timespan)
	b = protocol.AppendBoolField(b, "prevweek", prev != 0)
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "leaderboard")
	for i, r := range rows {
		if i >= leaderboardSize {
			break
		}
		b, stack = appendLeaderboardEntry(b, stack, r, i+1)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendArrayStart(b, stack, "playerrank")
	if selfRank > 0 {
		b, stack = appendLeaderboardEntry(b, stack, rows[selfRank-1], selfRank)
	}
	b, stack = protocol.AppendObjectEnd(b, stack) // playerrank
	b, _ = protocol.AppendObjectEnd(b, stack)     // result

	logrus.WithFields(logrus.Fields{
		"player": self, "mode": mode, "fleet_tier": fleetTier, "timespan": timespan,
		"prev_week": prev != 0, "ranked": len(rows), "player_rank": selfRank,
	}).Info("leaderboard: answered")
	return b
}
