package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS users (
		id           TEXT PRIMARY KEY,
		username     TEXT NOT NULL UNIQUE,
		email        TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at   TEXT NOT NULL DEFAULT (datetime('now')),
		banned_at    TEXT
	)`,
	`CREATE TABLE IF NOT EXISTS sessions (
		id          TEXT PRIMARY KEY,
		user_id     TEXT NOT NULL REFERENCES users(id),
		token_hash  TEXT NOT NULL UNIQUE,
		expires_at  TEXT NOT NULL,
		created_at  TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS bans (
		id         TEXT PRIMARY KEY,
		user_id    TEXT NOT NULL REFERENCES users(id),
		reason     TEXT NOT NULL,
		banned_by  TEXT NOT NULL,
		expires_at TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
	// v4: track Steam-linked users
	`ALTER TABLE users ADD COLUMN steam_id TEXT`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_steam_id ON users(steam_id) WHERE steam_id IS NOT NULL`,
}

func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
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
