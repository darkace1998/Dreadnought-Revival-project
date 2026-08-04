package matchmaker

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"
)

// PvP has never been exercised here. Every session so far ran with
// PLAYERS_PER_MATCH=1, which forms a match the instant anyone queues, so two
// people queueing together get two separate battle servers and never meet --
// the whole of "there is nothing to do in matches" (AGENT-CHAT C25.6, S17).
//
// Before recommending PLAYERS_PER_MATCH=2 to anyone, the formation path has to
// be shown to do the obvious things: one match, one battle server, both players
// in it, on opposite teams.

func pvpTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite3", t.TempDir()+"/mm.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, ddl := range []string{
		`CREATE TABLE matches (id TEXT PRIMARY KEY, game_mode TEXT, map TEXT, server_ip TEXT,
		 server_port INTEGER, status TEXT, created_at TEXT, started_at TEXT, ended_at TEXT,
		 instance_id TEXT NOT NULL DEFAULT '', server_ready_at TEXT)`,
		`CREATE TABLE match_slots (match_id TEXT, user_id TEXT, team INTEGER,
		 joined_at TEXT DEFAULT (datetime('now')), PRIMARY KEY (match_id, user_id))`,
		`CREATE TABLE queue_entries (id TEXT PRIMARY KEY, user_id TEXT, game_mode TEXT,
		 tier_min INTEGER, status TEXT, queued_at TEXT)`,
	} {
		if _, err := database.Exec(ddl); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
	return database
}

// pvpMatchmaker returns a matchmaker wired to a stub control plane that counts
// how many battle servers were asked for.
func pvpMatchmaker(t *testing.T, database *sql.DB, playersPerMatch int) (*Matchmaker, *int32) {
	t.Helper()
	var instances int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&instances, 1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ip":"10.0.0.73","port":7777,"instance_id":"inst-1"}`))
	}))
	t.Cleanup(srv.Close)

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	return &Matchmaker{
		DB:              database,
		Log:             log,
		GameMgrURL:      srv.URL,
		InternalKey:     "k",
		PlayersPerMatch: playersPerMatch,
	}, &instances
}

func queuePlayer(t *testing.T, database *sql.DB, id, user, mode, queuedAt string) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO queue_entries(id,user_id,game_mode,tier_min,status,queued_at) VALUES(?,?,?,1,'waiting',?)`,
		id, user, mode, queuedAt); err != nil {
		t.Fatalf("queue %s: %v", user, err)
	}
}

func TestTwoQueuedPlayersShareOneMatchAndOneBattleServer(t *testing.T) {
	database := pvpTestDB(t)
	m, instances := pvpMatchmaker(t, database, 2)

	queuePlayer(t, database, "q1", "alice", "TDM", "2026-08-04T10:00:00Z")
	queuePlayer(t, database, "q2", "bob", "TDM", "2026-08-04T10:00:05Z")

	if err := m.formMatch("TDM", 1); err != nil {
		t.Fatalf("formMatch: %v", err)
	}

	// One battle server, not one each. This is the whole point of the setting.
	if got := atomic.LoadInt32(instances); got != 1 {
		t.Errorf("asked the control plane for %d battle servers, want 1", got)
	}

	var matches int
	if err := database.QueryRow(`SELECT count(*) FROM matches`).Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if matches != 1 {
		t.Fatalf("%d matches created, want 1", matches)
	}

	// Both players in it, and on OPPOSITE teams -- a TDM where both are on team
	// 0 would be two people unable to shoot each other, which looks exactly
	// like the empty match this is meant to fix.
	teams := map[string]int{}
	rows, err := database.Query(`SELECT user_id, team FROM match_slots`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var user string
		var team int
		if err := rows.Scan(&user, &team); err != nil {
			t.Fatal(err)
		}
		teams[user] = team
	}
	if len(teams) != 2 {
		t.Fatalf("match has %d slots (%v), want 2", len(teams), teams)
	}
	if teams["alice"] == teams["bob"] {
		t.Errorf("alice and bob are both on team %d; they cannot fight each other", teams["alice"])
	}

	// And the queue is emptied, or they would be matched again on the next tick.
	var waiting int
	if err := database.QueryRow(`SELECT count(*) FROM queue_entries`).Scan(&waiting); err != nil {
		t.Fatal(err)
	}
	if waiting != 0 {
		t.Errorf("%d queue entries survived the match, want 0", waiting)
	}
}

// The other half: one player must NOT get a match of their own when the setting
// says two. Otherwise raising it changes nothing.
func TestOneQueuedPlayerWaitsWhenTwoAreRequired(t *testing.T) {
	database := pvpTestDB(t)
	m, instances := pvpMatchmaker(t, database, 2)

	queuePlayer(t, database, "q1", "alice", "TDM", "2026-08-04T10:00:00Z")

	if err := m.formMatch("TDM", 1); err != nil {
		t.Fatalf("formMatch: %v", err)
	}
	if got := atomic.LoadInt32(instances); got != 0 {
		t.Errorf("spawned %d battle servers for a single queued player, want 0", got)
	}

	var status string
	if err := database.QueryRow(`SELECT status FROM queue_entries WHERE id='q1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "waiting" {
		t.Errorf("queue entry status = %q, want \"waiting\" -- the player was consumed by a match that never formed", status)
	}
}

// Players queued for different modes must not be dropped into one match. With
// PLAYERS_PER_MATCH=1 this could never happen, so it has never been exercised.
func TestPlayersInDifferentModesDoNotShareAMatch(t *testing.T) {
	database := pvpTestDB(t)
	m, instances := pvpMatchmaker(t, database, 2)

	queuePlayer(t, database, "q1", "alice", "TDM", "2026-08-04T10:00:00Z")
	queuePlayer(t, database, "q2", "bob", "Onslaught", "2026-08-04T10:00:05Z")

	if err := m.formMatch("TDM", 1); err != nil {
		t.Fatalf("formMatch: %v", err)
	}
	if got := atomic.LoadInt32(instances); got != 0 {
		t.Errorf("formed a TDM match from %d players, one of whom queued Onslaught", got)
	}
}
