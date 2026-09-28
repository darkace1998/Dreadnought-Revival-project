package main

import (
	"testing"
	"time"
)

const dirtyTestPID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestCurrencyDirtyMarkAndConsume(t *testing.T) {
	markCurrencyDirty(dirtyTestPID)
	if !consumeCurrencyDirty(dirtyTestPID) {
		t.Fatal("fresh flag must report dirty")
	}
	if consumeCurrencyDirty(dirtyTestPID) {
		t.Fatal("consumed flag must not report dirty again")
	}
}

func TestCurrencyDirtyRejectsGarbage(t *testing.T) {
	markCurrencyDirty("not-a-pid")
	if consumeCurrencyDirty("not-a-pid") {
		t.Fatal("garbage pid must never be dirty")
	}
	if consumeCurrencyDirty("") {
		t.Fatal("empty pid must never be dirty")
	}
}

func TestCurrencyDirtyExpires(t *testing.T) {
	markCurrencyDirty(dirtyTestPID)
	currencyDirty.Lock()
	currencyDirty.at[dirtyTestPID] = time.Now().Add(-currencyDirtyTTL - time.Minute)
	currencyDirty.Unlock()
	if consumeCurrencyDirty(dirtyTestPID) {
		t.Fatal("expired flag must report clean")
	}
}

// The battle reward path must leave the flag behind: that is the whole
// mechanism by which a connected client learns its new balance.
func TestRecordBattleResultMarksCurrencyDirty(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	_, _, fresh, err := recordBattleResult(battleResult{
		match: "m1", pid: dirtyTestPID, team: 1, outcome: "win",
	}, currentBattleRewards())
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if !fresh {
		t.Fatal("expected a fresh grant")
	}
	if !consumeCurrencyDirty(dirtyTestPID) {
		t.Error("battle reward did not mark the account dirty")
	}
}
