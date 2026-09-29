package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS clusters (
		id              TEXT PRIMARY KEY,
		name            TEXT NOT NULL UNIQUE,
		web_url         TEXT NOT NULL,
		battle_ip       TEXT NOT NULL,
		version         TEXT NOT NULL DEFAULT '',
		motd            TEXT NOT NULL DEFAULT '',
		ca_cert         TEXT NOT NULL DEFAULT '',
		ca_fingerprint  TEXT NOT NULL DEFAULT '',
		players         INTEGER NOT NULL DEFAULT 0,
		servers         INTEGER NOT NULL DEFAULT 0,
		status          TEXT NOT NULL DEFAULT 'online',
		last_heartbeat  TEXT NOT NULL DEFAULT (datetime('now')),
		registered_at   TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
	// Operator blocklist: a blocked name is refused at register time, so a
	// kicked cluster cannot simply re-list itself on the next heartbeat.
	`ALTER TABLE clusters ADD COLUMN blocked INTEGER NOT NULL DEFAULT 0`,
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
			return fmt.Errorf("migration %d: %w", v, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_versions(version) VALUES(?)`, v); err != nil {
			return fmt.Errorf("record schema version %d: %w", v, err)
		}
	}
	return nil
}
