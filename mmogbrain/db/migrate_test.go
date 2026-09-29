package db

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// The live database held mirrored friend pairs ((A,B) plus (B,A), from the
// mixed-form era) that the old in-place normalisation UPDATE collapsed onto
// one PRIMARY KEY: migration 38 died with UNIQUE constraint failed, and
// mmogbrain refused to start at all (fatal: open database). The rebuild
// must swallow those pairs: one row, accepted wins, normalised ids.
func TestMigrateCollapsesMirroredFriends(t *testing.T) {
	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)
	a, b := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	c, d := "cccccccccccccccccccccccccccccccc", "dddddddddddddddddddddddddddddddd"
	// Legacy shape, legacy rows: mirrored pending pair (one dashed), a
	// mirrored pair where one side already accepted, one clean pair, one
	// dashed ignore dupe.
	for _, ddl := range []string{
		`CREATE TABLE player_friends(pid_a TEXT NOT NULL, pid_b TEXT NOT NULL,
			requester_id TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (pid_a, pid_b))`,
		`CREATE TABLE player_ignores(pid TEXT NOT NULL, ignored_id TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (pid, ignored_id))`,
	} {
		if _, err := database.Exec(ddl); err != nil {
			t.Fatalf("legacy ddl: %v", err)
		}
	}
	rows := []struct{ p, q, r, s, ts string }{
		{a, b, a, "pending", "2026-01-01 10:00:00"},
		{b, a, b, "pending", "2026-01-02 10:00:00"},
		{c, d, d, "pending", "2026-01-03 10:00:00"},
		{dash(c), dash(d), dash(d), "accepted", "2026-01-04 10:00:00"},
	}
	for _, row := range rows {
		if _, err := database.Exec(`INSERT INTO player_friends(pid_a,pid_b,requester_id,state,created_at)
			VALUES(?,?,?,?,?)`, row.p, row.q, row.r, row.s, row.ts); err != nil {
			t.Fatalf("seed friends: %v", err)
		}
	}
	if _, err := database.Exec(`INSERT INTO player_ignores(pid,ignored_id,created_at)
		VALUES(?,?,?)`, dash(a), b, "2026-01-05 10:00:00"); err != nil {
		t.Fatalf("seed ignores: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO player_ignores(pid,ignored_id,created_at)
		VALUES(?,?,?)`, a, b, "2026-01-06 10:00:00"); err != nil {
		t.Fatalf("seed ignores: %v", err)
	}

	// Full migration run over the legacy content — this is what killed the
	// live server.
	if err := migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_friends`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("friends rows = %d (err %v), want 2 collapsed pairs", n, err)
	}
	var state, requester string
	if err := database.QueryRow(`SELECT state,requester_id FROM player_friends WHERE pid_a=? AND pid_b=?`,
		a, b).Scan(&state, &requester); err != nil {
		t.Fatalf("pair ab: %v", err)
	}
	if state != "pending" {
		t.Errorf("pair ab state = %q, want pending", state)
	}
	var stateCD string
	if err := database.QueryRow(`SELECT state FROM player_friends WHERE pid_a=? AND pid_b=?`,
		c, d).Scan(&stateCD); err != nil {
		t.Fatalf("pair cd: %v", err)
	}
	if stateCD != "accepted" {
		t.Errorf("pair cd state = %q, want accepted-wins", stateCD)
	}
	// Normalised: undashed lowercase, ordered.
	var pa, pb string
	if err := database.QueryRow(`SELECT pid_a,pid_b FROM player_friends WHERE pid_a=?`,
		a).Scan(&pa, &pb); err != nil {
		t.Fatalf("ordering: %v", err)
	}
	if pa != a || pb != b {
		t.Errorf("pair = (%q,%q), want ordered undashed", pa, pb)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_ignores`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("ignores rows = %d (err %v), want 1", n, err)
	}
	var ip, ii string
	if err := database.QueryRow(`SELECT pid,ignored_id FROM player_ignores`).Scan(&ip, &ii); err != nil {
		t.Fatalf("ignore row: %v", err)
	}
	if ip != a || ii != b {
		t.Errorf("ignore = (%q,%q), want normalised", ip, ii)
	}
}

func dash(uid string) string {
	return uid[0:8] + "-" + uid[8:12] + "-" + uid[12:16] + "-" + uid[16:20] + "-" + uid[20:32]
}
