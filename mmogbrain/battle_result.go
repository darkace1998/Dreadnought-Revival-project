package main

import (
	"database/sql"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/handlers"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

// Match results (2026-09-26). Nothing ever reported a finished match: the
// battle server is the client exe, and the original backend's server build --
// which told mmogbrain who won -- is not in it. So no match ever paid XP or
// credits, and no career goal or quest could advance (audit 2026-09-26).
//
// battle-server-mod reads each connected player's result from the host at the
// end of the match (AYPlayerReplicationInfo m_kills/m_deaths/m_assists/damage/
// m_team, AYGameState m_finalMatchResult) and reports it here, once per player.
//
// GET /battle/result?match=&pid=&team=&final=&kills=&deaths=&assists=&damage=&ships=
//
//	team   EYTeam of the player (1, 2)
//	final  EYMatchResult of the match (1 team 1 won, 2 team 2 won, 3 draw, 0 unknown)
//	ships  comma-separated loadout ids the player picked this match
//
// Loopback only, like /battle/loadout. Idempotent: battle_results is keyed by
// (match_id, user_id), so a repeated report grants nothing.
//
// GUESS (the operator's placeholder values, 2026-09-26; the original reward
// tables are lost -- GitHub issue #71): credits 1500 for a
// win / 750 otherwise, +100 per kill; XP 1000 / 500, +50 per kill, granted as
// free XP, as rank XP and split over the ships flown. Each is overridable.
//
// Formula (operator, 2026-09-28):
//
//	Intermediate  = Base Reward (win/loss) + Performance Reward (per kill)
//	Total XP      = Intermediate_xp      x (1 + 1.25 + 0.25 + Fleet + EliteTeam%)
//	Total Credits = Intermediate_credits x (1 + 0.75 + 0.25 + Fleet + EliteTeam%)
//
// Same formula in every game mode. Fleet is the fleet battle bonus of the
// match's fleet tier: Recruit 100%, Veteran 125%, Legendary 150% (operator,
// 2026-09-28) -- the formula's "1.00" term, which is the Recruit value. The
// tier comes from the match record (matches.fleet_type, keyed by the battle
// server's match id); a result whose match is unknown pays Recruit.
//
// Configurable: DN_REWARD_XP_BONUSES / DN_REWARD_CREDIT_BONUSES (the fixed
// terms, comma-separated fractions), DN_REWARD_FLEET_BONUSES (Recruit,
// Veteran, Legendary) and DN_REWARD_ELITE_TEAM_PCT (a percentage, 0 by
// default: nothing grants it yet). The fixed terms apply to every result.
type battleRewards struct {
	winCredits, lossCredits, killCredits int32
	winXP, lossXP, killXP                int32
	xpBonuses, creditBonuses             []float64
	fleetBonuses                         []float64 // by EYFleetType-1: Recruit, Veteran, Legendary
	eliteTeamPct                         float64
}

// fleetBonus is the fleet battle bonus of an EYFleetType (1 Recruit,
// 2 Veteran, 3 Legendary); anything else pays Recruit.
func (r battleRewards) fleetBonus(fleetType int) float64 {
	if fleetType < 1 || fleetType > len(r.fleetBonuses) {
		fleetType = 1
	}
	if len(r.fleetBonuses) == 0 {
		return 0
	}
	return r.fleetBonuses[fleetType-1]
}

// multiplier is 1 + every fixed bonus term + the fleet bonus + EliteTeam%.
func (r battleRewards) multiplier(bonuses []float64, fleetType int) float64 {
	m := 1 + r.fleetBonus(fleetType) + r.eliteTeamPct/100
	for _, b := range bonuses {
		m += b
	}
	return m
}

func parseRewardBonuses(env string, def []float64) []float64 {
	v := strings.TrimSpace(os.Getenv(env))
	if v == "" {
		return def
	}
	var out []float64
	for _, f := range strings.Split(v, ",") {
		b, err := strconv.ParseFloat(strings.TrimSpace(f), 64)
		if err != nil || b < 0 {
			logrus.WithField("value", v).Warn(env + ": not a list of non-negative numbers; using the default")
			return def
		}
		out = append(out, b)
	}
	return out
}

func currentBattleRewards() battleRewards {
	n := func(env string, def int32) int32 {
		if v, err := strconv.Atoi(os.Getenv(env)); err == nil && v >= 0 {
			return int32(v)
		}
		return def
	}
	return battleRewards{
		winCredits: n("DN_REWARD_WIN_CREDITS", 1500), lossCredits: n("DN_REWARD_LOSS_CREDITS", 750), killCredits: n("DN_REWARD_KILL_CREDITS", 100),
		winXP: n("DN_REWARD_WIN_XP", 1000), lossXP: n("DN_REWARD_LOSS_XP", 500), killXP: n("DN_REWARD_KILL_XP", 50),
		xpBonuses:     parseRewardBonuses("DN_REWARD_XP_BONUSES", []float64{1.25, 0.25}),
		creditBonuses: parseRewardBonuses("DN_REWARD_CREDIT_BONUSES", []float64{0.75, 0.25}),
		fleetBonuses:  parseRewardBonuses("DN_REWARD_FLEET_BONUSES", []float64{1.00, 1.25, 1.50}),
		eliteTeamPct:  float64(n("DN_REWARD_ELITE_TEAM_PCT", 0)),
	}
}

// battleOutcome turns the host's numbers into win / loss / draw. Unknown (the
// match result not yet set) pays as a loss.
func battleOutcome(team, final int) string {
	switch {
	case final == 3:
		return "draw"
	case team < 1 || team > 2:
		// The host could not say which side the player was on (the first
		// live report read YT_NONE); a result against "no team" is unknown,
		// not a loss.
		return "unknown"
	case final >= 1 && final <= 2 && final == team:
		return "win"
	case final >= 1 && final <= 2:
		return "loss"
	default:
		return "unknown"
	}
}

func (r battleRewards) forOutcome(outcome string, kills int32, fleetType int) (credits, xp int32) {
	if kills < 0 {
		kills = 0
	}
	if kills > 500 { // no match has that many; caps a malformed report
		kills = 500
	}
	baseCredits, baseXP := r.lossCredits, r.lossXP
	if outcome == "win" {
		baseCredits, baseXP = r.winCredits, r.winXP
	}
	intermediateCredits := baseCredits + r.killCredits*kills
	intermediateXP := baseXP + r.killXP*kills
	return int32(math.Round(float64(intermediateCredits) * r.multiplier(r.creditBonuses, fleetType))),
		int32(math.Round(float64(intermediateXP) * r.multiplier(r.xpBonuses, fleetType)))
}

func battleResultHandler(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		http.Error(w, "loopback only", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	match := strings.TrimSpace(q.Get("match"))
	// Not normalizedPlayerStatePID: it falls back to the default dev player,
	// and a report with a bad pid must pay nobody.
	pid := protocol.NormalizePlayerPID(q.Get("pid"))
	if match == "" || pid == "" {
		http.Error(w, "match and pid are required", http.StatusBadRequest)
		return
	}
	num := func(k string) int { v, _ := strconv.Atoi(q.Get(k)); return v }
	res := battleResult{
		match: match, pid: pid, team: num("team"),
		outcome: battleOutcome(num("team"), num("final")),
		kills:   int32(num("kills")), deaths: int32(num("deaths")), assists: int32(num("assists")), damage: int32(num("damage")),
	}
	for _, id := range strings.Split(q.Get("ships"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			res.ships = append(res.ships, id)
		}
	}
	credits, xp, fresh, err := recordBattleResult(res, currentBattleRewards())
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{"match": match, "player": pid}).Error("battle result: not recorded")
		http.Error(w, "not recorded", http.StatusInternalServerError)
		return
	}
	logrus.WithFields(logrus.Fields{"match": match, "player": pid, "outcome": res.outcome, "fleet_type": res.fleetType, "kills": res.kills,
		"deaths": res.deaths, "credits": credits, "xp": xp, "ships": res.ships, "new": fresh}).Info("battle result")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprintf(w, "outcome=%s\ncredits=%d\nxp=%d\nnew=%v\n", res.outcome, credits, xp, fresh)
}

