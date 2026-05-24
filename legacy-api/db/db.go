package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS player_profiles (
		id           TEXT PRIMARY KEY,
		user_id      TEXT NOT NULL UNIQUE,
		display_name TEXT NOT NULL,
		created_at   TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS player_stats (
		user_id         TEXT PRIMARY KEY,
		kills           INTEGER NOT NULL DEFAULT 0,
		deaths          INTEGER NOT NULL DEFAULT 0,
		matches_played  INTEGER NOT NULL DEFAULT 0,
		wins            INTEGER NOT NULL DEFAULT 0,
		xp_total        INTEGER NOT NULL DEFAULT 0,
		credits         INTEGER NOT NULL DEFAULT 10000,
		updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS player_inventory (
		id          TEXT PRIMARY KEY,
		user_id     TEXT NOT NULL,
		item_type   TEXT NOT NULL,
		item_id     TEXT NOT NULL,
		acquired_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS match_history (
		id         TEXT PRIMARY KEY,
		mode       TEXT NOT NULL,
		map        TEXT NOT NULL,
		started_at TEXT NOT NULL,
		ended_at   TEXT,
		result_json TEXT
	)`,
	`CREATE TABLE IF NOT EXISTS match_players (
		match_id TEXT NOT NULL,
		user_id  TEXT NOT NULL,
		team     INTEGER NOT NULL DEFAULT 0,
		score    INTEGER NOT NULL DEFAULT 0,
		kills    INTEGER NOT NULL DEFAULT 0,
		deaths   INTEGER NOT NULL DEFAULT 0,
		damage   INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (match_id, user_id)
	)`,
	`ALTER TABLE player_stats ADD COLUMN assists INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE player_stats ADD COLUMN damage_dealt INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE player_stats ADD COLUMN damage_taken INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE player_stats ADD COLUMN healing_done INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE player_stats ADD COLUMN control_points INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE player_stats ADD COLUMN double_kills INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE player_stats ADD COLUMN triple_kills INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE player_stats ADD COLUMN multikills INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE player_stats ADD COLUMN kill_streak INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE player_stats ADD COLUMN modules_used INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE player_stats ADD COLUMN energy_spent INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE player_stats ADD COLUMN distance_traveled INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE player_stats ADD COLUMN time_played INTEGER NOT NULL DEFAULT 0`,
}

func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", path)
	database, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	database.SetMaxOpenConns(1)
	if err := database.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := migrate(database); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return database, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_versions (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}
	var current int
	_ = db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_versions`).Scan(&current)
	for i, ddl := range migrations {
		v := i + 1
		if v <= current {
			continue
		}
		if _, err := db.Exec(ddl); err != nil {
			if strings.Contains(err.Error(), "duplicate column") {
				_, _ = db.Exec(`INSERT OR IGNORE INTO schema_versions(version) VALUES(?)`, v)
				continue
			}
			return fmt.Errorf("migration %d: %w", v, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_versions(version) VALUES(?)`, v); err != nil {
			return fmt.Errorf("record schema version %d: %w", v, err)
		}
	}
	return nil
}
