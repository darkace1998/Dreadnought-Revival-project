package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// Open opens (or creates) a SQLite database at path with WAL mode and
// foreign-key enforcement enabled.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // sqlite3 is single-writer
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	return db, nil
}

// Migrate runs DDL statements sequentially, creating the schema_versions
// table to track applied migrations.
func Migrate(db *sql.DB, migrations []string) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_versions (
		version   INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create schema_versions: %w", err)
	}

	var current int
	_ = db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_versions`).Scan(&current)

	for i, ddl := range migrations {
		version := i + 1
		if version <= current {
			continue
		}
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("migration %d: %w", version, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_versions(version) VALUES(?)`, version); err != nil {
			return fmt.Errorf("record migration %d: %w", version, err)
		}
	}
	return nil
}
