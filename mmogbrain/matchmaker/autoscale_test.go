package matchmaker

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestRequiredMatchSize(t *testing.T) {
	const cap10 = 10
	wait := 60 * time.Second
	for _, tc := range []struct {
		name              string
		queued, available int
		waited            time.Duration
		want              int
	}{
		{"alone online: start at once", 1, 1, 0, 1},
		{"two online, both queued: one match of two", 2, 2, 0, 2},
		{"two online, one queued: wait for the other", 1, 2, 10 * time.Second, 0},
		{"two online, one queued, waited out: go alone", 1, 2, wait, 1},
		{"more queued than the cap: cap", 12, 12, 0, cap10},
		{"more online than the cap, cap queued: start", 10, 30, 0, cap10},
		{"stale online count below queued: queued wins", 3, 1, 0, 3},
		{"nobody queued", 0, 5, wait, 0},
	} {
		if got := requiredMatchSize(tc.queued, tc.available, cap10, tc.waited, wait); got != tc.want {
			t.Errorf("%s: requiredMatchSize(queued=%d, available=%d, waited=%s) = %d, want %d",
				tc.name, tc.queued, tc.available, tc.waited, got, tc.want)
		}
	}
}

// End to end through tick(): who counts as available, and the max wait.
func TestAutoscaledTickWaitsForIdleOnlinePlayersButNotForever(t *testing.T) {
	database := pvpTestDB(t)
	m, instances := pvpMatchmaker(t, database, 10)
	m.MaxWait = time.Minute
	online := []string{"alice", "bob", "carol"}
	m.OnlinePlayers = func() []string { return online }

	// carol is online but already in a battle: she must not be waited for.
	if _, err := database.Exec(`INSERT INTO matches(id,game_mode,map,status,created_at,instance_id)
		VALUES('live','BC','m','active',datetime('now'),'i')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO match_slots(match_id,user_id,team) VALUES('live','carol',0)`); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	queuePlayer(t, database, "q1", "alice", "BC", now.Format(time.RFC3339))
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(instances); got != 0 {
		t.Fatalf("alice started alone while bob is online and idle: %d servers", got)
	}

	// bob queues too: available = alice + bob (carol is in a battle) -> one match of two.
	queuePlayer(t, database, "q2", "bob", "BC", now.Format(time.RFC3339))
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(instances); got != 1 {
		t.Fatalf("alice and bob queued: %d battle servers, want 1", got)
	}
	var slots int
	if err := database.QueryRow(`SELECT count(*) FROM match_slots WHERE match_id != 'live'`).Scan(&slots); err != nil {
		t.Fatal(err)
	}
	if slots != 2 {
		t.Fatalf("new match has %d players, want 2", slots)
	}

	// dave queues while erin idles in the hangar; after the max wait dave goes alone.
	online = append(online, "dave", "erin")
	queuePlayer(t, database, "q3", "dave", "BC", now.Add(-2*time.Minute).Format("2006-01-02 15:04:05"))
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(instances); got != 2 {
		t.Fatalf("dave waited past the max wait: %d battle servers, want 2", got)
	}
}
