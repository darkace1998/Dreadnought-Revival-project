package matchmaker

import (
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// Backfill: prefer a match with other players over a private one.
//
// Matches are auto-sized by who is idle (requiredMatchSize), so a player who
// queues while everyone else is in a battle starts a match alone, against
// bots -- and the next player to queue a minute later started ANOTHER one.
// Measured 2026-10-02: 77 of 124 matches in 24 h had one human.
//
// So before a new match is formed, waiting players join a match that is
// already running when one fits: same game mode and fleet type, its host has
// reported ready, it started less than BackfillWindow ago, it has at least one
// player in it and room for the whole party (PlayersPerMatch). The party goes
// on the side with fewer players (all on team 1 in co-op modes). The battle
// server takes late joiners: it is not told who may join, and the mod's bot
// balance gives a bot's place to each human who arrives.
//
// OFF by default (main.go): live, late joiners logged in but never got a
// ship selection (0 of 11 picked a ship, 2026-10-03), and joiners after the
// bots spawned found no PlayerStart. Unresolved; see main.go.
//
// GUESS: the window. The original game's join-in-progress rules are not
// known; 3 minutes keeps a joiner well inside a ~10 minute match.

// backfillMatch is a running match a waiting party could join.
type backfillMatch struct {
	id    string
	teams map[int]int // players per team
	total int
}

// backfill places the waiting parties of one queue bucket into running
// matches. It returns how many players it placed.
func (m *Matchmaker) backfill(gameMode string, tierMin, fleetType int) (int, error) {
	if m.BackfillWindow <= 0 {
		return 0, nil
	}
	groups, err := m.waitingGroups(gameMode, tierMin, fleetType)
	if err != nil || len(groups) == 0 {
		return 0, err
	}
	runMode := runnableGameMode(gameMode)
	cutoff := time.Now().UTC().Add(-m.BackfillWindow).Format(time.RFC3339)
	rows, err := m.DB.Query(`SELECT id FROM matches
		WHERE status='active' AND game_mode=? AND fleet_type=? AND server_ready_at IS NOT NULL
		  AND datetime(created_at) >= datetime(?)
		ORDER BY created_at DESC`, runMode, fleetType, cutoff)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	// Team counts AFTER the rows are closed: one database connection.
	var matches []*backfillMatch
	for _, id := range ids {
		bm := &backfillMatch{id: id, teams: map[int]int{}}
		trows, err := m.DB.Query(`SELECT team, COUNT(*) FROM match_slots WHERE match_id=? GROUP BY team`, id)
		if err != nil {
			return 0, err
		}
		for trows.Next() {
			var team, n int
			if trows.Scan(&team, &n) == nil {
				bm.teams[team] = n
				bm.total += n
			}
		}
		_ = trows.Close()
		if bm.total > 0 { // nobody left in it: the host is about to be reaped
			matches = append(matches, bm)
		}
	}
	if len(matches) == 0 {
		return 0, nil
	}

	pvp := matchTeam(runMode, 1) == 2
	placed := 0
	for _, g := range groups {
		var target *backfillMatch
		for _, bm := range matches {
			if bm.total+len(g) <= m.PlayersPerMatch {
				target = bm
				break
			}
		}
		if target == nil {
			continue
		}
		team := 1
		if pvp && target.teams[2] < target.teams[1] {
			team = 2
		}
		for _, e := range g {
			if _, err := m.DB.Exec(`INSERT OR REPLACE INTO match_slots(match_id,user_id,team) VALUES(?,?,?)`, target.id, e.UserID, team); err != nil {
				return placed, fmt.Errorf("backfill slot for %s: %w", e.UserID, err)
			}
			if _, err := m.DB.Exec(`DELETE FROM queue_entries WHERE id=?`, e.ID); err != nil {
				return placed, fmt.Errorf("delete backfilled queue entry %s: %w", e.ID, err)
			}
		}
		target.teams[team] += len(g)
		target.total += len(g)
		placed += len(g)
		m.Log.WithFields(logrus.Fields{
			"match_id":   target.id,
			"game_mode":  runMode,
			"fleet_type": fleetType,
			"players":    len(g),
			"team":       team,
			"in_match":   target.total,
		}).Info("matchmaker: backfilled a running match instead of starting a new one")
	}
	return placed, nil
}
