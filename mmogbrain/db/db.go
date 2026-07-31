package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS queue_entries (
		id          TEXT PRIMARY KEY,
		user_id     TEXT NOT NULL,
		game_mode   TEXT NOT NULL DEFAULT 'TDM',
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
	    soft_currency    INTEGER NOT NULL DEFAULT 10000 CHECK (soft_currency >= 0),
	    premium_currency INTEGER NOT NULL DEFAULT 0 CHECK (premium_currency >= 0),  -- Maps to DREAD's "hard_currency"
	    free_xp          INTEGER NOT NULL DEFAULT 0 CHECK (free_xp >= 0),
	    current_xp       INTEGER NOT NULL DEFAULT 0 CHECK (current_xp >= 0),
	    current_rank     INTEGER NOT NULL DEFAULT 1 CHECK (current_rank >= 1),
	    rank_xp          INTEGER NOT NULL DEFAULT 5000,  -- XP required for next rank (e.g., 5000 for Rank 2)
	    display_name     TEXT NOT NULL DEFAULT 'Local' CHECK (length(display_name) <= 32),
	    display_info     TEXT NOT NULL DEFAULT '' CHECK (length(display_info) <= 256),
	    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
	    updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
	)`,
	`CREATE TABLE IF NOT EXISTS player_fleets (
		user_id                   TEXT NOT NULL,
		fleet_id                  INTEGER NOT NULL,
		token                     TEXT NOT NULL,
		display_name              TEXT NOT NULL,
		fleet_type                INTEGER NOT NULL DEFAULT 1,
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
	    loadout_index            INTEGER NOT NULL DEFAULT 0,    -- First slot
	    loadout_name             TEXT NOT NULL,
	    position                 INTEGER NOT NULL DEFAULT 0,    -- First position
	    active                   INTEGER NOT NULL DEFAULT 0,    -- Inactive by default
	    weapon_primary_id        INTEGER NOT NULL DEFAULT -1,   -- -1 = unset
	    weapon_secondary_id      INTEGER NOT NULL DEFAULT -1,   -- -1 = unset
	    ability_primary_id       INTEGER NOT NULL DEFAULT -1,   -- -1 = unset
	    ability_secondary_id     INTEGER NOT NULL DEFAULT -1,   -- -1 = unset
	    ability_perimeter_id     INTEGER NOT NULL DEFAULT -1,   -- -1 = unset
	    ability_internal_id      INTEGER NOT NULL DEFAULT -1,   -- -1 = unset
	    perk_com_id              INTEGER NOT NULL DEFAULT -1,   -- -1 = unset
	    perk_weapon_id           INTEGER NOT NULL DEFAULT -1,   -- -1 = unset
	    perk_navigation_id       INTEGER NOT NULL DEFAULT -1,   -- -1 = unset
	    perk_engineer_id         INTEGER NOT NULL DEFAULT -1,   -- -1 = unset
	    created_at               TEXT NOT NULL DEFAULT (datetime('now')),
	    updated_at               TEXT NOT NULL DEFAULT (datetime('now')),
	    PRIMARY KEY (user_id, loadout_id),
	    FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS player_fleet_loadouts (
	    user_id    TEXT NOT NULL,
	    fleet_id   INTEGER NOT NULL,
	    position   INTEGER NOT NULL DEFAULT 0, 
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
	    state        TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'completed', 'expired', 'claimed', 'rerolled')),
	    progress     INTEGER NOT NULL DEFAULT 0,
	    completed_at TEXT,
	    payload      TEXT NOT NULL DEFAULT '{}',
	    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
	    updated_at   TEXT NOT NULL DEFAULT (datetime('now')),
	    PRIMARY KEY (user_id, contract_id),
	    FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE,
	    CHECK (progress >= 0 AND progress <= 100)  -- Progress as percentage
	)`,
	// Goal counters the CLIENT reports. It sends YA_IncrementPlayerStatsCounter
	// with counterId/counterSubId as STRINGS plus an increment -- captured
	// verbatim: counterId="Customize" counterSubId="Captain"/"Ship". Those names
	// are the client's, so storing what it sends is the only way to have the
	// real ones rather than guesses.
	`CREATE TABLE IF NOT EXISTS player_stats_counters (
	    user_id        TEXT NOT NULL,
	    counter_id     TEXT NOT NULL,
	    counter_sub_id TEXT NOT NULL DEFAULT '',
	    value          INTEGER NOT NULL DEFAULT 0,
	    updated_at     TEXT NOT NULL DEFAULT (datetime('now')),
	    PRIMARY KEY (user_id, counter_id, counter_sub_id),
	    FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS player_ribbons (
		user_id     TEXT NOT NULL,
		ribbon_type TEXT NOT NULL,
		count       INTEGER NOT NULL DEFAULT 0,
		created_at  TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (user_id, ribbon_type),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS player_season_progress (
		user_id     TEXT NOT NULL,
		season_id   TEXT NOT NULL,
		xp          INTEGER NOT NULL DEFAULT 0,
		level       INTEGER NOT NULL DEFAULT 1,
		created_at  TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (user_id, season_id),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS player_ship_xp (
		user_id     TEXT NOT NULL,
		ship_id     INTEGER NOT NULL,
		xp          INTEGER NOT NULL DEFAULT 0,
		created_at  TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (user_id, ship_id),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS player_purchases (
		user_id     TEXT NOT NULL,
		item_id     INTEGER NOT NULL,
		item_type   TEXT NOT NULL DEFAULT '',
		price_paid  INTEGER NOT NULL DEFAULT 0,
		currency    TEXT NOT NULL DEFAULT 'gp',
		purchased_at TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (user_id, item_id),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS player_pve_progress (
		user_id        TEXT NOT NULL,
		mode           TEXT NOT NULL,
		highest_wave   INTEGER NOT NULL DEFAULT 0,
		total_waves    INTEGER NOT NULL DEFAULT 0,
		boss_kills     INTEGER NOT NULL DEFAULT 0,
		total_kills    INTEGER NOT NULL DEFAULT 0,
		best_score     INTEGER NOT NULL DEFAULT 0,
		created_at     TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at     TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (user_id, mode),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS player_boss_kills (
		user_id     TEXT NOT NULL,
		boss_id     TEXT NOT NULL,
		kill_count  INTEGER NOT NULL DEFAULT 0,
		first_kill  TEXT,
		last_kill   TEXT,
		created_at  TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (user_id, boss_id),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS player_ai_preferences (
		user_id          TEXT PRIMARY KEY,
		difficulty       TEXT NOT NULL DEFAULT 'Normal',
		ai_behavior      TEXT NOT NULL DEFAULT 'Balanced',
		spawn_rate       REAL NOT NULL DEFAULT 1.0,
		boss_frequency   REAL NOT NULL DEFAULT 1.0,
		created_at       TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at       TEXT NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS player_membership (
		user_id     TEXT PRIMARY KEY,
		expires_at  INTEGER NOT NULL DEFAULT 0,  -- unix epoch seconds; 0 = never purchased / not active
		created_at  TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	`ALTER TABLE player_state ADD COLUMN login_streak INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE player_state ADD COLUMN last_login_date TEXT NOT NULL DEFAULT ''`,
	`CREATE TABLE IF NOT EXISTS player_faction_reputation (
		user_id     TEXT NOT NULL,
		faction_id  INTEGER NOT NULL,
		reputation  INTEGER NOT NULL DEFAULT 0,
		created_at  TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (user_id, faction_id),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
	// Opaque client-authored save blobs. The client serialises some of its own
	// state (onboarding rule progress, call-to-arms history) into a compressed
	// binary blob, uploads it with YA_SaveGame / YA_SaveCtAData, and expects it
	// back verbatim in the SGD / SCtA fields of YA_PlayerGet. The server never
	// interprets the contents — it is a per-player key/value store.
	`CREATE TABLE IF NOT EXISTS player_save_blobs (
		user_id     TEXT NOT NULL,
		slot        TEXT NOT NULL,  -- 'SGD' (onboarding) or 'SCtA' (call to arms)
		data        BLOB NOT NULL,
		updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (user_id, slot),
		FOREIGN KEY (user_id) REFERENCES player_state(user_id) ON DELETE CASCADE
	)`,
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

	// Migrations are keyed by INDEX, which makes the list's ORDER load-bearing:
	// editing it shifts every later version number. That has already bitten us
	// once -- this database recorded versions 1..23 while only 21 migrations
	// were declared, so player_stats_counters, sitting at index 11 after an
	// edit, was treated as long since applied and was never created. Its
	// absence surfaced only as a "mmog: read stats counters ... no such table"
	// warning, but playerStatsCounters() returns nothing on error, so every
	// career goal keyed on a counter silently read 0.
	//
	// So already-applied entries are re-run when, and only when, they are safe
	// to re-run: CREATE ... IF NOT EXISTS is, ALTER TABLE ... ADD COLUMN is
	// NOT. Re-running everything unconditionally looked tempting and took the
	// server down with "migration 20: duplicate column name: login_streak".
	for i, ddl := range migrations {
		v := i + 1
		if v <= current {
			if repeatableDDL(ddl) {
				if _, err := db.Exec(ddl); err != nil {
					return fmt.Errorf("re-apply migration %d: %w", v, err)
				}
			}
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

// repeatableDDL reports whether a migration can be executed again on a database
// that already has it. Only guarded CREATEs qualify; anything that mutates an
// existing table (ALTER TABLE ... ADD COLUMN) fails the second time.
func repeatableDDL(ddl string) bool {
	if strings.Contains(strings.ToUpper(ddl), "ALTER TABLE") {
		return false
	}
	return strings.Contains(strings.ToUpper(ddl), "IF NOT EXISTS")
}
