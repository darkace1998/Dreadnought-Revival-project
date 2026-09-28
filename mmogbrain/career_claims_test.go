package main

import (
	"strings"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

func claimRequest(goal string, stage int32) []byte {
	b := protocol.AppendStringField(nil, "id", goal)
	return protocol.AppendInt32Field(b, "stage", stage)
}

func TestClaimCareerGoalStages(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "0123456789abcdef0123456789abcdef"
	setCredits(t, pid, 0)

	claim := func(goal string, stage int32) string {
		return string(buildMmogRequestResponsePayload("YA_ClaimCareerGoal", pid, claimRequest(goal, stage)))
	}
	if r := claim("GOAL_MATCHES_WON", 1); countWireStringField(r, "status", "stage not completed") != 1 {
		t.Fatalf("claim without a win: %q", r)
	}
	if _, err := database.Exec(`INSERT INTO battle_results(match_id,user_id,team,outcome,kills) VALUES('m1',?,1,'win',2)`, pid); err != nil {
		t.Fatal(err)
	}
	r := claim("GOAL_MATCHES_WON", 1)
	// id and stage at the root (where the client reads them) and in result.
	if countWireStringField(r, "status", "ok") != 1 || countWireStringField(r, "id", "GOAL_MATCHES_WON") != 2 ||
		countWireStringField(r, "stage", "1") != 2 || !strings.Contains(r, "rewardInfo") {
		t.Fatalf("first-win claim: %q", r)
	}
	if credits(t, pid) != 1500 {
		t.Errorf("credits %d after claiming the 1500-credit stage", credits(t, pid))
	}
	if r := claim("GOAL_MATCHES_WON", 1); countWireStringField(r, "status", "stage already claimed") != 1 || credits(t, pid) != 1500 {
		t.Errorf("repeat claim: %q, credits %d", r, credits(t, pid))
	}
	if r := claim("GOAL_MATCHES_WON", 2); countWireStringField(r, "status", "stage not completed") != 1 {
		t.Errorf("stage 2 with one win: %q", r)
	}
	if careerGoalClaimedStages(pid, "GOAL_MATCHES_WON") != 1 {
		t.Errorf("claimed stages %d, want 1", careerGoalClaimedStages(pid, "GOAL_MATCHES_WON"))
	}
	// The progression reply reports it, so the client's stage check agrees.
	var b []byte
	b, _ = appendCareerGoalProgress(b, nil, pid)
	if !strings.Contains(string(b), "claimed_stage") || countWireStringField(string(b), "claimed_stage", "1") != 1 {
		t.Errorf("progress does not report claimed_stage 1")
	}
	if r := claim("NO_SUCH_GOAL", 1); countWireStringField(r, "status", "unknown goal") != 1 {
		t.Errorf("unknown goal: %q", r)
	}
}
