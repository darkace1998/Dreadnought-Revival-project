package main

// The counters the career goals count (career_goals.go), all from this
// server's own records. The original goal counters lived in client-side data
// that did not ship (issue #68); the client reports only a few of its own
// (YA_IncrementPlayerStatsCounter, e.g. Customize/Ship), so the server counts.
const (
	counterTutorialFinished  = "TutorialFinished"
	counterProvingGrounds    = "ProvingGroundsPlayed"
	counterMatchesWon        = "MatchesWon"
	counterShipsDestroyed    = "ShipsDestroyed"
	counterModulesResearched = "ModulesResearched"
	counterT2Owned           = "T2ShipsOwned"
	counterT3Owned           = "T3ShipsOwned"
	counterT4Owned           = "T4ShipsOwned"
	counterContracts         = "DailyContractsCompleted"
	counterTDMPlayed         = "TDMPlayed"
	counterTEPlayed          = "TEPlayed"
	counterOnslaughtPlayed   = "OnslaughtPlayed"
	counterConquestPlayed    = "ConquestPlayed"
)

// careerModeCounters maps a per-mode counter to the matchmaker's game modes.
var careerModeCounters = map[string][]string{
	counterProvingGrounds:  {"BC", "Bootcamp", "TM"},
	counterTDMPlayed:       {"TDM", "TurboTDM", "PodTDM"},
	counterTEPlayed:        {"TE"},
	counterOnslaughtPlayed: {"Onslaught"},
	counterConquestPlayed:  {"TER", "Territory"},
}

// careerCounterValue is the player's current amount for a goal counter.
func careerCounterValue(playerPID, counterID string) int32 {
	pid := normalizedPlayerStatePID(playerPID)
	database := currentMmogPlayerStateDB()
	if database == nil {
		return 0
	}
	count := func(query string, args ...any) int32 {
		var v int32
		if err := database.QueryRow(query, args...).Scan(&v); err != nil {
			return 0
		}
		return v
	}
	if modes, ok := careerModeCounters[counterID]; ok {
		var n int32
		for _, m := range modes {
			// battle_results.match_id is the battle server's id.
			n += count(`SELECT COUNT(*) FROM battle_results b JOIN matches m ON m.battle_match_id=b.match_id
				WHERE b.user_id=? AND b.match_id<>'' AND m.game_mode=?`, pid, m)
		}
		return n
	}
	switch counterID {
	case counterMatchesPlayed, counterMatchesWon, counterShipsDestroyed:
		return battleResultCounter(pid, counterID)
	case counterTutorialFinished:
		// The client reports the end of its tutorial (YA_AnalyticsTutorialSummaryEvent,
		// stored by telemetry.go). GUESS: a player who has played a match is
		// past the tutorial too (it can be skipped, and older accounts never
		// reported it).
		if count(`SELECT COUNT(*) FROM client_telemetry WHERE user_id=? AND rt='YA_AnalyticsTutorialSummaryEvent'`, pid) > 0 ||
			battleResultCounter(pid, counterMatchesPlayed) > 0 {
			return 1
		}
		return 0
	case counterModulesResearched:
		// GUESS: every researched module counts, not only tier I ones.
		return count(`SELECT COUNT(*) FROM player_purchases WHERE user_id=? AND item_type IN ('ability','weapon')`, pid)
	case counterContracts:
		return count(`SELECT COUNT(*) FROM player_daily_contracts WHERE user_id=? AND completed_at>0`, pid)
	case counterT2Owned, counterT3Owned, counterT4Owned:
		want := map[string]int32{counterT2Owned: 2, counterT3Owned: 3, counterT4Owned: 4}[counterID]
		var n int32
		for _, l := range ownedShipLoadoutsForPlayerData(mmogPlayerStateForPID(pid), pid) {
			tier, ok := shipTierForIDChecked(l.precastLoadoutID)
			if ok && (tier == want || (counterID == counterT4Owned && tier >= 4)) {
				n++
			}
		}
		return n
	}
	return 0
}
