package main

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// realIncrementRequest is a verbatim capture of what the client sends. The ids
// are STRINGS -- the server used to answer YA_GetPlayerStatsCounterData with a
// single row whose counterId was the int32 0.
func realIncrementRequest(counterID, subID string, increment int32) []byte {
	var b []byte
	b = protocol.AppendStringField(b, "RT", "YA_IncrementPlayerStatsCounter")
	b = protocol.AppendStringField(b, "counterId", counterID)
	b = protocol.AppendStringField(b, "counterSubId", subID)
	b = protocol.AppendInt32Field(b, "increment", increment)
	return b
}

func TestStatsCountersPersistAndAccumulate(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const playerPID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	database := currentMmogPlayerStateDB()

	for _, step := range []struct {
		counterID, subID string
		increment        int32
	}{
		{"Customize", "Captain", 1},
		{"Customize", "Ship", 1},
		{"Customize", "Ship", 2},
	} {
		if err := persistMmogPlayerMutation(playerPID, "YA_IncrementPlayerStatsCounter",
			realIncrementRequest(step.counterID, step.subID, step.increment)); err != nil {
			t.Fatalf("persist %s/%s: %v", step.counterID, step.subID, err)
		}
	}
	_ = database

	if got := playerStatsCounterValue(playerPID, "Customize", "Ship"); got != 3 {
		t.Errorf("Customize/Ship = %d, want 3 (1+2)", got)
	}
	if got := playerStatsCounterValue(playerPID, "Customize", ""); got != 4 {
		t.Errorf("Customize (all sub-ids) = %d, want 4", got)
	}
	if got := playerStatsCounterValue(playerPID, "NeverReported", ""); got != 0 {
		t.Errorf("an unreported counter = %d, want 0", got)
	}
}

func TestStatsCounterResponseUsesStringIDs(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const playerPID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := persistMmogPlayerMutation(playerPID, "YA_IncrementPlayerStatsCounter",
		realIncrementRequest("Customize", "Captain", 7)); err != nil {
		t.Fatal(err)
	}

	payload := buildMmogPlayerStatsCounterDataPayload(playerPID)

	// The client sends these as strings, so they have to come back as strings.
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "counterId", "Customize")) {
		t.Error("counterId is not the string the client sent")
	}
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "counterSubId", "Captain")) {
		t.Error("counterSubId is not the string the client sent")
	}
	// And the value as a numeric string: an int32 reads back as 0 through this
	// client's value accessors.
	if !bytes.Contains(payload, protocol.AppendStringField(nil, "value", strconv.Itoa(7))) {
		t.Error("value is not carried as a numeric string")
	}
	if bytes.Contains(payload, protocol.AppendInt32Field(nil, "counterId", 0)) {
		t.Error("the hardcoded int32 counterId row is back")
	}
}

// UYGoalManager::GetCurrentAmount reads the goal's amount straight out of the
// dynamic career response, so this number is the goal's progress. It used to be
// a constant zero, which meant no goal could ever advance.
func TestCareerGoalProgressFollowsReportedCounters(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const playerPID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	var counterGoal careerGoal
	for _, goal := range careerGoalsConfig() {
		if goal.id != "UnlockAllModes" && goal.counterID != "" {
			counterGoal = goal
			break
		}
	}
	if counterGoal.id == "" {
		t.Fatal("no goal carries a counter id")
	}

	if got := careerGoalProgressForPlayer(playerPID, counterGoal.id); got != 0 {
		t.Fatalf("a fresh player starts %s at %d, want 0", counterGoal.id, got)
	}
	if err := persistMmogPlayerMutation(playerPID, "YA_IncrementPlayerStatsCounter",
		realIncrementRequest(counterGoal.counterID, counterGoal.counterSubID, 4)); err != nil {
		t.Fatal(err)
	}
	if got := careerGoalProgressForPlayer(playerPID, counterGoal.id); got != 4 {
		t.Errorf("%s = %d after the client reported 4, want 4", counterGoal.id, got)
	}

	// UnlockAllModes stays satisfied regardless, or the client locks every mode.
	if got := careerGoalProgressForPlayer(playerPID, "UnlockAllModes"); got < 1 {
		t.Errorf("UnlockAllModes = %d, want at least its final stage amount", got)
	}
}
