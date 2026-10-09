package main

import (
	"database/sql"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

// Daily contracts: the player's own quests (the catalog the client resolves
// them against is mpquest_contracts.go).
//
// No player ever had one. The quest list the client reads -- "Quests" on the
// player object of YA_PlayerGet (0x2A70DA0 -> 0x2A69310), entries parsed by
// 0x2A706F0 -- was filled from player_contracts, which nothing ever wrote
// (0 rows on 2026-10-09): the only writer sat behind request names the client
// never sends (YA_CompleteContract, YA_RerollContract). And no progress was
// ever counted. The client tracks none itself either: YA_UpdateContract is a
// dead string in the exe (no code references it).
//
// What the client does, verified in the exe 2026-10-09:
//
//	entry   eid (string, the assignment's own id), id (the quest's m_id, matched
//	        case-insensitively against the collection), act, cpl (bool),
//	        prg (progress), dif (index into the quest's per-difficulty
//	        target/reward arrays, also handed to the quest via vtable +0x1E0),
//	        ran. Entries past m_numBaseContractSlots are the elite slot.
//	state   DailyContractStateID, LastContractsAssignment and
//	        DailyContractLastReplaceTime (Unix s) beside "Quests", on the
//	        player object and in the YA_ContractReplace reply (0x2A69310).
//	reroll  YA_ContractReplace {EntryID} (sender 0x2A3A9A0), offered while
//	        DailyContractLastReplaceTime is before the last reset (0x306700
//	        compares it with ContractNextResetTime - 1 day).
//	ack     YA_ContractRemove {EntryID} (sender 0x2A40B60); reply root
//	        "Contracts", the entry list.
//	push    YA_ContractRefresh: root "Contracts" (0x2A391E0).
//
// The rules, from the original game:
//   - "Every day, you can complete up to 3 contracts (4 if you have active
//     Elite Status)", "one contract per day can be rerolled", "Reach captain
//     rank 1 to unlock the daily contracts" (patch 1.4.1 notes); the elite
//     slot shows "BECOME ELITE TO ACTIVATE THIS CONTRACT" (client text).
//   - Contracts pay Credits ("You can earn Credits by playing matches and
//     completing contracts"), added to the rewards of the match that
//     completes them (Steam, the community team's answer on contract
//     credits). The amount is the quest's m_FPReward.
//   - What counts is each quest's own filters (mpquest_rules_gen.go).
//
// So a slot is refilled once per day: an empty slot (never filled, or its
// contract acknowledged or abandoned) gets a new contract when it was last
// filled before the most recent reset. GUESS: a contract still in progress
// is kept across the reset rather than replaced.

type mpQuestKind int

const (
	mpQuestPlayed mpQuestKind = iota // YMPQuest_GamePlayed
	mpQuestKills                     // YMPQuest_PlayerKills
	mpQuestScore                     // YMPQuest_EndOfMatchScore
)

type mpQuestRule struct {
	id          string
	kind        mpQuestKind
	counts      [3]int32
	rewards     [3]int32
	rank        int32
	outcomes    []string // battle_results outcomes that count; nil = any
	modes       []string // game modes that count; nil = any
	killed      []int32  // EYShipClass of the destroyed ship; nil = any player ship
	killer      []int32  // EYShipClass the player must fly; nil = any
	abilityOnly bool
}

// defaultObjectiveCounts is the target of a quest that does not set one (the
// three Win quests). GUESS: the native default is not traced; one win
// matches the lowest difficulty of the original's win contract ("winning 1
// battle will complete the contract", patch 1.1.0 notes).
var defaultObjectiveCounts = [3]int32{1, 1, 1}

// contractDifficulty is the "dif" every contract is given. The quests'
// targets and rewards are the same at every difficulty, so the choice only
// matters for documentation. GUESS: 0.
const contractDifficulty = 0

func mpQuestRuleByID(id string) (mpQuestRule, bool) {
	for _, r := range mpQuestRules {
		if strings.EqualFold(r.id, id) {
			return r, true
		}
	}
	return mpQuestRule{}, false
}

// contractKill is one kill the battle server reported: the destroyed ship's
// EYShipClass and the class the killer was flying.
type contractKill struct{ victim, killer int32 }

// contractMatch is what a finished match contributes to contracts.
type contractMatch struct {
	mode, outcome string
	kills         int32
	detail        []contractKill
	hasDetail     bool // the battle server reported per-kill detail
	score         int32
	hasScore      bool
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// increment is how much a match advances a contract of this quest.
func (r mpQuestRule) increment(m contractMatch) int32 {
	if len(r.modes) > 0 && !containsString(r.modes, m.mode) {
		return 0
	}
	switch r.kind {
	case mpQuestPlayed:
		if m.outcome == "" || m.outcome == "unknown" || (len(r.outcomes) > 0 && !containsString(r.outcomes, m.outcome)) {
			return 0
		}
		return 1
	case mpQuestKills:
		if r.abilityOnly {
			return 0 // module kills are not reported yet
		}
		if !m.hasDetail {
			if len(r.killed) == 0 && len(r.killer) == 0 {
				return m.kills
			}
			return 0
		}
		var n int32
		for _, k := range m.detail {
			if k.victim < 1 || k.victim > 15 { // only player ship classes count
				continue
			}
			if (len(r.killed) == 0 || containsInt32(r.killed, k.victim)) && (len(r.killer) == 0 || containsInt32(r.killer, k.killer)) {
				n++
			}
		}
		return n
	case mpQuestScore:
		if m.hasScore {
			return m.score
		}
	}
	return 0
}

// countable reports whether this server can count progress for the quest:
// module kills are never reported, and class filters and match scores only
// once the battle servers report per-kill detail (contractDetailReported).
func (r mpQuestRule) countable(detail bool) bool {
	switch {
	case r.abilityOnly:
		return false
	case r.kind == mpQuestScore:
		return detail
	case r.kind == mpQuestKills && (len(r.killed) > 0 || len(r.killer) > 0):
		return detail
	}
	return true
}

// --- server flag: battle servers report per-kill detail ----------------------

var contractDetail struct {
	once sync.Once
	on   atomic.Bool
}

func contractDetailReported() bool {
	contractDetail.once.Do(func() {
		if database := currentMmogPlayerStateDB(); database != nil {
			var v string
			if database.QueryRow(`SELECT value FROM server_flags WHERE name='contract_kill_detail'`).Scan(&v) == nil && v == "1" {
				contractDetail.on.Store(true)
			}
		}
	})
	return contractDetail.on.Load()
}

func markContractDetailReported(database *sql.DB) {
	if contractDetailReported() || database == nil {
		return
	}
	contractDetail.on.Store(true)
	_, _ = database.Exec(`INSERT OR REPLACE INTO server_flags(name,value) VALUES('contract_kill_detail','1')`)
	logrus.Info("contracts: the battle servers report per-kill detail -- class and score contracts are now offered")
}

// --- assignment ---------------------------------------------------------------

// contractsMu serialises contract changes: two requests for one player must
// not both fill the same slot.
var contractsMu sync.Mutex

func contractPeriodStart(now time.Time) time.Time {
	return nextContractReset(now).Add(-24 * time.Hour)
}

func contractSlots() int { return mpQuestNumBaseContractSlots + mpQuestNumEliteContractSlots }

type dailyContract struct {
	entryID    int64
	slot       int
	quest      string
	difficulty int32
	target     int32
	progress   int32
	reward     int32
	state      string
	assignedAt int64
}

func (c dailyContract) visible() bool { return c.state == "active" || c.state == "completed" }

func loadDailyContracts(database *sql.DB, pid string) ([]dailyContract, error) {
	rows, err := database.Query(`SELECT entry_id, slot, quest_id, difficulty, target, progress, reward, state, assigned_at
		FROM player_daily_contracts WHERE user_id=? ORDER BY slot, entry_id`, pid)
	if err != nil {
		return nil, err
	}
	var out []dailyContract
	for rows.Next() {
		var c dailyContract
		if rows.Scan(&c.entryID, &c.slot, &c.quest, &c.difficulty, &c.target, &c.progress, &c.reward, &c.state, &c.assignedAt) == nil {
			out = append(out, c)
		}
	}
	_ = rows.Close()
	return out, nil
}

// pickContractQuest draws a quest the player can be offered: rank allows it,
// this server can count it, and it is not already in another slot.
func pickContractQuest(rank int32, exclude map[string]bool, initial bool) (mpQuestRule, bool) {
	detail := contractDetailReported()
	var pool []mpQuestRule
	consider := func(r mpQuestRule) {
		if r.rank <= rank && r.countable(detail) && !exclude[strings.ToLower(r.id)] {
			pool = append(pool, r)
		}
	}
	if initial {
		for _, id := range mpQuestInitialContracts {
			if r, ok := mpQuestRuleByID(id); ok {
				consider(r)
			}
		}
	}
	if len(pool) == 0 {
		for _, r := range mpQuestRules {
			consider(r)
		}
	}
	if len(pool) == 0 {
		return mpQuestRule{}, false
	}
	return pool[rand.Intn(len(pool))], true
}

func insertDailyContract(database *sql.DB, pid string, slot int, r mpQuestRule, now time.Time) error {
	_, err := database.Exec(`INSERT INTO player_daily_contracts(user_id,slot,quest_id,difficulty,target,reward,assigned_at)
		VALUES(?,?,?,?,?,?,?)`, pid, slot, r.id, contractDifficulty, r.counts[contractDifficulty], r.rewards[contractDifficulty], now.Unix())
	return err
}

func bumpContractState(database *sql.DB, pid string, assigned, replaced int64) {
	_, _ = database.Exec(`INSERT INTO player_contract_state(user_id,state_id,last_assignment,last_replace) VALUES(?,1,?,?)
		ON CONFLICT(user_id) DO UPDATE SET state_id=state_id+1,
			last_assignment=CASE WHEN excluded.last_assignment>0 THEN excluded.last_assignment ELSE last_assignment END,
			last_replace=CASE WHEN excluded.last_replace>0 THEN excluded.last_replace ELSE last_replace END`,
		pid, assigned, replaced)
}

// dailyContractsEnabled is the DN_DAILY_CONTRACTS switch: "0" assigns no
// contracts and counts nothing (the client then shows empty slots).
func dailyContractsEnabled() bool { return os.Getenv("DN_DAILY_CONTRACTS") != "0" }

// ensureDailyContracts fills every empty slot that was last filled before the
// most recent reset. Reports whether anything was assigned. The caller holds
// contractsMu.
func ensureDailyContracts(database *sql.DB, pid string, now time.Time) bool {
	if database == nil || pid == "" || !dailyContractsEnabled() {
		return false
	}
	rank := mmogPlayerStateForPID(pid).currentRank
	if rank < 1 { // "Reach captain rank 1 to unlock the daily contracts"
		return false
	}
	all, err := loadDailyContracts(database, pid)
	if err != nil {
		return false
	}
	periodStart := contractPeriodStart(now).Unix()
	visible := map[int]bool{}
	lastFilled := map[int]int64{}
	exclude := map[string]bool{}
	for _, c := range all {
		if c.visible() {
			visible[c.slot] = true
			exclude[strings.ToLower(c.quest)] = true
		}
		if c.assignedAt > lastFilled[c.slot] {
			lastFilled[c.slot] = c.assignedAt
		}
	}
	assigned := false
	for slot := 0; slot < contractSlots(); slot++ {
		if visible[slot] || (lastFilled[slot] != 0 && lastFilled[slot] >= periodStart) {
			continue
		}
		r, ok := pickContractQuest(rank, exclude, len(all) == 0)
		if !ok {
			break
		}
		if insertDailyContract(database, pid, slot, r, now) != nil {
			break
		}
		exclude[strings.ToLower(r.id)] = true
		assigned = true
	}
	if assigned {
		bumpContractState(database, pid, now.Unix(), 0)
	}
	return assigned
}

type contractState struct {
	stateID, lastAssignment, lastReplace int64
	entries                              []dailyContract
}

// currentContracts makes sure the slots are filled and returns the state.
func currentContracts(pid string, now time.Time) contractState {
	var st contractState
	database := currentMmogPlayerStateDB()
	pid = normalizedPlayerStatePID(pid)
	if database == nil {
		return st
	}
	contractsMu.Lock()
	defer contractsMu.Unlock()
	ensureDailyContracts(database, pid, now)
	return readContractState(database, pid)
}

func readContractState(database *sql.DB, pid string) contractState {
	var st contractState
	_ = database.QueryRow(`SELECT state_id, last_assignment, last_replace FROM player_contract_state WHERE user_id=?`, pid).
		Scan(&st.stateID, &st.lastAssignment, &st.lastReplace)
	all, _ := loadDailyContracts(database, pid)
	for _, c := range all {
		if c.visible() {
			st.entries = append(st.entries, c)
		}
	}
	return st
}

// --- reroll, acknowledge --------------------------------------------------------

// replaceDailyContract rerolls an active contract, once per day.
func replaceDailyContract(pid string, entryID int64, now time.Time) (bool, string) {
	database := currentMmogPlayerStateDB()
	pid = normalizedPlayerStatePID(pid)
	if database == nil {
		return false, "no database"
	}
	contractsMu.Lock()
	defer contractsMu.Unlock()
	st := readContractState(database, pid)
	if st.lastReplace >= contractPeriodStart(now).Unix() {
		return false, "already rerolled today"
	}
	var target *dailyContract
	exclude := map[string]bool{}
	for i, c := range st.entries {
		exclude[strings.ToLower(c.quest)] = true
		if c.entryID == entryID {
			target = &st.entries[i]
		}
	}
	if target == nil || target.state != "active" {
		return false, "no such active contract"
	}
	r, ok := pickContractQuest(mmogPlayerStateForPID(pid).currentRank, exclude, false)
	if !ok {
		return false, "no other contract available"
	}
	if _, err := database.Exec(`UPDATE player_daily_contracts SET state='replaced' WHERE entry_id=? AND user_id=?`, entryID, pid); err != nil {
		return false, err.Error()
	}
	if err := insertDailyContract(database, pid, target.slot, r, now); err != nil {
		return false, err.Error()
	}
	bumpContractState(database, pid, now.Unix(), now.Unix())
	return true, r.id
}

// removeDailyContract is the player acknowledging a completed contract (or
// dropping one): the slot stays empty until the next reset.
func removeDailyContract(pid string, entryID int64) bool {
	database := currentMmogPlayerStateDB()
	pid = normalizedPlayerStatePID(pid)
	if database == nil {
		return false
	}
	contractsMu.Lock()
	defer contractsMu.Unlock()
	res, err := database.Exec(`UPDATE player_daily_contracts SET state=CASE state WHEN 'completed' THEN 'acknowledged' ELSE 'removed' END
		WHERE entry_id=? AND user_id=? AND state IN ('active','completed')`, entryID, pid)
	if err != nil {
		return false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false
	}
	bumpContractState(database, pid, 0, 0)
	return true
}

// --- progress -----------------------------------------------------------------

// applyContractProgress counts a finished match against the player's
// contracts, pays completed ones in credits and reports the credits paid and
// whether anything changed. The elite slot counts only with Elite Status.
func applyContractProgress(pid string, m contractMatch, now time.Time) (int32, bool) {
	database := currentMmogPlayerStateDB()
	pid = normalizedPlayerStatePID(pid)
	if database == nil || !dailyContractsEnabled() {
		return 0, false
	}
	elite := int64(membershipExpiresAt(pid)) > now.Unix()
	contractsMu.Lock()
	defer contractsMu.Unlock()
	ensureDailyContracts(database, pid, now)
	st := readContractState(database, pid)
	var credits int32
	changed := false
	for _, c := range st.entries {
		if c.state != "active" || (c.slot >= mpQuestNumBaseContractSlots && !elite) {
			continue
		}
		r, ok := mpQuestRuleByID(c.quest)
		if !ok {
			continue
		}
		inc := r.increment(m)
		if inc <= 0 {
			continue
		}
		progress := c.progress + inc
		if r.kind == mpQuestScore { // one match's score, not a sum
			progress = max(c.progress, inc)
		}
		if progress > c.target {
			progress = c.target
		}
		state, completedAt := "active", int64(0)
		if progress >= c.target {
			state, completedAt = "completed", now.Unix()
			credits += c.reward
		}
		if _, err := database.Exec(`UPDATE player_daily_contracts SET progress=?, state=?, completed_at=? WHERE entry_id=?`,
			progress, state, completedAt, c.entryID); err != nil {
			continue
		}
		changed = true
		logrus.WithFields(logrus.Fields{"player": pid, "contract": c.quest, "progress": progress, "target": c.target,
			"state": state}).Info("contracts: progress")
	}
	if credits > 0 {
		_, _ = database.Exec(`UPDATE player_state SET soft_currency=soft_currency+?, updated_at=datetime('now') WHERE user_id=?`, credits, pid)
	}
	if changed {
		bumpContractState(database, pid, 0, 0)
	}
	return credits, changed
}

// --- wire -----------------------------------------------------------------------

func appendContractEntries(b []byte, stack []int, name string, entries []dailyContract) ([]byte, []int) {
	b, stack = protocol.AppendArrayStart(b, stack, name)
	for _, c := range entries {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "eid", strconv.FormatInt(c.entryID, 10))
		b = protocol.AppendStringField(b, "id", c.quest)
		b = protocol.AppendStringField(b, "act", "1")
		b = protocol.AppendStringField(b, "cpl", boolToOneZero(c.state == "completed"))
		b = protocol.AppendStringField(b, "prg", strconv.Itoa(int(c.progress)))
		b = protocol.AppendStringField(b, "dif", strconv.Itoa(int(c.difficulty)))
		b = protocol.AppendStringField(b, "ran", "0")
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	return protocol.AppendObjectEnd(b, stack)
}

// appendContractStateFields writes the four contract fields the client reads
// together (0x2A69310): on the player object and in the replace reply.
func appendContractStateFields(b []byte, stack []int, st contractState) ([]byte, []int) {
	b = protocol.AppendStringField(b, "DailyContractStateID", strconv.FormatInt(st.stateID, 10))
	b = protocol.AppendStringField(b, "LastContractsAssignment", strconv.FormatInt(st.lastAssignment, 10))
	b = protocol.AppendStringField(b, "DailyContractLastReplaceTime", strconv.FormatInt(st.lastReplace, 10))
	return appendContractEntries(b, stack, "Quests", st.entries)
}

func contractEntryID(payload []byte) int64 {
	id, _ := strconv.ParseInt(strings.TrimSpace(protocol.FirstNonEmptyString(payload, "EntryID", "entryId", "eid")), 10, 64)
	return id
}

// buildMmogContractReplacePayload answers YA_ContractReplace (reroll): the
// whole contract state, at the root (0x2A2F23E hands the root to 0x2A69310).
func buildMmogContractReplacePayload(pid string, payload []byte) []byte {
	now := time.Now()
	ok, detail := replaceDailyContract(pid, contractEntryID(payload), now)
	logrus.WithFields(logrus.Fields{"player": normalizedPlayerStatePID(pid), "entry": contractEntryID(payload), "ok": ok,
		"detail": detail}).Info("contracts: reroll")
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_ContractReplace")
	b, _ = appendContractStateFields(b, stack, currentContracts(pid, now))
	return b
}

// buildMmogContractRemovePayload answers YA_ContractRemove (acknowledge):
// the entry list as root "Contracts".
func buildMmogContractRemovePayload(pid string, payload []byte) []byte {
	removeDailyContract(pid, contractEntryID(payload))
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_ContractRemove")
	b, _ = appendContractEntries(b, stack, "Contracts", currentContracts(pid, time.Now()).entries)
	return b
}

// buildMmogContractRefreshPush is the in-session update after a match or a
// reset: root "Contracts" (0x2A391E0).
func buildMmogContractRefreshPush(pid string) []byte {
	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_ContractRefresh")
	b, _ = appendContractEntries(b, stack, "Contracts", currentContracts(pid, time.Now()).entries)
	return b
}

// parseContractKills reads the battle server's per-kill report: kl=
// "victim.killer,victim.killer" (EYShipClass numbers).
func parseContractKills(v string) []contractKill {
	var out []contractKill
	for _, part := range strings.Split(v, ",") {
		a, b, ok := strings.Cut(strings.TrimSpace(part), ".")
		if !ok {
			continue
		}
		victim, err1 := strconv.Atoi(a)
		killer, err2 := strconv.Atoi(b)
		if err1 == nil && err2 == nil {
			out = append(out, contractKill{victim: int32(victim), killer: int32(killer)})
		}
	}
	return out
}