type battleResult struct {
	match, pid, outcome            string
	team                           int
	fleetType                      int // EYFleetType of the match; 0 = match not found (pays Recruit)
	kills, deaths, assists, damage int32
	ships                          []string // loadout ids picked this match
}

// recordBattleResult stores the result and grants its rewards in one
// transaction. fresh is false when this (match, player) was already recorded.
func recordBattleResult(res battleResult, rewards battleRewards) (credits, xp int32, fresh bool, err error) {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return 0, 0, false, fmt.Errorf("database unavailable")
	}
	if err := seedMmogPlayerState(database, res.pid); err != nil {
		return 0, 0, false, err
	}
	res.fleetType = matchFleetType(database, res.match)
	credits, xp = rewards.forOutcome(res.outcome, res.kills, res.fleetType)

	// Ship XP goes to the hulls actually flown, resolved to pawn ids the way
	// player_ship_xp keys them. Resolved BEFORE the transaction: the store has
	// one connection, and a query inside an open transaction waits for itself.
	var ships []int32
	seen := map[int32]bool{}
	for _, id := range res.ships {
		if loadout, ok := battleLoadoutFor(res.pid, id); ok && loadout.ship.id != 0 && !seen[loadout.ship.id] {
			seen[loadout.ship.id] = true
			ships = append(ships, loadout.ship.id)
		}
	}

	tx, err := database.Begin()
	if err != nil {
		return 0, 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	ins, err := tx.Exec(`INSERT OR IGNORE INTO battle_results(match_id,user_id,team,outcome,kills,deaths,assists,damage,credits,xp,fleet_type)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, res.match, res.pid, res.team, res.outcome, res.kills, res.deaths, res.assists, res.damage, credits, xp, res.fleetType)
	if err != nil {
		return 0, 0, false, err
	}
	if n, _ := ins.RowsAffected(); n == 0 {
		return credits, xp, false, nil // already paid
	}
	if err := grantBattleRewards(tx, res.pid, credits, xp, ships); err != nil {
		return 0, 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, false, err
	}
	// The connected client still shows its login-time balance; the frame loop
	// picks this up as a fresh YA_RewardCurrencies push (currency_dirty.go).
	markCurrencyDirty(res.pid)
	return credits, xp, true, nil
}

// matchFleetType is the EYFleetType of the match a result was reported for,
// or 0 when no match record has that battle-server match id (a match formed
// before battle_match_id was recorded, or a host started by hand). The mod
// appends "-r<n>" to the id for a second round on the same host.
func matchFleetType(database *sql.DB, battleMatchID string) int {
	if i := strings.LastIndex(battleMatchID, "-r"); i > 0 {
		if _, err := strconv.Atoi(battleMatchID[i+2:]); err == nil {
			battleMatchID = battleMatchID[:i]
		}
	}
	var fleetType int
	if err := database.QueryRow(`SELECT fleet_type FROM matches WHERE battle_match_id=? AND battle_match_id!=''`, battleMatchID).Scan(&fleetType); err != nil {
		return 0
	}
	return fleetType
}

func grantBattleRewards(tx *sql.Tx, pid string, credits, xp int32, ships []int32) error {
	var currentXP, rank, rankXP int32
	if err := tx.QueryRow(`SELECT current_xp, current_rank, rank_xp FROM player_state WHERE user_id=?`, pid).
		Scan(&currentXP, &rank, &rankXP); err != nil {
		return err
	}
	// Rank progression with the same thresholds as /internal/progression.
	rankXP += xp
	for {
		threshold := handlers.RankXPThreshold(rank + 1)
		if threshold <= 0 || rankXP < threshold {
			break
		}
		rankXP -= threshold
		rank++
	}
	if _, err := tx.Exec(`UPDATE player_state SET soft_currency=soft_currency+?, free_xp=free_xp+?,
		current_xp=current_xp+?, current_rank=?, rank_xp=?, updated_at=datetime('now') WHERE user_id=?`,
		credits, xp, xp, rank, rankXP, pid); err != nil {
		return err
	}
	if len(ships) > 0 {
		share := xp / int32(len(ships))
		for _, ship := range ships {
			if _, err := tx.Exec(`INSERT INTO player_ship_xp(user_id,ship_id,xp) VALUES(?,?,?)
				ON CONFLICT(user_id,ship_id) DO UPDATE SET xp=xp+?, updated_at=datetime('now')`, pid, ship, share, share); err != nil {
				return err
			}
		}
	}
	return nil
}

// battleResultCounter is the server's own count for a career-goal counter,
// from recorded results: matches played, won, and enemy ships destroyed.
func battleResultCounter(playerPID, counterID string) int32 {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return 0
	}
	var query string
	switch counterID {
	case "MatchesPlayed":
		query = `SELECT COUNT(*) FROM battle_results WHERE user_id=?`
	case "MatchesWon":
		query = `SELECT COUNT(*) FROM battle_results WHERE user_id=? AND outcome='win'`
	case "ShipsDestroyed":
		query = `SELECT COALESCE(SUM(kills),0) FROM battle_results WHERE user_id=?`
	default:
		return 0
	}
	var v int32
	if err := database.QueryRow(query, normalizedPlayerStatePID(playerPID)).Scan(&v); err != nil {
		return 0
	}
	return v
}
