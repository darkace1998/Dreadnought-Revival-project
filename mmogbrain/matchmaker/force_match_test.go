package matchmaker

import (
	"sync/atomic"
	"testing"
)

// ForceMatch is the operator escape hatch for a stuck queue: whoever waits
// plays now, instead of waiting for the bucket to fill.
func TestForceMatchStartsWhoeverWaits(t *testing.T) {
	database := pvpTestDB(t)
	m, instances := pvpMatchmaker(t, database, 10)

	queuePlayer(t, database, "q1", "alice", "TDM", "2026-08-04T10:00:00Z")

	info, err := m.ForceMatch()
	if err != nil {
		t.Fatalf("ForceMatch: %v", err)
	}
	if info["players"] != 1 {
		t.Errorf("players = %v, want 1", info["players"])
	}
	var matches int
	if err := database.QueryRow(`SELECT count(*) FROM matches`).Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if matches != 1 {
		t.Fatalf("%d matches, want 1", matches)
	}
	if got := atomic.LoadInt32(instances); got != 1 {
		t.Errorf("%d battle servers asked for, want 1", got)
	}
}

func TestForceMatchOnEmptyQueueFails(t *testing.T) {
	database := pvpTestDB(t)
	m, _ := pvpMatchmaker(t, database, 10)
	if _, err := m.ForceMatch(); err == nil {
		t.Fatal("expected an error on an empty queue")
	}
}
