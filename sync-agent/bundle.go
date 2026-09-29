package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Identity forms: auth + legacy key users by dashed UUID, mmog by the same
// UUID with hyphens stripped (see matchmaker.normalizeUserID). Bundles are
// keyed normalized; apply re-adds dashes where the local schema needs them.
func normID(id string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(id), "-", ""))
}

func denormID(normalized string) string {
	if len(normalized) != 32 {
		return normalized
	}
	return normalized[0:8] + "-" + normalized[8:12] + "-" + normalized[12:16] + "-" +
		normalized[16:20] + "-" + normalized[20:32]
}

// bundle is one user's full roamable state.
type bundle struct {
	UserID    string                       `json:"user_id"`
	UpdatedAt string                       `json:"updated_at"`
	Tables    map[string][]map[string]any  `json:"tables"`
}

func tableKey(spec tableSpec) string {
	return spec.db + "." + spec.table
}

// buildBundle reads one user (by normalized id) from all local DBs.
func buildBundle(dbs databases, userID string, at time.Time) (*bundle, error) {
	b := &bundle{UserID: userID, UpdatedAt: at.UTC().Format(time.RFC3339), Tables: map[string][]map[string]any{}}
	for _, spec := range syncedTables {
		localID := userID
		if spec.db != "mmog" {
			localID = denormID(userID)
		}
		rows, err := readRows(dbs.byName(spec.db), spec, localID)
		if err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []map[string]any{}
		}
		b.Tables[tableKey(spec)] = rows
	}
	return b, nil
}

// headline extracts balance/rank/ships for the master's stats columns.
func (b *bundle) headline() (credits, rank, ships int) {
	rank = 1
	for _, m := range b.Tables["mmog.player_state"] {
		credits = int(jsonNumber(m["soft_currency"]))
		if r := int(jsonNumber(m["current_rank"])); r > 0 {
			rank = r
		}
	}
	ships = len(b.Tables["mmog.player_ship_loadouts"])
	return credits, rank, ships
}

func jsonNumber(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	default:
		return 0
	}
}

// applyBundle replaces one user's local rows with the bundle. Parents
// (auth.users, player_state) upsert so referencing rows and sessions
// survive; children are delete+insert per spec, which lists parents first.
func applyBundle(dbs databases, b *bundle) error {
	// auth.users carries "id", not "user_id": upsert it explicitly.
	for _, m := range b.Tables["auth.users"] {
		if _, err := dbs.auth.Exec(`INSERT INTO users(id,username,email,password_hash,created_at,banned_at,steam_id)
			VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET username=excluded.username,
			email=excluded.email, password_hash=excluded.password_hash,
			banned_at=excluded.banned_at, steam_id=excluded.steam_id`,
			denormID(b.UserID), strField(m, "username"), strField(m, "email"),
			strField(m, "password_hash"), strField(m, "created_at"),
			nilIfEmpty(strField(m, "banned_at")), nilIfEmpty(strField(m, "steam_id"))); err != nil {
			return err
		}
	}
	for _, spec := range syncedTables {
		if spec.db == "auth" && spec.table == "users" {
			continue
		}
		rows, ok := b.Tables[tableKey(spec)]
		if !ok {
			continue
		}
		wantID := b.UserID
		if spec.db != "mmog" {
			wantID = denormID(b.UserID)
		}
		fixed := make([]map[string]any, 0, len(rows))
		for _, m := range rows {
			cp := make(map[string]any, len(m)+1)
			for k, v := range m {
				cp[k] = v
			}
			cp["user_id"] = wantID
			fixed = append(fixed, cp)
		}
		if err := writeRows(dbs.byName(spec.db), spec, wantID, fixed); err != nil {
			return err
		}
	}
	return nil
}

func strField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// isUniqueConflict reports a SQLite UNIQUE violation (go-sqlite3 surfaces it
// as "UNIQUE constraint failed: ..."). Used to skip a conflicting row rather
// than abort a whole apply.
func isUniqueConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// applyIdentity upserts pulled accounts and replaces their bans, maintaining
// users.banned_at from the ban rows (a global ban lands even for accounts
// this cluster never saw; an empty set lifts it).
//
// A pulled account whose name or address collides with a DIFFERENT local id
// (same callsign/email registered twice in the sync window) is skipped, not
// fatal: one duplicate must never block every other account's apply. The
// local row wins by staying; the directory pre-check at registration keeps
// this rare.
func applyIdentity(dbs databases, users []map[string]any, bans []map[string]any) error {
	skipped := map[string]bool{}
	for _, u := range users {
		id, _ := u["user_id"].(string)
		if normID(id) == "" {
			continue
		}
		dashed := denormID(normID(id))
		if _, err := dbs.auth.Exec(`INSERT INTO users(id,username,email,password_hash,created_at,banned_at,steam_id)
			VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET username=excluded.username,
			email=excluded.email, password_hash=excluded.password_hash,
			banned_at=excluded.banned_at, steam_id=excluded.steam_id`,
			dashed, strField(u, "username"), strField(u, "email"), strField(u, "password_hash"),
			strField(u, "created_at"), nilIfEmpty(strField(u, "banned_at")), nilIfEmpty(strField(u, "steam_id"))); err != nil {
			if isUniqueConflict(err) {
				skipped[dashed] = true
				continue
			}
			return err
		}
	}
	byUser := map[string][]map[string]any{}
	for _, b := range bans {
		id, _ := b["user_id"].(string)
		if normID(id) == "" {
			continue
		}
		uid := denormID(normID(id))
		if skipped[uid] {
			continue
		}
		byUser[uid] = append(byUser[uid], b)
	}
	// Replace per pulled user (not just per ban row): the pull carries the
	// full ban state, so a missing row means lifted, not unknown.
	touched := map[string]bool{}
	for _, u := range users {
		id, _ := u["user_id"].(string)
		if normID(id) == "" {
			continue
		}
		uid := denormID(normID(id))
		if skipped[uid] {
			continue
		}
		touched[uid] = true
	}
	for uid := range touched {
		if _, err := dbs.auth.Exec(`DELETE FROM bans WHERE user_id=?`, uid); err != nil {
			return err
		}
	}
	for uid, list := range byUser {
		for _, b := range list {
			id, _ := b["id"].(string)
			if strings.TrimSpace(id) == "" {
				continue
			}
			if _, err := dbs.auth.Exec(`INSERT INTO bans(id,user_id,reason,banned_by,expires_at,created_at)
				VALUES(?,?,?,?,?,?)`, id, uid, strField(b, "reason"), strField(b, "banned_by"),
				nilIfEmpty(strField(b, "expires_at")), strField(b, "created_at")); err != nil {
				return err
			}
		}
		if _, err := dbs.auth.Exec(`UPDATE users SET banned_at=
			(SELECT created_at FROM bans WHERE user_id=users.id ORDER BY created_at DESC LIMIT 1)
			WHERE id=?`, uid); err != nil {
			return err
		}
	}
	// Users in the payload whose bans vanished entirely clear as well.
	for uid := range touched {
		if _, ok := byUser[uid]; ok {
			continue
		}
		if _, err := dbs.auth.Exec(`UPDATE users SET banned_at=NULL WHERE id=?`, uid); err != nil {
			return err
		}
	}
	return nil
}
