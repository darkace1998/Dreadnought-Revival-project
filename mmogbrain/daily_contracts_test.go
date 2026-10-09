package main

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

const contractTestPID = "0000000000000000000000000000c0de"

func contractTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database := useTempMmogPlayerStateDB(t)
	if err := seedMmogPlayerState(database, contractTestPID); err != nil {
		t.Fatal(err)
	}
	contractDetail.on.Store(false)
	t.Cleanup(func() { contractDetail.on.Store(false) })
	return database
}

// setContracts replaces the player's contracts with these quests, one per
// slot from 0, assigned at the given time.
func setContracts(t *testing.T, database *sql.DB, at time.Time, quests ...string) {
	t.Helper()
	if _, err := database.Exec(`DELETE FROM player_daily_contracts WHERE user_id=?`, contractTestPID); err != nil {
		t.Fatal(err)
	}
	for slot, q := range quests {
		r, ok := mpQuestRuleByID(q)
		if !ok {
			t.Fatalf("no quest %s", q)
		}
		if err := insertDailyContract(database, contractTestPID, slot, r, at); err != nil {
			t.Fatal(err)
		}
	}
}

func contractBySlot(t *testing.T, slot int) dailyContract {
	t.Helper()
	for _, c := range readContractState(currentMmogPlayerStateDB(), contractTestPID).entries {
		if c.slot == slot {
			return c
		}
	}
	t.Fatalf("no contract in slot %d", slot)
	return dailyContract{}
}

// A new player gets all four slots (3 + elite), from the collection's initial
// contracts, each a quest this server can count.
func TestNewPlayerGetsDailyContracts(t *testing.T) {
	contractTestDB(t)
	st := currentContracts(contractTestPID, time.Now())
	if len(st.entries) != contractSlots() {
		t.Fatalf("%d contracts, want %d", len(st.entries), contractSlots())
	}
	seen := map[string]bool{}
	for _, c := range st.entries {
		r, ok := mpQuestRuleByID(c.quest)
		if !ok || !r.countable(false) {
			t.Errorf("slot %d: %s cannot be counted without kill detail", c.slot, c.quest)
		}
		if seen[c.quest] {
			t.Errorf("%s offered twice", c.quest)
		}
		seen[c.quest] = true
		if c.target != r.counts[0] || c.reward != r.rewards[0] {
			t.Errorf("%s: target %d reward %d, want %d/%d", c.quest, c.target, c.reward, r.counts[0], r.rewards[0])
		}
	}
	// The player object carries them the way the client reads them.
	payload := buildMmogPlayerGetPayload(contractTestPID)
	quests := extractNamedMmogArray(t, payload, "Quests")
	for _, c := range st.entries {
		if !bytes.Contains(quests, protocol.AppendStringField(nil, "id", c.quest)) ||
			!bytes.Contains(quests, protocol.AppendStringField(nil, "eid", strconv.FormatInt(c.entryID, 10))) {
			t.Errorf("YA_PlayerGet Quests lacks %s (eid %d)", c.quest, c.entryID)
		}
	}
	// Asking again assigns nothing new.
	if again := currentContracts(contractTestPID, time.Now()); len(again.entries) != len(st.entries) || again.stateID != st.stateID {
		t.Error("a second read reassigned contracts")
	}
}

