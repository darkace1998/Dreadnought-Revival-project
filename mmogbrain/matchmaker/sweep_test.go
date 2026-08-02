package matchmaker

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"
)

func sweepTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite3", t.TempDir()+"/mm.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, ddl := range []string{
		// instance_id and server_ready_at mirror the real migrations: the
		// readiness poll selects on both, and a schema without them makes every
		// test in this package that runs a tick silently skip it.
		`CREATE TABLE matches (id TEXT PRIMARY KEY, game_mode TEXT, map TEXT, server_ip TEXT,
		 server_port INTEGER, status TEXT, created_at TEXT, started_at TEXT, ended_at TEXT,
		 instance_id TEXT NOT NULL DEFAULT '', server_ready_at TEXT)`,
		`CREATE TABLE match_slots (match_id TEXT, user_id TEXT, team INTEGER,
		 joined_at TEXT DEFAULT (datetime('now')), PRIMARY KEY (match_id, user_id))`,
	} {
		if _, err := database.Exec(ddl); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
	return database
}

// The sweep is the only thing that ever ends a match. Without it every match
// stayed 'active' forever and permanently pinned its players as "matched", so
// they could never queue again.
func TestSweepEndsStaleMatchesAndFreesPlayers(t *testing.T) {
	database := sweepTestDB(t)
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	m := &Matchmaker{DB: database, Log: log}

	old := time.Now().UTC().Add(-MaxMatchLifetime - time.Hour).Format(time.RFC3339)
	fresh := time.Now().UTC().Format(time.RFC3339)
	for _, row := range []struct{ id, created string }{{"stale", old}, {"fresh", fresh}} {
		if _, err := database.Exec(
			`INSERT INTO matches(id,game_mode,map,server_ip,server_port,status,created_at,started_at)
			 VALUES(?,'BC','Amirani','10.0.0.73',7777,'active',?,?)`, row.id, row.created, row.created); err != nil {
			t.Fatalf("insert match %s: %v", row.id, err)
		}
		if _, err := database.Exec(`INSERT INTO match_slots(match_id,user_id,team) VALUES(?,?,0)`, row.id, "p-"+row.id); err != nil {
			t.Fatalf("insert slot %s: %v", row.id, err)
		}
	}

	if err := m.sweepStaleMatches(); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	var status string
	if err := database.QueryRow(`SELECT status FROM matches WHERE id='stale'`).Scan(&status); err != nil {
		t.Fatalf("read stale status: %v", err)
	}
	if status != "ended" {
		t.Errorf("stale match status = %q, want ended", status)
	}
	// Its slots must go too -- the slot is what binds the player to the match.
	var slots int
	if err := database.QueryRow(`SELECT COUNT(*) FROM match_slots WHERE match_id='stale'`).Scan(&slots); err != nil {
		t.Fatalf("count stale slots: %v", err)
	}
	if slots != 0 {
		t.Errorf("stale match still has %d slots; its players stay pinned as matched", slots)
	}

	// A live match must be untouched, or players get kicked out of real games.
	if err := database.QueryRow(`SELECT status FROM matches WHERE id='fresh'`).Scan(&status); err != nil {
		t.Fatalf("read fresh status: %v", err)
	}
	if status != "active" {
		t.Errorf("fresh match status = %q, want active; the sweep is ending live matches", status)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM match_slots WHERE match_id='fresh'`).Scan(&slots); err != nil {
		t.Fatalf("count fresh slots: %v", err)
	}
	if slots != 1 {
		t.Errorf("fresh match has %d slots, want 1", slots)
	}
}

// TM is the only game mode whose game info supplies the player's loadout
// (GameInfo_TM_BP's m_trainingMatchLoadout), which is what lets a battle server
// spawn a pawn without backend fleet data. It only works on Highlands -- the
// one map shipping a TM level variation (MP_Highlands_TM.umap) -- so the
// matchmaker must not hand it any other map.
func TestTrainingMatchIsPinnedToHighlands(t *testing.T) {
	maps, ok := mapsByGameMode["TM"]
	if !ok {
		t.Fatal("TM has no pinned map; it would be sent to a map with no orbit spawns")
	}
	if len(maps) != 1 || maps[0].Name != "Highlands" {
		t.Fatalf("TM maps = %+v, want only Highlands", maps)
	}
	if maps[0].Path != "/Game/Maps/MP/Highlands/MP_Highlands_P" {
		t.Errorf("TM map path = %q, want the Highlands package path", maps[0].Path)
	}
}
