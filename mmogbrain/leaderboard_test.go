package main

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

func leaderboardRequest(mode, timespan string, tier int32, prev bool) []byte {
	b := protocol.AppendStringField(nil, "RT", "YA_GetLeaderboardData")
	b = protocol.AppendStringField(b, "modename", mode)
	b = protocol.AppendStringField(b, "timespan", timespan)
	b = protocol.AppendInt32Field(b, "fleettier", tier)
	b = protocol.AppendBoolField(b, "prevweek", prev)
	return protocol.AppendRootEnd(b)
}

func TestLeaderboardWindowStartsMonday(t *testing.T) {
	thu := time.Date(2026, 10, 15, 13, 0, 0, 0, time.UTC)
	from, to := leaderboardWindow("Weekly", false, thu)
	if from != "2026-10-12 00:00:00" || to != "2026-10-19 00:00:00" {
		t.Errorf("this week = [%s, %s)", from, to)
	}
	from, to = leaderboardWindow("Weekly", true, thu)
	if from != "2026-10-05 00:00:00" || to != "2026-10-12 00:00:00" {
		t.Errorf("previous week = [%s, %s)", from, to)
	}
	sun := time.Date(2026, 10, 18, 23, 0, 0, 0, time.UTC)
	if from, _ = leaderboardWindow("Weekly", false, sun); from != "2026-10-12 00:00:00" {
		t.Errorf("Sunday belongs to the week that started %s", from)
	}
	if from, to = leaderboardWindow("Lifetime", false, thu); from != "" || to != "" {
		t.Errorf("Lifetime is unbounded, got [%s, %s)", from, to)
	}
}

// The parser (0x2A6E840) needs modename/fleettier/timespan at the root and
// result.leaderboard + result.playerrank as arrays of PID/Rank/SID/Score.
func TestLeaderboardRanksMatchScores(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const (
		alice  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		bob    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		carol  = "cccccccccccccccccccccccccccccccc"
		banned = "dddddddddddddddddddddddddddddddd"
	)
	for _, p := range []struct{ pid, name string }{{alice, "Alice"}, {bob, "Bob"}, {carol, "Carol"}, {banned, "Cheater"}} {
		if err := seedMmogPlayerState(database, p.pid); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE player_state SET display_name=? WHERE user_id=?`, p.name, p.pid); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`INSERT INTO admin_bans(user_id, reason) VALUES(?, 'test')`, banned); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	thisWeek := now.Format("2006-01-02 15:04:05")
	lastWeek := now.AddDate(0, 0, -7).Format("2006-01-02 15:04:05")
	match := func(id, mode string, fleet int, at string, scores map[string]int) {
		t.Helper()
		if _, err := database.Exec(`INSERT INTO matches(id, game_mode, map, status, battle_match_id, fleet_type) VALUES(?,?,'Gorge','ended',?,?)`,
			"m-"+id, mode, id, fleet); err != nil {
			t.Fatal(err)
		}
		for pid, score := range scores {
			if _, err := database.Exec(`INSERT INTO battle_results(match_id,user_id,outcome,fleet_type,created_at) VALUES(?,?,'win',?,?)`, id, pid, fleet, at); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`INSERT INTO battle_result_stats(match_id,user_id,stat,value) VALUES(?,?,'score',?)`, id, pid, score); err != nil {
				t.Fatal(err)
			}
		}
	}
	match("w1", "TDM", 2, thisWeek, map[string]int{alice: 900, bob: 1500, banned: 99999})
	match("w2", "TDM", 2, thisWeek, map[string]int{alice: 800})             // alice 1700 > bob 1500
	match("w3", "Onslaught", 2, thisWeek, map[string]int{carol: 5000})      // other mode
	match("w4", "TDM", 3, thisWeek, map[string]int{carol: 4000})            // other fleet tier
	match("old", "TDM", 2, lastWeek, map[string]int{carol: 3000, bob: 500}) // last week

	resp := buildMmogLeaderboardPayload(bob, leaderboardRequest("TDM", "Weekly", 2, false))
	if got := protocol.ExtractStringField(resp, "modename"); got != "TDM" {
		t.Errorf("root modename = %q", got)
	}
	if v, ok := protocol.ExtractInt32Field(resp, "fleettier"); !ok || v != 2 {
		t.Errorf("root fleettier = %d (%v)", v, ok)
	}
	sids := protocol.ExtractStringFields(resp, "SID")
	want := []string{gatewayUsernameTag("Alice", alice), gatewayUsernameTag("Bob", bob), gatewayUsernameTag("Bob", bob)}
	if strings.Join(sids, ",") != strings.Join(want, ",") {
		t.Fatalf("SIDs (two board rows, then the player's own row) = %v, want %v", sids, want)
	}
	if pids := protocol.ExtractStringFields(resp, "PID"); len(pids) != 3 || pids[0] != alice || pids[2] != bob {
		t.Errorf("PIDs = %v", pids)
	}

	prev := buildMmogLeaderboardPayload(carol, leaderboardRequest("TDM", "Weekly", 2, true))
	if sids := protocol.ExtractStringFields(prev, "SID"); len(sids) != 3 || !strings.HasPrefix(sids[0], "Carol#") || !strings.HasPrefix(sids[1], "Bob#") {
		t.Errorf("previous week = %v, want Carol then Bob, then Carol's own row", sids)
	}

	life := buildMmogLeaderboardPayload(alice, leaderboardRequest("TDM", "Lifetime", 2, false))
	if sids := protocol.ExtractStringFields(life, "SID"); len(sids) != 4 || !strings.HasPrefix(sids[0], "Carol#") {
		t.Errorf("lifetime = %v, want Carol (3000), Bob (2000), Alice (1700), then Alice's own row", sids)
	}

	// Unranked player: an empty playerrank array -> the client's "NOT RANKED!".
	none := buildMmogLeaderboardPayload(carol, leaderboardRequest("TDM", "Weekly", 2, false))
	if sids := protocol.ExtractStringFields(none, "SID"); len(sids) != 2 {
		t.Errorf("unranked player got %d rows, want just the 2 board rows", len(sids))
	}
}

// The request as the client sends it (captured 2026-10-16, PID zeroed):
// PID is a GUID (0x02), fleettier a 1-byte int (0x26), prevweek a bool (0x05).
func TestLeaderboardReadsTheCapturedRequest(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	req, _ := hex.DecodeString("025254091500000059415f4765744c6561646572626f61726444617461035049440200000000000000000000000000000000086d6f64656e616d65090300000054444d0874696d657370616e09060000005765656b6c7909666c65657474696572260108707265767765656b0500000e00000000")
	resp := buildMmogLeaderboardPayload("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", req)
	if got := protocol.ExtractStringField(resp, "modename"); got != "TDM" {
		t.Errorf("modename = %q", got)
	}
	if got := protocol.ExtractStringField(resp, "timespan"); got != "Weekly" {
		t.Errorf("timespan = %q", got)
	}
	if v, ok := protocol.ExtractInt32Field(resp, "fleettier"); !ok || v != 1 {
		t.Errorf("fleettier = %d (%v)", v, ok)
	}
}
