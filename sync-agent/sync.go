package main

// Table specs: exactly which user rows roam between clusters. Column lists
// mirror the service schemas 1:1 — if a service adds a column, it roams only
// after being added here (a deliberate choke point, not an accident).
//
// Encoding: rows are scanned driver-natively (INTEGER→int64, REAL→float64,
// TEXT→string, BLOB→[]byte, NULL→nil) and go through encoding/json, which
// base64-encodes []byte by itself. On apply the same values are inserted
// back; SQLite column affinity restores INTEGER/REAL from JSON numbers.
// One exception: base64 strings must be decoded for real BLOB columns
// (blobColumns below) — everywhere else a string stays a string.

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
)

type tableSpec struct {
	db    string // auth | mmog | legacy
	table string
	cols  []string
	// idCol is the owner column (almost always user_id; auth.users uses id,
	// player_ignores uses pid).
	idCol string
	// upsert parents (FK targets) are INSERT OR REPLACEd so referencing rows
	// and sessions survive; everything else is DELETE + INSERT per user.
	upsert bool
	// pairCols marks a pair table (player_friends): a row belongs to TWO
	// users, so it is read by either column and never deleted — pairs
	// upsert with accepted-wins (acceptance is monotonic, both sides
	// converge; mirrors the live auto-accept for cross requests).
	pairCols [2]string
	// maxCol marks a monotonic counter (player_career_claims.claimed_stages):
	// per-key MAX instead of replace, so claims on two clusters add up
	// instead of one side's LWW snapshot reverting the other's.
	maxCol string
	// conflict is the ON CONFLICT target for the pair/max modes.
	conflict []string
}

func (s tableSpec) ownerCol() string {
	if s.idCol != "" {
		return s.idCol
	}
	return "user_id"
}

var syncedTables = []tableSpec{
	{db: "auth", table: "users", idCol: "id", cols: []string{"id", "username", "email", "password_hash", "created_at", "banned_at", "steam_id"}, upsert: true},
	{db: "auth", table: "bans", cols: []string{"id", "user_id", "reason", "banned_by", "expires_at", "created_at"}},
	{db: "mmog", table: "player_state", cols: []string{"user_id", "soft_currency", "premium_currency", "free_xp", "current_xp", "current_rank", "rank_xp", "display_name", "display_info", "login_streak", "last_login_date", "created_at", "updated_at"}, upsert: true},
	{db: "mmog", table: "player_fleets", cols: []string{"user_id", "fleet_id", "token", "display_name", "fleet_type", "active", "flagship_ship_id", "flagship_loadout_id", "flagship_loadout_index", "created_at", "updated_at"}},
	{db: "mmog", table: "player_ship_loadouts", cols: []string{"user_id", "loadout_id", "native_loadout_id", "precast_loadout_id", "ship_id", "loadout_index", "loadout_name", "position", "active", "weapon_primary_id", "weapon_secondary_id", "ability_primary_id", "ability_secondary_id", "ability_perimeter_id", "ability_internal_id", "perk_com_id", "perk_weapon_id", "perk_navigation_id", "perk_engineer_id", "created_at", "updated_at"}},
	{db: "mmog", table: "player_fleet_loadouts", cols: []string{"user_id", "fleet_id", "position", "loadout_id"}},
	{db: "mmog", table: "player_officers", cols: []string{"user_id", "officer_id", "payload", "created_at", "updated_at"}},
	{db: "mmog", table: "player_purchases", cols: []string{"user_id", "item_id", "item_type", "price_paid", "currency", "purchased_at", "research_xp"}},
	{db: "mmog", table: "player_ship_xp", cols: []string{"user_id", "ship_id", "xp", "created_at", "updated_at"}},
	{db: "mmog", table: "player_contracts", cols: []string{"user_id", "contract_id", "state", "progress", "completed_at", "payload", "created_at", "updated_at"}},
	{db: "mmog", table: "player_season_progress", cols: []string{"user_id", "season_id", "xp", "level", "created_at", "updated_at"}},
	{db: "mmog", table: "player_stats_counters", cols: []string{"user_id", "counter_id", "counter_sub_id", "value", "updated_at"}},
	{db: "mmog", table: "player_ribbons", cols: []string{"user_id", "ribbon_type", "count", "created_at", "updated_at"}},
	{db: "mmog", table: "player_membership", cols: []string{"user_id", "expires_at", "created_at", "updated_at"}},
	{db: "mmog", table: "player_faction_reputation", cols: []string{"user_id", "faction_id", "reputation", "created_at", "updated_at"}},
	{db: "mmog", table: "player_save_blobs", cols: []string{"user_id", "slot", "data", "updated_at"}},
	{db: "mmog", table: "player_pve_progress", cols: []string{"user_id", "mode", "highest_wave", "total_waves", "boss_kills", "total_kills", "best_score", "created_at", "updated_at"}},
	{db: "mmog", table: "player_boss_kills", cols: []string{"user_id", "boss_id", "kill_count", "first_kill", "last_kill", "created_at", "updated_at"}},
	{db: "mmog", table: "player_ai_preferences", cols: []string{"user_id", "difficulty", "ai_behavior", "spawn_rate", "boss_frequency", "created_at", "updated_at"}},
	{db: "mmog", table: "player_friends", cols: []string{"pid_a", "pid_b", "requester_id", "state", "created_at"},
		pairCols: [2]string{"pid_a", "pid_b"}, conflict: []string{"pid_a", "pid_b"}},
	{db: "mmog", table: "player_ignores", idCol: "pid", cols: []string{"pid", "ignored_id", "created_at"}},
	{db: "mmog", table: "player_career_claims", cols: []string{"user_id", "goal_id", "claimed_stages", "updated_at"},
		maxCol: "claimed_stages", conflict: []string{"user_id", "goal_id"}},
	{db: "legacy", table: "player_profiles", cols: []string{"id", "user_id", "display_name", "created_at", "updated_at"}},
	{db: "legacy", table: "player_stats", cols: []string{"user_id", "kills", "deaths", "matches_played", "wins", "xp_total", "credits", "assists", "damage_dealt", "damage_taken", "healing_done", "control_points", "double_kills", "triple_kills", "multikills", "kill_streak", "modules_used", "energy_spent", "distance_traveled", "time_played"}},
	{db: "legacy", table: "player_inventory", cols: []string{"id", "user_id", "item_type", "item_id", "acquired_at"}},
}

