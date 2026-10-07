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
	// freeXPPct is the share of a match's XP that is ALSO paid as free XP.
	// The rest is ship XP only. CHANGED 2026-09-29: free XP was 100% of the
	// match XP ("the free xp should be way less than that maybe 25 % of the
	// total XP", operator). GUESS: the operator's figure; the client's own
	// XPStandardFreeXpPercentage (YA_GetProgressionData) is not sent or traced.
	freeXPPct int32
	// unplayedShipXPPct is what every ship of the match's fleet that was NOT
	// flown earns: this share of the match's XP WITHOUT the performance part,
	// so the performance reward goes only to the ships played. The game's own
	// text: "Whichever fleet you choose to play ... every ship in that fleet
	// will earn Ship XP after you finish a match." GUESS: 10% is the
	// operator's figure (2026-09-30); only flown ships earned anything before.
	unplayedShipXPPct int32
	// eventXP is the match's XP from the ORIGINAL scoring table when the
	// battle server reported it (event_xp, summed by battle-server-mod from
	// AssignScoringReward, plus the end-of-match awards from
	// endOfMatchEventXP). It replaces the win/loss + per-kill XP base;
	// credits keep the formula (the original credit payout, ScoringParamsTable
	// CrBuckets, is not in the datamine). GUESS: the formula's bonus terms and
	// fleet bonus still apply on top, as they did to the placeholder base.
	eventXP    int32
	useEventXP bool
}

// withEventXP is r paying xp as the match's XP base (see eventXP).
func (r battleRewards) withEventXP(xp int32) battleRewards {
	if xp < 0 {
		xp = 0
	}
	r.eventXP, r.useEventXP = xp, true
	return r
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
		freeXPPct:     n("DN_REWARD_FREE_XP_PERCENT", 25),

		unplayedShipXPPct: n("DN_REWARD_UNPLAYED_SHIP_XP_PERCENT", 10),
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
	c, x := r.poolsFor(outcome, kills, fleetType)
	return c.total(), x.total()
}

// rewardPools is a payout split the way the client's end-of-match screen
// shows it: one amount per EYXPPoolType (registration 0x6A3CFE):
//
//	0 Scoring  1 ScoringBase  2 ScoringPerformance  3 BoosterWin
//	4 BoosterFirstWinOfTheDay  5 GoldMembership  6 TeammatesGoldMembership
//	7 BattleReadyRecruit  8 BattleReadyVeteran  9 BattleReadyLegendary
//	10 FreeXPFleetBonus  11 FreeXPFleetBonusBoosted  12 None
//
// The client allocates 13 (0x0D) entries and totals every pool except 1-2
// (the breakdown of 0) and 10-11 (0x3FAFA0), so Scoring carries base +
// performance. The operator's formula maps onto it: Intermediate = base +
// performance -> pools 1, 2 and 0; the bonus terms -> 5 (1.25 XP / 0.75
// credits) and 6 (0.25); the fleet battle bonus -> 7/8/9 by fleet type.
// GUESS: which named pool each operator term is (the numbers fit Gold /
// Teammates-gold / BattleReady; nothing states it); EliteTeam% and any extra
// bonus term go to 3 (BoosterWin).
const rewardPoolCount = 13

type rewardPools [rewardPoolCount]int32

// total is what the player is paid: every pool the client sums.
func (p rewardPools) total() int32 {
	var t int32
	for i, v := range p {
		if i == 1 || i == 2 || i == 10 || i == 11 {
			continue
		}
		t += v
	}
	return t
}

// freeXPOf is the free XP a match's XP pays.
func (r battleRewards) freeXPOf(xp int32) int32 {
	return int32(math.Round(float64(xp) * float64(r.freeXPPct) / 100))
}

// scaled is p with every pool multiplied by pct/100, rounding differences
// going to pool 0 so the counted total is exactly want.
func (p rewardPools) scaled(pct int32, want int32) rewardPools {
	var q rewardPools
	for i, v := range p {
		q[i] = int32(math.Round(float64(v) * float64(pct) / 100))
	}
	q[0] += want - q.total()
	return q
}

