package matchmaker

import (
	"testing"
	"time"
)

func addRunningMatch(t *testing.T, m *Matchmaker, id, mode string, fleetType int, age time.Duration, ready bool, slots map[string]int) {
	t.Helper()
	created := time.Now().UTC().Add(-age).Format(time.RFC3339)
	var readyAt any
	if ready {
		readyAt = created
	}
	if _, err := m.DB.Exec(`INSERT INTO matches(id,game_mode,map,status,created_at,started_at,server_ready_at,fleet_type) VALUES(?,?,'X','active',?,?,?,?)`,
		id, mode, created, created, readyAt, fleetType); err != nil {
		t.Fatal(err)
	}
	for user, team := range slots {
		if _, err := m.DB.Exec(`INSERT INTO match_slots(match_id,user_id,team) VALUES(?,?,?)`, id, user, team); err != nil {
			t.Fatal(err)
		}
	}
}

// A player who queues while another plays alone joins that match, on the
// other team, instead of getting a second private match.
func TestBackfillJoinsARunningMatch(t *testing.T) {
	database := pvpTestDB(t)
	m, spawns := pvpMatchmaker(t, database, 10)
	m.BackfillWindow = 3 * time.Minute
	addRunningMatch(t, m, "running", "TDM", 1, 40*time.Second, true, map[string]int{"first": 1})
	queuePlayer(t, database, "q1", "second", "TDM", time.Now().UTC().Format(time.RFC3339))

	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	teams := map[string]int{}
	rows, _ := database.Query(`SELECT user_id, team FROM match_slots WHERE match_id='running'`)
	for rows.Next() {
		var u string
		var team int
		_ = rows.Scan(&u, &team)
		teams[u] = team
	}
	_ = rows.Close()
	if teams["second"] != 2 {
		t.Errorf("slots %v: want the second player in the running match on team 2", teams)
	}
	if *spawns != 0 {
		t.Errorf("%d new battle servers started; the running match had room", *spawns)
	}
	var queued int
	_ = database.QueryRow(`SELECT COUNT(*) FROM queue_entries`).Scan(&queued)
	if queued != 0 {
		t.Errorf("%d queue entries left", queued)
	}
}

// No backfill into a match of another fleet, an old one, one whose host is
// not ready, a full one, or an empty one: those start a new match.
func TestBackfillOnlyIntoAFittingMatch(t *testing.T) {
	cases := []struct {
		name     string
		fleet    int
		age      time.Duration
		ready    bool
		slots    map[string]int
		capacity int
	}{
		{"other fleet", 2, 30 * time.Second, true, map[string]int{"a": 1}, 10},
		{"too old", 1, 10 * time.Minute, true, map[string]int{"a": 1}, 10},
		{"host not ready", 1, 30 * time.Second, false, map[string]int{"a": 1}, 10},
		{"full", 1, 30 * time.Second, true, map[string]int{"a": 1, "b": 2}, 2},
		{"empty", 1, 30 * time.Second, true, map[string]int{}, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			database := pvpTestDB(t)
			m, _ := pvpMatchmaker(t, database, c.capacity)
			m.BackfillWindow = 3 * time.Minute
			addRunningMatch(t, m, "running", "TDM", c.fleet, c.age, c.ready, c.slots)
			queuePlayer(t, database, "q1", "new", "TDM", time.Now().UTC().Format(time.RFC3339))
			if placed, err := m.backfill("TDM", 1, 1); err != nil || placed != 0 {
				t.Errorf("placed %d (err %v), want 0", placed, err)
			}
		})
	}
}

// A squad goes in whole, on one team, or not at all.
func TestBackfillKeepsASquadTogether(t *testing.T) {
	database := pvpTestDB(t)
	m, _ := pvpMatchmaker(t, database, 4)
	m.BackfillWindow = 3 * time.Minute
	addRunningMatch(t, m, "running", "TDM", 1, 30*time.Second, true, map[string]int{"a": 1, "b": 2})
	now := time.Now().UTC().Format(time.RFC3339)
	queueParty(t, database, "q1", "leader", "sq", now)
	queueParty(t, database, "q2", "member", "sq", now)
	if placed, err := m.backfill("TDM", 1, 1); err != nil || placed != 2 {
		t.Fatalf("placed %d (err %v), want the squad of 2", placed, err)
	}
	teams := slotTeams(t, database)
	if teams["leader"] != teams["member"] {
		t.Errorf("squad split: %v", teams)
	}

	// Now full (4/4): a third squad member's party of 2 does not fit.
	queueParty(t, database, "q3", "x", "sq2", now)
	queueParty(t, database, "q4", "y", "sq2", now)
	if placed, _ := m.backfill("TDM", 1, 1); placed != 0 {
		t.Errorf("placed %d into a full match", placed)
	}
}
