package matchmaker

import (
	"database/sql"
	"testing"
)

func queueParty(t *testing.T, database *sql.DB, id, user, party, queuedAt string) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO queue_entries(id,user_id,game_mode,tier_min,status,queued_at,party_id) VALUES(?,?,'TDM',1,'waiting',?,?)`,
		id, user, queuedAt, party); err != nil {
		t.Fatalf("queue %s: %v", user, err)
	}
}

func slotTeams(t *testing.T, database *sql.DB) map[string]int {
	t.Helper()
	teams := map[string]int{}
	rows, err := database.Query(`SELECT user_id, team FROM match_slots`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var u string
		var team int
		if err := rows.Scan(&u, &team); err != nil {
			t.Fatal(err)
		}
		teams[u] = team
	}
	return teams
}

// A squad of two and two solo players in a 4-player TDM: the squad fights on
// ONE team, the solos on the other.
func TestSquadPlaysOnOneTeam(t *testing.T) {
	database := pvpTestDB(t)
	m, _ := pvpMatchmaker(t, database, 4)
	queueParty(t, database, "q1", "leader", "sq1", "2026-09-29T10:00:00Z")
	queueParty(t, database, "q2", "member", "sq1", "2026-09-29T10:00:00Z")
	queueParty(t, database, "q3", "solo1", "", "2026-09-29T10:00:01Z")
	queueParty(t, database, "q4", "solo2", "", "2026-09-29T10:00:02Z")
	if err := m.formMatch("TDM", 1, 1); err != nil {
		t.Fatal(err)
	}
	teams := slotTeams(t, database)
	if len(teams) != 4 {
		t.Fatalf("%d slots, want 4: %v", len(teams), teams)
	}
	if teams["leader"] != teams["member"] {
		t.Errorf("squad split across teams: %v", teams)
	}
	if teams["solo1"] == teams["leader"] || teams["solo2"] == teams["leader"] {
		t.Errorf("solo players joined the squad's side instead of opposing it: %v", teams)
	}
}

// A squad is never split: with one slot left it waits, and a solo player
// behind it takes the slot instead.
func TestSquadIsNeverSplit(t *testing.T) {
	database := pvpTestDB(t)
	m, _ := pvpMatchmaker(t, database, 2)
	queueParty(t, database, "q1", "solo1", "", "2026-09-29T10:00:00Z")
	queueParty(t, database, "q2", "leader", "sq1", "2026-09-29T10:00:01Z")
	queueParty(t, database, "q3", "member", "sq1", "2026-09-29T10:00:01Z")
	queueParty(t, database, "q4", "solo2", "", "2026-09-29T10:00:02Z")
	if err := m.formMatch("TDM", 1, 1); err != nil {
		t.Fatal(err)
	}
	teams := slotTeams(t, database)
	if _, ok := teams["solo2"]; !ok || len(teams) != 2 {
		t.Fatalf("slots %v, want solo1 + solo2 (the squad does not fit in one slot)", teams)
	}
	var waiting int
	_ = database.QueryRow(`SELECT count(*) FROM queue_entries WHERE party_id='sq1' AND status='waiting'`).Scan(&waiting)
	if waiting != 2 {
		t.Errorf("%d squad entries still waiting, want both", waiting)
	}
}