// unplayedShipPools is what each fleet ship that was not flown earns: the
// match's XP pools without the performance part, scaled to unplayedShipXPPct.
func (r battleRewards) unplayedShipPools(outcome string, fleetType int) rewardPools {
	_, base := r.poolsFor(outcome, 0, fleetType)
	want := int32(math.Round(float64(base.total()) * float64(r.unplayedShipXPPct) / 100))
	return base.scaled(r.unplayedShipXPPct, want)
}

func (p rewardPools) csv() string {
	parts := make([]string, len(p))
	for i, v := range p {
		parts[i] = strconv.Itoa(int(v))
	}
	return strings.Join(parts, ",")
}

func (r battleRewards) poolsFor(outcome string, kills int32, fleetType int) (credits, xp rewardPools) {
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
	credits = r.split(baseCredits, r.killCredits*kills, r.creditBonuses, fleetType)
	if r.useEventXP {
		// All of it as base: the event XP is not split into a win/loss base
		// and a kill performance, and unplayedShipPools takes its share of
		// the base.
		return credits, r.split(r.eventXP, 0, r.xpBonuses, fleetType)
	}
	xp = r.split(baseXP, r.killXP*kills, r.xpBonuses, fleetType)
	return credits, xp
}

// split builds the pools for one currency. The total is the formula rounded
// once; per-pool rounding differences go to the fleet pool, so the pools
// always add up to exactly what is paid.
func (r battleRewards) split(base, performance int32, bonuses []float64, fleetType int) rewardPools {
	var p rewardPools
	intermediate := base + performance
	p[0], p[1], p[2] = intermediate, base, performance
	part := func(f float64) int32 { return int32(math.Round(float64(intermediate) * f)) }
	for i, b := range bonuses {
		switch i {
		case 0:
			p[5] += part(b)
		case 1:
			p[6] += part(b)
		default:
			p[3] += part(b)
		}
	}
	p[3] += part(r.eliteTeamPct / 100)
	ft := fleetType
	if ft < 1 || ft > 3 {
		ft = 1
	}
	fleetPool := 6 + ft // 7 Recruit, 8 Veteran, 9 Legendary
	p[fleetPool] += part(r.fleetBonus(fleetType))
	want := int32(math.Round(float64(intermediate) * r.multiplier(bonuses, fleetType)))
	p[fleetPool] += want - p.total()
	return p
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
	rewards := currentBattleRewards()
	var eventXP, eomXP int32
	scoringMode := ""
	if v := strings.TrimSpace(q.Get("event_xp")); v != "" && os.Getenv("DN_REWARD_EVENT_XP") != "0" {
		eventXP = int32(num("event_xp"))
		eomXP, scoringMode = endOfMatchEventXP(currentMmogPlayerStateDB(), match)
		rewards = rewards.withEventXP(eventXP + eomXP)
	}
	credits, xp, gains, fresh, err := recordBattleResult(res, rewards)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{"match": match, "player": pid}).Error("battle result: not recorded")
		http.Error(w, "not recorded", http.StatusInternalServerError)
		return
	}
	if fresh {
		// The hangar's credits only change through YA_RewardCurrencies, whose
		// handler ASSIGNS Credits/Points (buildMmogRewardCurrenciesPayload); it
		// was sent at login only, so the hangar kept the pre-match balance
		// until a restart (operator, 2026-09-29).
		squadHubInstance.push(pid, buildMmogRewardCurrenciesPayload(pid))
		// ...and free XP and ship XP, which reach the client only through
		// YA_PlayerGet (login) and never refreshed after a match ("the free xp
		// is not getting updated ... i need to restart the game", operator
		// 2026-09-30). See buildMmogShipXPSyncPush.
		squadHubInstance.push(pid, buildMmogShipXPSyncPush(pid, gains))
	}
	logrus.WithFields(logrus.Fields{"match": match, "player": pid, "outcome": res.outcome, "fleet_type": res.fleetType, "kills": res.kills,
		"deaths": res.deaths, "credits": credits, "xp": xp, "ships": res.ships, "new": fresh,
		"event_xp": eventXP, "eom_xp": eomXP, "scoring_mode": scoringMode, "events": q.Get("events"),
		"event_credits": q.Get("event_credits")}).Info("battle result")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprintf(w, "outcome=%s\ncredits=%d\nxp=%d\nnew=%v\n", res.outcome, credits, xp, fresh)
	// The same payout split into pools, for the end-of-match screen: the mod
	// writes these into the player's m_creditsInfo / m_matchXPInfo (the data
	// the missing server build used to fill), so the screen shows what was
	// paid. See rewardPools.
	database := currentMmogPlayerStateDB()
	fleetType := 0
	if database != nil {
		fleetType = matchFleetType(database, res.match)
	}
	creditPools, xpPools := rewards.poolsFor(res.outcome, res.kills, fleetType)
	flown := flownFleetShipIDs(res.pid, res.ships)
	shipPools := xpPools
	if len(flown) > 1 {
		for i := range shipPools {
			shipPools[i] /= int32(len(flown))
		}
	}
	// xp_pools become the screen's FREE XP (m_matchXPInfo.m_freeXP): the
	// free share of the match XP, not all of it. Ship XP keeps the full split.
	freePools := xpPools.scaled(rewards.freeXPPct, rewards.freeXPOf(xpPools.total()))
	// unplayed_ship_xp_pools is what every fleet ship NOT flown earned (see
	// unplayedShipXPPct); the mod shows it on those ships, which read 0 before.
	_, _ = fmt.Fprintf(w, "credit_pools=%s\nxp_pools=%s\nship_xp_pools=%s\nunplayed_ship_xp_pools=%s\nfleet_ships=%s\nflown_ships=%s\n",
		creditPools.csv(), freePools.csv(), shipPools.csv(), rewards.unplayedShipPools(res.outcome, fleetType).csv(),
		joinInt32s(battleFleetShipIDs(res.pid, fleetType)), joinInt32s(flown))
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
// gains is the ship XP each ship earned, by pawn id.
func recordBattleResult(res battleResult, rewards battleRewards) (credits, xp int32, gains map[int32]int32, fresh bool, err error) {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return 0, 0, nil, false, fmt.Errorf("database unavailable")
	}
	if err := seedMmogPlayerState(database, res.pid); err != nil {
		return 0, 0, nil, false, err
	}
	res.fleetType = matchFleetType(database, res.match)
	credits, xp = rewards.forOutcome(res.outcome, res.kills, res.fleetType)

	// Ship XP goes to the hulls actually flown, resolved to pawn ids the way
	// player_ship_xp keys them, and a smaller share to the rest of the fleet.
	// Resolved BEFORE the transaction: the store has one connection, and a
	// query inside an open transaction waits for itself.
	ships := flownShipIDs(res.pid, res.ships)
	unplayed := unplayedFleetPawnIDs(res.pid, res.fleetType, ships)
	unplayedXP := rewards.unplayedShipPools(res.outcome, res.fleetType).total()

	gains = map[int32]int32{}
	if len(ships) > 0 {
		for _, ship := range ships {
			gains[ship] += xp / int32(len(ships))
		}
	}
	for _, ship := range unplayed {
		gains[ship] += unplayedXP
	}

	tx, err := database.Begin()
	if err != nil {
		return 0, 0, nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	ins, err := tx.Exec(`INSERT OR IGNORE INTO battle_results(match_id,user_id,team,outcome,kills,deaths,assists,damage,credits,xp,fleet_type)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, res.match, res.pid, res.team, res.outcome, res.kills, res.deaths, res.assists, res.damage, credits, xp, res.fleetType)
	if err != nil {
		return 0, 0, nil, false, err
	}
	if n, _ := ins.RowsAffected(); n == 0 {
		return credits, xp, nil, false, nil // already paid
	}
	if err := grantBattleRewards(tx, res.pid, credits, xp, rewards.freeXPOf(xp), ships, unplayed, unplayedXP); err != nil {
		return 0, 0, nil, false, err
	}
	return credits, xp, gains, true, tx.Commit()
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

// flownShipIDs resolves the loadout ids a player picked to pawn (ship item)
// ids, once each.
func flownShipIDs(pid string, loadoutIDs []string) []int32 {
	var ships []int32
	seen := map[int32]bool{}
	for _, id := range loadoutIDs {
		if loadout, ok := battleLoadoutFor(pid, id); ok && loadout.ship.id != 0 && !seen[loadout.ship.id] {
			seen[loadout.ship.id] = true
			ships = append(ships, loadout.ship.id)
		}
	}
	return ships
}

// battleFleetShipIDs lists the ships of the fleet the player fought with (the
// active fleet of the match's fleet type), by FLEET SHIP id. The client's
// rewards screen walks every ship of its active fleet and looks each up by
// that id (GatherEomRewardsData 0x340C40 -> 0x3FB0D0); a ship with no entry
// is reported as "Ship XP pools were not gathered correctly. ... Ship ID {1}"
// (YA_LogSpecial, client_reports).
//
// VERIFIED 2026-09-28: keyed by pawn id (184483981...), the client filed
// ShipXpError for Ship IDs 33489265/67/68/69/70 -- exactly the fleetShipID
// (= precast loadout id) of the five ships in that player's active fleet.
func battleFleetShipIDs(pid string, fleetType int) []int32 {
	var ids []int32
	seen := map[int32]bool{}
	for _, l := range battleFleetLoadouts(pid, fleetType) {
		if id := fleetShipKey(l); id != 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// battleFleetLoadouts is the fleet the player fought with: the active fleet
// of the match's fleet type.
//
// Chosen among ALL the player's fleets, not the "active" ones: active marks
// the single fleet selected in the hangar, so Veteran and Legendary fleets are
// normally inactive (every account, measured 2026-10-01). Looking only at
// active fleets found no fleet of type 2 or 3, fell back to the Recruit
// fleet, and after every Veteran match the rewards were written for the
// Recruit ships -- the client then reported "Ship XP pools were not gathered
// correctly" for each Veteran ship it fielded (client reports, 2026-10-01),
// and the unflown-ship XP went to ships that were not in the match.
func battleFleetLoadouts(pid string, fleetType int) []mmogShipLoadoutSeed {
	state := mmogPlayerStateForPID(pid)
	var match *mmogFleetSeed
	for i := range state.fleets {
		if int(state.fleets[i].fleetType) != fleetType {
			continue
		}
		if match == nil || state.fleets[i].active {
			match = &state.fleets[i]
		}
	}
	if match != nil {
		return match.shipLoadouts
	}
	return state.activeFleet().shipLoadouts
}

// unplayedFleetPawnIDs is the fleet's ships that were not flown, by pawn id
// (how player_ship_xp keys them), once each.
func unplayedFleetPawnIDs(pid string, fleetType int, flown []int32) []int32 {
	skip := map[int32]bool{}
	for _, id := range flown {
		skip[id] = true
	}
	var ids []int32
	for _, l := range battleFleetLoadouts(pid, fleetType) {
		if id := l.ship.id; id != 0 && !skip[id] {
			skip[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func fleetShipKey(l mmogShipLoadoutSeed) int32 {
	if l.fleetShipID != 0 {
		return l.fleetShipID
	}
	return l.precastLoadoutID
}

// flownFleetShipIDs is flownShipIDs keyed the way the rewards screen looks
// ships up (fleet ship id), not by pawn id.
func flownFleetShipIDs(pid string, loadoutIDs []string) []int32 {
	var ids []int32
	seen := map[int32]bool{}
	for _, id := range loadoutIDs {
		if loadout, ok := battleLoadoutFor(pid, id); ok {
			if k := fleetShipKey(loadout); k != 0 && !seen[k] {
				seen[k] = true
				ids = append(ids, k)
			}
		}
	}
	return ids
}

func joinInt32s(v []int32) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.Itoa(int(x))
	}
	return strings.Join(parts, ",")
}

func grantBattleRewards(tx *sql.Tx, pid string, credits, xp, freeXP int32, ships, unplayed []int32, unplayedXP int32) error {
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
		credits, freeXP, xp, rank, rankXP, pid); err != nil {
		return err
	}
	addShipXP := func(ship, amount int32) error {
		if amount <= 0 {
			return nil
		}
		_, err := tx.Exec(`INSERT INTO player_ship_xp(user_id,ship_id,xp) VALUES(?,?,?)
			ON CONFLICT(user_id,ship_id) DO UPDATE SET xp=xp+?, updated_at=datetime('now')`, pid, ship, amount, amount)
		return err
	}
	if len(ships) > 0 {
		share := xp / int32(len(ships))
		for _, ship := range ships {
			if err := addShipXP(ship, share); err != nil {
				return err
			}
		}
	}
	for _, ship := range unplayed {
		if err := addShipXP(ship, unplayedXP); err != nil {
			return err
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
