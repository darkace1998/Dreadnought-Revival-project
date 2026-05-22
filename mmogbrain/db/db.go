package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS queue_entries (
		id          TEXT PRIMARY KEY,
		user_id     TEXT NOT NULL,
		game_mode   TEXT NOT NULL DEFAULT 'TeamDeathmatch',
		tier_min    INTEGER NOT NULL DEFAULT 1,
		tier_max    INTEGER NOT NULL DEFAULT 5,
		queued_at   TEXT NOT NULL DEFAULT (datetime('now')),
		status      TEXT NOT NULL DEFAULT 'waiting'
	)`,
	`CREATE TABLE IF NOT EXISTS matches (
		id          TEXT PRIMARY KEY,
		game_mode   TEXT NOT NULL,
		map         TEXT NOT NULL,
		server_ip   TEXT NOT NULL DEFAULT '',
		server_port INTEGER NOT NULL DEFAULT 0,
		status      TEXT NOT NULL DEFAULT 'forming',
		created_at  TEXT NOT NULL DEFAULT (datetime('now')),
		started_at  TEXT,
		ended_at    TEXT
	)`,
	`CREATE TABLE IF NOT EXISTS match_slots (
		match_id  TEXT NOT NULL,
		user_id   TEXT NOT NULL,
		team      INTEGER NOT NULL DEFAULT 0,
		joined_at TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (match_id, user_id)
	)`,
	`CREATE TABLE IF NOT EXISTS chat_messages (
		id        TEXT PRIMARY KEY,
		channel   TEXT NOT NULL DEFAULT 'global',
		sender_id TEXT NOT NULL,
		content   TEXT NOT NULL,
		sent_at   TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS player_state (
		user_id          TEXT PRIMARY KEY,
		soft_currency    INTEGER NOT NULL DEFAULT 10000,
		premium_currency INTEGER NOT NULL DEFAULT 0,
		free_xp          INTEGER NOT NULL DEFAULT 0,
		current_xp       INTEGER NOT NULL DEFAULT 0,
		current_rank     INTEGER NOT NULL DEFAULT 1,
		rank_xp          INTEGER NOT NULL DEFAULT 0,
		created_at       TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS player_fleets (
		user_id                   TEXT NOT NULL,
		fleet_id                  INTEGER NOT NULL,
		token                     TEXT NOT NULL,
		display_name              TEXT NOT NULL,
		fleet_type                INTEGER NOT NULL,
		active                    INTEGER NOT NULL DEFAULT 0,
		flagship_ship_id          INTEGER NOT NULL DEFAULT 0,
		flagship_loadout_id       INTEGER NOT NULL DEFAULT 0,
		flagship_loadout_index    INTEGER NOT NULL DEFAULT 0,
		created_at                TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at                TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (user_id, fleet_id),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS player_ship_loadouts (
		user_id                  TEXT NOT NULL,
		loadout_id               INTEGER NOT NULL,
		native_loadout_id        TEXT NOT NULL,
		precast_loadout_id       INTEGER NOT NULL,
		ship_id                  INTEGER NOT NULL,
		loadout_index            INTEGER NOT NULL DEFAULT 0,
		loadout_name             TEXT NOT NULL,
		position                 INTEGER NOT NULL DEFAULT 0,
		active                   INTEGER NOT NULL DEFAULT 0,
		weapon_primary_id        INTEGER NOT NULL DEFAULT 0,
		weapon_secondary_id      INTEGER NOT NULL DEFAULT 0,
		ability_primary_id       INTEGER NOT NULL DEFAULT 0,
		ability_secondary_id     INTEGER NOT NULL DEFAULT 0,
		ability_perimeter_id     INTEGER NOT NULL DEFAULT 0,
		ability_internal_id      INTEGER NOT NULL DEFAULT 0,
		perk_com_id              INTEGER NOT NULL DEFAULT 0,
		perk_weapon_id           INTEGER NOT NULL DEFAULT 0,
		perk_navigation_id       INTEGER NOT NULL DEFAULT 0,
		perk_engineer_id         INTEGER NOT NULL DEFAULT 0,
		created_at               TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at               TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (user_id, loadout_id),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS player_fleet_loadouts (
		user_id    TEXT NOT NULL,
		fleet_id   INTEGER NOT NULL,
		position   INTEGER NOT NULL,
		loadout_id INTEGER NOT NULL,
		PRIMARY KEY (user_id, fleet_id, position),
		FOREIGN KEY (user_id, fleet_id) REFERENCES player_fleets(user_id, fleet_id) ON DELETE CASCADE,
		FOREIGN KEY (user_id, loadout_id) REFERENCES player_ship_loadouts(user_id, loadout_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS player_officers (
		user_id    TEXT NOT NULL,
		officer_id TEXT NOT NULL,
		payload    TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (user_id, officer_id),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS player_contracts (
		user_id      TEXT NOT NULL,
		contract_id  TEXT NOT NULL,
		state        TEXT NOT NULL DEFAULT 'active',
		progress     INTEGER NOT NULL DEFAULT 0,
		completed_at TEXT,
		payload      TEXT NOT NULL DEFAULT '{}',
		created_at   TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at   TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (user_id, contract_id),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`ALTER TABLE player_state ADD COLUMN display_name TEXT NOT NULL DEFAULT 'Local'`,
	`ALTER TABLE player_state ADD COLUMN display_info TEXT NOT NULL DEFAULT ''`,
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
