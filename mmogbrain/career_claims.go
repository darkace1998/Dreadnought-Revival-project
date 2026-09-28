package main

import (
	"strconv"
	"strings"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

// YA_ClaimCareerGoal: claiming a career goal stage on the progression screen.
// It was never answered ("unknown MMOG request"), so every claim failed with
// "goal : claiming stage 0 failed: error" and a player could not progress
// from Recruit to Captain (operator, 2026-09-28).
//
// Request (0x2A16860): RT, id (goal id string), stage (int, 1-BASED: the
// first claim of a goal sends stage=1 -- measured 2026-09-28, the server read
// stage=1 while the client logged it as "stage 0").
//
// Reply, read by the response dispatcher (0x2A30368, verified):
//
//	result.status   "ok", or the text the client logs as the failure reason
//	id, stage       at the ROOT: with them only inside "result" the client
//	                logged "goal : claiming stage 0" (empty id, stage 0).
//	                Also kept inside result; the client ignores those.
//	rewardInfo.freexp
//
// The client also checks the stage against its own count ("claimed stage %d
// is bigger than current stage %d + 1" / "smaller than current stage"). That
// count is claimed_stage in the career progression reply, which was always
// "0"; it now comes from player_career_claims -- the number of stages claimed.
func buildMmogClaimCareerGoalPayload(playerPID string, payload []byte) []byte {
	pid := normalizedPlayerStatePID(playerPID)
	goalID := protocol.FirstNonEmptyString(payload, "id", "Id", "goalId")
	stage := protocol.FirstInt32Field(payload, -1, "stage", "Stage")
	if stage < 0 {
		if v, err := strconv.Atoi(strings.TrimSpace(protocol.FirstNonEmptyString(payload, "stage", "Stage"))); err == nil {
			stage = int32(v)
		}
	}

	status, freeXP := claimCareerGoalStage(pid, goalID, stage)
	logrus.WithFields(logrus.Fields{"player": pid, "goal": goalID, "stage": stage, "status": status, "freexp": freeXP}).
		Info("mmog: YA_ClaimCareerGoal")

	var b []byte
	var stack []int
	b = protocol.AppendStringField(b, "RT", "YA_ClaimCareerGoal")
	b = protocol.AppendStringField(b, "id", goalID)
	b = protocol.AppendStringField(b, "stage", strconv.Itoa(int(stage)))
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b = protocol.AppendStringField(b, "status", status)
	b = protocol.AppendStringField(b, "id", goalID)
	b = protocol.AppendStringField(b, "stage", strconv.Itoa(int(stage)))
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendObjectStart(b, stack, "rewardInfo")
	b = protocol.AppendStringField(b, "freexp", strconv.Itoa(int(freeXP)))
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

// claimCareerGoalStage validates and records one claim and pays its reward.
// stage is the client's 1-based stage number. It returns "ok" or the reason it
// refused, and the free XP it granted.
func claimCareerGoalStage(pid, goalID string, wireStage int32) (status string, freeXP int32) {
	stage := wireStage - 1 // index into goal.stages
	var goal *careerGoal
	for _, g := range careerGoalsConfig() {
		if g.id == goalID {
			g := g
			goal = &g
			break
		}
	}
	switch {
	case goal == nil:
		return "unknown goal", 0
	case stage < 0 || int(stage) >= len(goal.stages):
		return "no such stage", 0
	}
	claimed := careerGoalClaimedStages(pid, goalID)
	if stage < claimed {
		return "stage already claimed", 0
	}
	if stage > claimed {
		return "earlier stage not claimed", 0
	}
	st := goal.stages[stage]
	if careerGoalProgressForPlayer(pid, goalID) < st.amountToComplete {
		return "stage not completed", 0
	}

	database := currentMmogPlayerStateDB()
	if database == nil {
		return "unavailable", 0
	}
	if err := seedMmogPlayerState(database, pid); err != nil {
		return "unavailable", 0
	}
	tx, err := database.Begin()
	if err != nil {
		return "unavailable", 0
	}
	defer func() { _ = tx.Rollback() }()
	// The claim is keyed by the stage count before it, so a repeated or racing
	// claim of the same stage changes nothing and pays nothing.
	res, err := tx.Exec(`INSERT INTO player_career_claims(user_id,goal_id,claimed_stages) VALUES(?,?,?)
		ON CONFLICT(user_id,goal_id) DO UPDATE SET claimed_stages=excluded.claimed_stages, updated_at=datetime('now')
		WHERE player_career_claims.claimed_stages=?`, pid, goalID, stage+1, stage)
	if err != nil {
		return "unavailable", 0
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "stage already claimed", 0
	}
	switch st.rewardType {
	case "EYGoalRewardType::YGR_CREDITS":
		_, err = tx.Exec(`UPDATE player_state SET soft_currency=soft_currency+?, updated_at=datetime('now') WHERE user_id=?`, st.reward, pid)
	case "EYGoalRewardType::YGR_FREEXP":
		freeXP = st.reward
		_, err = tx.Exec(`UPDATE player_state SET free_xp=free_xp+?, updated_at=datetime('now') WHERE user_id=?`, st.reward, pid)
	}
	if err != nil {
		return "unavailable", 0
	}
	if err := tx.Commit(); err != nil {
		return "unavailable", 0
	}
	return "ok", freeXP
}

// careerGoalClaimedStages is how many stages of a goal the player has claimed.
func careerGoalClaimedStages(pid, goalID string) int32 {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return 0
	}
	var n int32
	_ = database.QueryRow(`SELECT claimed_stages FROM player_career_claims WHERE user_id=? AND goal_id=?`,
		normalizedPlayerStatePID(pid), goalID).Scan(&n)
	return n
}