// blobColumns need base64 decoding on apply (encoding/json leaves them as
// strings after a master round-trip).
var blobColumns = map[string]bool{
	"mmog.player_save_blobs.data": true,
}

// Deliberately NOT synced (local-only state): sessions (login tokens),
// queue/matches/slots (live matchmaking), battle_results + match_history
// (per-cluster audit), chat_messages (local chatter), client_reports
// (diagnostics), launcher_tiles (cluster content). Unfriending does not
// propagate either: pair rows upsert, never delete.

type databases struct {
	auth   *sql.DB
	mmog   *sql.DB
	legacy *sql.DB
}

func (d databases) byName(name string) *sql.DB {
	switch name {
	case "auth":
		return d.auth
	case "mmog":
		return d.mmog
	default:
		return d.legacy
	}
}

// userIDs lists every account known locally (auth users drive the sync;
// mmog-only pids without an auth row cannot log in anywhere).
func userIDs(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT id FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// readRows returns every row for one user as column-name maps, in spec order.
// Pair tables match either column (a friendship belongs to both sides).
func readRows(db *sql.DB, spec tableSpec, userID string) ([]map[string]any, error) {
	where, args := spec.ownerCol()+"=?", []any{userID}
	if spec.pairCols[0] != "" {
		where = spec.pairCols[0] + "=? OR " + spec.pairCols[1] + "=?"
		args = []any{userID, userID}
	}
	rows, err := db.Query(fmt.Sprintf("SELECT %s FROM %s WHERE %s",
		strings.Join(spec.cols, ","), spec.table, where), args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", spec.table, err)
	}
	defer func() { _ = rows.Close() }()
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(spec.cols))
		ptrs := make([]any, len(spec.cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("%s scan: %w", spec.table, err)
		}
		m := make(map[string]any, len(spec.cols))
		for i, col := range spec.cols {
			m[col] = vals[i]
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// writeRows replaces one user's rows (or upserts parents) from maps, reading
// values in spec order so INSERT columns always line up.
func writeRows(db *sql.DB, spec tableSpec, userID string, rows []map[string]any) error {
	if spec.upsert {
		for _, m := range rows {
			place := make([]string, len(spec.cols))
			vals := make([]any, len(spec.cols))
			for i, col := range spec.cols {
				place[i] = "?"
				vals[i] = coerceValue(spec, col, m[col])
			}
			q := fmt.Sprintf("INSERT OR REPLACE INTO %s(%s) VALUES(%s)",
				spec.table, strings.Join(spec.cols, ","), strings.Join(place, ","))
			if _, err := db.Exec(q, vals...); err != nil {
				return fmt.Errorf("%s upsert: %w", spec.table, err)
			}
		}
		return nil
	}
	if spec.pairCols[0] != "" {
		return writePairs(db, spec, rows)
	}
	if spec.maxCol != "" {
		return writeMaxed(db, spec, rows)
	}
	if _, err := db.Exec(fmt.Sprintf("DELETE FROM %s WHERE %s=?", spec.table, spec.ownerCol()), userID); err != nil {
		return fmt.Errorf("%s clear: %w", spec.table, err)
	}
	for _, m := range rows {
		place := make([]string, len(spec.cols))
		vals := make([]any, len(spec.cols))
		for i, col := range spec.cols {
			place[i] = "?"
			vals[i] = coerceValue(spec, col, m[col])
		}
		q := fmt.Sprintf("INSERT INTO %s(%s) VALUES(%s)",
			spec.table, strings.Join(spec.cols, ","), strings.Join(place, ","))
		if _, err := db.Exec(q, vals...); err != nil {
			return fmt.Errorf("%s insert: %w", spec.table, err)
		}
	}
	return nil
}

// writePairs upserts pair rows (player_friends) without deleting: a pair
// belongs to two users and one side's sync must never drop the other's row.
// Acceptance is monotonic — accepted on either side (or cross-requested, the
// live auto-accept case) settles the pair everywhere.
func writePairs(db *sql.DB, spec tableSpec, rows []map[string]any) error {
	for _, m := range rows {
		vals := make([]any, len(spec.cols))
		for i, col := range spec.cols {
			vals[i] = coerceValue(spec, col, m[col])
		}
		place := make([]string, len(spec.cols))
		for i := range place {
			place[i] = "?"
		}
		t := spec.table
		q := fmt.Sprintf(`INSERT INTO %s(%s) VALUES(%s) ON CONFLICT(%s) DO UPDATE SET state=
			CASE WHEN excluded.state='accepted' OR %s.state='accepted' THEN 'accepted'
			WHEN %s.requester_id <> excluded.requester_id THEN 'accepted'
			ELSE %s.state END`,
			t, strings.Join(spec.cols, ","), strings.Join(place, ","),
			strings.Join(spec.conflict, ","), t, t, t)
		if _, err := db.Exec(q, vals...); err != nil {
			return fmt.Errorf("%s pair upsert: %w", t, err)
		}
	}
	return nil
}

// writeMaxed merges monotonic counters (player_career_claims) per key with
// MAX: claims on two clusters between syncs add up instead of the older
// snapshot reverting the newer one. updated_at follows the winning side.
func writeMaxed(db *sql.DB, spec tableSpec, rows []map[string]any) error {
	for _, m := range rows {
		vals := make([]any, len(spec.cols))
		for i, col := range spec.cols {
			vals[i] = coerceValue(spec, col, m[col])
		}
		place := make([]string, len(spec.cols))
		for i := range place {
			place[i] = "?"
		}
		t := spec.table
		q := fmt.Sprintf(`INSERT INTO %s(%s) VALUES(%s) ON CONFLICT(%s) DO UPDATE SET
			%s=max(%s.%s,excluded.%s),
			updated_at=CASE WHEN excluded.%s>%s.%s THEN excluded.updated_at ELSE %s.updated_at END`,
			t, strings.Join(spec.cols, ","), strings.Join(place, ","),
			strings.Join(spec.conflict, ","),
			spec.maxCol, t, spec.maxCol, spec.maxCol,
			spec.maxCol, t, spec.maxCol, t)
		if _, err := db.Exec(q, vals...); err != nil {
			return fmt.Errorf("%s max merge: %w", t, err)
		}
	}
	return nil
}

// coerceValue decodes base64 back to bytes for real BLOB columns (everything
// else passes through; SQLite affinity restores INTEGER/REAL from JSON
// numbers, NULL stays NULL).
func coerceValue(spec tableSpec, col string, v any) any {
	s, ok := v.(string)
	if !ok || !blobColumns[spec.db+"."+spec.table+"."+col] {
		return v
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return v
	}
	return raw
}