// Matches advance contracts by their own filters; a completed one pays its
// credits once; the elite slot counts only with Elite Status.
func TestContractProgressAndPayout(t *testing.T) {
	database := contractTestDB(t)
	now := time.Now()
	setContracts(t, database, now, "YMPQ_CompleteMatches", "YMPQ_WinMatchesTER", "YMPQ_Kills", "YMPQ_WinMatches")
	before := mmogPlayerStateForPID(contractTestPID).softCurrency

	tdmLoss := contractMatch{mode: "TDM", outcome: "loss", kills: 4}
	if credits, changed := applyContractProgress(contractTestPID, tdmLoss, now); !changed || credits != 0 {
		t.Fatalf("first match: credits %d changed %v", credits, changed)
	}
	if c := contractBySlot(t, 0); c.progress != 1 || c.state != "active" {
		t.Errorf("CompleteMatches after one match: %+v", c)
	}
	if c := contractBySlot(t, 1); c.progress != 0 {
		t.Errorf("a TDM loss counted for Conquest wins: %+v", c)
	}
	if c := contractBySlot(t, 2); c.progress != 4 {
		t.Errorf("Kills after 4 kills: %+v", c)
	}
	if c := contractBySlot(t, 3); c.progress != 0 {
		t.Errorf("the elite slot counted without Elite Status: %+v", c)
	}

	credits, _ := applyContractProgress(contractTestPID, contractMatch{mode: "TER", outcome: "win", kills: 2}, now)
	if want := int32(5000 * 3); credits != want {
		t.Fatalf("second match paid %d, want %d (CompleteMatches, WinMatchesTER, Kills)", credits, want)
	}
	for slot := 0; slot < 3; slot++ {
		if c := contractBySlot(t, slot); c.state != "completed" || c.progress != c.target {
			t.Errorf("slot %d not completed: %+v", slot, c)
		}
	}
	if got := mmogPlayerStateForPID(contractTestPID).softCurrency - before; got != 15000 {
		t.Errorf("balance grew by %d, want 15000", got)
	}
	// Completed contracts are not paid again.
	if credits, _ := applyContractProgress(contractTestPID, contractMatch{mode: "TER", outcome: "win", kills: 9}, now); credits != 0 {
		t.Errorf("paid %d again", credits)
	}

	// With Elite Status the elite slot counts.
	if _, err := database.Exec(`INSERT OR REPLACE INTO player_membership(user_id, expires_at) VALUES(?, ?)`, contractTestPID,
		now.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if credits, _ := applyContractProgress(contractTestPID, contractMatch{mode: "TDM", outcome: "win"}, now); credits != 5000 {
		t.Errorf("elite WinMatches paid %d, want 5000", credits)
	}
}

// One reroll per day; acknowledging leaves the slot empty until the reset,
// which refills it.
func TestContractRerollAcknowledgeAndReset(t *testing.T) {
	database := contractTestDB(t)
	day1 := time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)
	setContracts(t, database, day1, "YMPQ_CompleteMatches", "YMPQ_WinMatches", "YMPQ_Kills", "YMPQ_CompleteMatchesONS")

	first := contractBySlot(t, 1)
	if ok, why := replaceDailyContract(contractTestPID, first.entryID, day1); !ok {
		t.Fatalf("first reroll refused: %s", why)
	}
	rerolled := contractBySlot(t, 1)
	if rerolled.entryID == first.entryID || strings.EqualFold(rerolled.quest, first.quest) {
		t.Fatalf("reroll kept the contract: %+v", rerolled)
	}
	if ok, _ := replaceDailyContract(contractTestPID, contractBySlot(t, 2).entryID, day1.Add(time.Hour)); ok {
		t.Fatal("a second reroll on the same day was allowed")
	}
	if ok, why := replaceDailyContract(contractTestPID, contractBySlot(t, 2).entryID, day1.Add(24*time.Hour)); !ok {
		t.Fatalf("reroll after the reset refused: %s", why)
	}

	// Complete slot 0 and acknowledge it.
	applyContractProgress(contractTestPID, contractMatch{mode: "TDM", outcome: "win"}, day1)
	applyContractProgress(contractTestPID, contractMatch{mode: "TDM", outcome: "win"}, day1)
	done := contractBySlot(t, 0)
	if done.state != "completed" {
		t.Fatalf("slot 0 not completed: %+v", done)
	}
	if !removeDailyContract(contractTestPID, done.entryID) {
		t.Fatal("acknowledge failed")
	}
	if n := len(currentContracts(contractTestPID, day1.Add(time.Hour)).entries); n != contractSlots()-1 {
		t.Fatalf("%d contracts after acknowledging, want the slot empty until the reset", n)
	}
	next := currentContracts(contractTestPID, day1.Add(24*time.Hour))
	if len(next.entries) != contractSlots() {
		t.Fatalf("%d contracts after the reset, want the slot refilled", len(next.entries))
	}

	// The wire replies.
	reply := buildMmogContractRemovePayload(contractTestPID, protocol.AppendStringField(nil, "EntryID", "999999"))
	if !bytes.Contains(reply, appendFieldMarker("Contracts", 0x0d)) {
		t.Error("YA_ContractRemove reply lacks Contracts")
	}
	replace := buildMmogContractReplacePayload(contractTestPID, protocol.AppendStringField(nil, "EntryID", "999999"))
	for _, f := range []string{"DailyContractStateID", "LastContractsAssignment", "DailyContractLastReplaceTime"} {
		if !bytes.Contains(replace, appendFieldMarker(f, 0x09)) {
			t.Errorf("YA_ContractReplace reply lacks %s", f)
		}
	}
}

// Per-kill detail from the battle server: class filters count, and its
// presence makes the class contracts available.
func TestContractKillDetail(t *testing.T) {
	contractTestDB(t)
	destroyCorvette, _ := mpQuestRuleByID("YMPQ_DestroyCorvette")
	killsInCorvette, _ := mpQuestRuleByID("YMPQ_KillsCorvette")
	kills, _ := mpQuestRuleByID("YMPQ_Kills")
	if destroyCorvette.countable(false) || !destroyCorvette.countable(true) {
		t.Fatal("class contracts must wait for kill detail")
	}
	detail := parseContractKills("2.7,8.7,1.2,29.2")
	m := contractMatch{mode: "TDM", outcome: "win", kills: 4, detail: detail, hasDetail: true}
	if n := destroyCorvette.increment(m); n != 2 {
		t.Errorf("DestroyCorvette counted %d, want 2 (victims 2 and 8)", n)
	}
	if n := killsInCorvette.increment(m); n != 1 {
		t.Errorf("KillsCorvette counted %d, want 1 (killer class 2, victim a player ship)", n)
	}
	if n := kills.increment(m); n != 3 {
		t.Errorf("Kills counted %d, want 3 (the AI ship, class 29, does not count)", n)
	}
	ability, _ := mpQuestRuleByID("YMPQ_ModuleKills")
	if ability.countable(true) {
		t.Error("module kills are not reported and must not be offered")
	}
}
