package main

import (
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	_ "github.com/mattn/go-sqlite3"
)

// testDBs builds empty databases with the tables the specs query. Column
// types mirror production where the test asserts values (INTEGER stays
// INTEGER through the round trip); the rest are TEXT because the test only
// counts those rows.
func testDBs(t *testing.T) databases {
	t.Helper()
	open := func() *sql.DB {
		db, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	auth, mmog, legacy := open(), open(), open()
	stmts := []struct {
		db  *sql.DB
		ddl string
	}{
		{auth, `CREATE TABLE users(id TEXT PRIMARY KEY, username TEXT, email TEXT, password_hash TEXT, created_at TEXT, banned_at TEXT, steam_id TEXT)`},
		{auth, `CREATE TABLE bans(id TEXT PRIMARY KEY, user_id TEXT, reason TEXT, banned_by TEXT, expires_at TEXT, created_at TEXT)`},
		{mmog, `CREATE TABLE player_state(user_id TEXT PRIMARY KEY, soft_currency INTEGER, premium_currency INTEGER, free_xp INTEGER, current_xp INTEGER, current_rank INTEGER, rank_xp INTEGER, display_name TEXT, display_info TEXT, login_streak INTEGER, last_login_date TEXT, created_at TEXT, updated_at TEXT)`},
		{mmog, `CREATE TABLE player_ship_xp(user_id TEXT, ship_id INTEGER, xp INTEGER, created_at TEXT, updated_at TEXT, PRIMARY KEY (user_id, ship_id))`},
		{mmog, `CREATE TABLE player_save_blobs(user_id TEXT, slot TEXT, data BLOB, updated_at TEXT, PRIMARY KEY (user_id, slot))`},
	}
	// Every other spec table as all-TEXT (row counts only).
	textTables := map[string][]string{}
	for _, spec := range syncedTables {
		key := spec.db + "." + spec.table
		if _, ok := textTables[key]; ok {
			continue
		}
		textTables[key] = spec.cols
	}
	dbs := map[string]*sql.DB{"auth": auth, "mmog": mmog, "legacy": legacy}
	skip := map[string]bool{
		"auth.users": true, "auth.bans": true, "mmog.player_state": true,
		"mmog.player_ship_xp": true, "mmog.player_save_blobs": true,
	}
	for key, cols := range textTables {
		if skip[key] {
			continue
		}
		parts := make([]string, len(cols))
		for i, c := range cols {
			parts[i] = c + " TEXT"
		}
		dbName := key[:strings.Index(key, ".")]
		table := key[strings.Index(key, ".")+1:]
		def := "CREATE TABLE " + table + "(" + strings.Join(parts, ",") + ")"
		if _, err := dbs[dbName].Exec(def); err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
	}
	for _, s := range stmts {
		if _, err := s.db.Exec(s.ddl); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	return databases{auth: auth, mmog: mmog, legacy: legacy}
}

const roundtripPID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const roundtripDashed = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

func seedRoundtrip(t *testing.T, dbs databases) {
	t.Helper()
	if _, err := dbs.auth.Exec(`INSERT INTO users(id,username,email,password_hash,created_at)
		VALUES(?,?,?,?,?)`, roundtripDashed, "alice", "a@x.org", "bcrypt-hash", "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := dbs.mmog.Exec(`INSERT INTO player_state(user_id,soft_currency,premium_currency,free_xp,current_xp,current_rank,rank_xp,display_name)
		VALUES(?,?,0,700,0,5,0,'Alice')`, roundtripPID, 15000); err != nil {
		t.Fatal(err)
	}
	if _, err := dbs.mmog.Exec(`INSERT INTO player_ship_xp(user_id,ship_id,xp) VALUES(?,?,?)`,
		roundtripPID, 10, 5000); err != nil {
		t.Fatal(err)
	}
	if _, err := dbs.mmog.Exec(`INSERT INTO player_save_blobs(user_id,slot,data) VALUES(?,?,?)`,
		roundtripPID, "SGD", []byte{0, 1, 2, 250, 255}); err != nil {
		t.Fatal(err)
	}
}

func TestBundleRoundtrip(t *testing.T) {
	src := testDBs(t)
	seedRoundtrip(t, src)
	b, err := buildBundle(src, roundtripPID, time.Now().UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dst := testDBs(t)
	if err := applyBundle(dst, b); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Balances survive with types (INTEGER, not "15000").
	var credits int64
	if err := dst.mmog.QueryRow(`SELECT soft_currency FROM player_state WHERE user_id=?`,
		roundtripPID).Scan(&credits); err != nil || credits != 15000 {
		t.Errorf("credits = %v, %v; want 15000", credits, err)
	}
	// Blobs survive byte-identical (base64 round trip).
	var blob []byte
	if err := dst.mmog.QueryRow(`SELECT data FROM player_save_blobs WHERE user_id=? AND slot='SGD'`,
		roundtripPID).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 1, 2, 250, 255}
	if len(blob) != len(want) {
		t.Fatalf("blob len %d, want %d", len(blob), len(want))
	}
	for i := range want {
		if blob[i] != want[i] {
			t.Fatalf("blob differs at %d", i)
		}
	}
	// The auth id is re-dashed on apply.
	var id string
	if err := dst.auth.QueryRow(`SELECT id FROM users WHERE username='alice'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != roundtripDashed {
		t.Errorf("auth id = %q, want dashed %q", id, roundtripDashed)
	}
	// Row counts match everywhere (pair tables match either column).
	for _, spec := range syncedTables {
		var a, c int
		dbA := src.byName(spec.db)
		dbC := dst.byName(spec.db)
		col := spec.ownerCol()
		keyA, keyC := roundtripPID, roundtripPID
		if spec.db != "mmog" {
			keyA, keyC = roundtripDashed, roundtripDashed
		}
		q, argsA, argsC := "SELECT COUNT(*) FROM "+spec.table+" WHERE "+col+"=?", []any{keyA}, []any{keyC}
		if spec.pairCols[0] != "" {
			q = "SELECT COUNT(*) FROM " + spec.table + " WHERE " +
				spec.pairCols[0] + "=? OR " + spec.pairCols[1] + "=?"
			argsA, argsC = []any{keyA, keyA}, []any{keyC, keyC}
		}
		if err := dbA.QueryRow(q, argsA...).Scan(&a); err != nil {
			t.Fatalf("count %s: %v", spec.table, err)
		}
		if err := dbC.QueryRow(q, argsC...).Scan(&c); err != nil {
			t.Fatalf("count %s: %v", spec.table, err)
		}
		if a != c {
			t.Errorf("%s rows: src %d, dst %d", spec.table, a, c)
		}
	}
}

func TestApplyIdentityBansAndUnbans(t *testing.T) {
	dbs := testDBs(t)
	uid := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dashed := denormID(uid)
	users := []map[string]any{{"user_id": uid, "username": "bob", "email": "b@x.org",
		"password_hash": "h", "created_at": "2026-01-01", "banned_at": "", "steam_id": ""}}
	bans := []map[string]any{{"id": "ban-1", "user_id": uid, "reason": "griefing",
		"banned_by": "admin", "expires_at": "", "created_at": "2026-02-01"}}
	if err := applyIdentity(dbs, users, bans); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var bannedAt string
	if err := dbs.auth.QueryRow(`SELECT banned_at FROM users WHERE id=?`, dashed).Scan(&bannedAt); err != nil {
		t.Fatal(err)
	}
	if bannedAt == "" {
		t.Error("banned_at not maintained from ban rows")
	}
	// Unban arrives as a user without ban rows: bans clear, banned_at NULLs.
	if err := applyIdentity(dbs, users, nil); err != nil {
		t.Fatalf("apply unban: %v", err)
	}
	var unbanned any
	if err := dbs.auth.QueryRow(`SELECT banned_at FROM users WHERE id=?`, dashed).Scan(&unbanned); err != nil {
		t.Fatal(err)
	}
	if unbanned != nil {
		t.Errorf("banned_at = %v after unban, want NULL", unbanned)
	}
	var remaining int
	if err := dbs.auth.QueryRow(`SELECT COUNT(*) FROM bans WHERE user_id=?`, dashed).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Errorf("%d ban rows left after unban", remaining)
	}
}

func TestSyncTimeAfter(t *testing.T) {
	if !syncTimeAfter("2026-09-29T12:01:00Z", "2026-09-29T12:00:00Z") {
		t.Error("newer must win")
	}
	if syncTimeAfter("2026-09-29T12:00:00Z", "2026-09-29T12:01:00Z") {
		t.Error("older must lose")
	}
	if syncTimeAfter("2026-09-29T12:00:00Z", "2026-09-29T12:00:00Z") {
		t.Error("tie must not overwrite (keeps local unsynced earnings)")
	}
	if syncTimeAfter("garbage", "2026-09-29T12:00:00Z") {
		t.Error("garbage must never win")
	}
	if !syncTimeAfter("2026-09-29T12:00:00Z", "") {
		t.Error("anything beats unknown")
	}
}

func TestInLiveMatch(t *testing.T) {
	dbs := testDBs(t)
	for _, ddl := range []string{
		`CREATE TABLE matches(id TEXT PRIMARY KEY, status TEXT)`,
		`CREATE TABLE match_slots(match_id TEXT, user_id TEXT)`,
	} {
		if _, err := dbs.mmog.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	uid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := dbs.mmog.Exec(`INSERT INTO matches(id,status) VALUES('m1','active')`); err != nil {
		t.Fatal(err)
	}
	if inLiveMatch(dbs.mmog, uid) {
		t.Error("empty slots must not count as in-match")
	}
	if _, err := dbs.mmog.Exec(`INSERT INTO match_slots(match_id,user_id) VALUES('m1',?)`, uid); err != nil {
		t.Fatal(err)
	}
	if !inLiveMatch(dbs.mmog, uid) {
		t.Error("active slot must count as in-match")
	}
	// Dashed spelling of the same account must match too.
	if !inLiveMatch(dbs.mmog, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa") {
		t.Error("dashed id form must match the same slot")
	}
	if _, err := dbs.mmog.Exec(`UPDATE matches SET status='finished' WHERE id='m1'`); err != nil {
		t.Fatal(err)
	}
	if inLiveMatch(dbs.mmog, uid) {
		t.Error("finished match must not count as in-match")
	}
}

func TestLoopSkipsEverythingWhenOptedOut(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	defer srv.Close()
	dir := t.TempDir()
	optOut := filepath.Join(dir, "dn-no-master-server.txt")
	if err := os.WriteFile(optOut, []byte("operator choice\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &agent{
		cfg: config{
			masterURL: srv.URL, cluster: "shy",
			secret: "irrelevant", noMasterFile: optOut,
			stateFile: filepath.Join(dir, "sync-state.json"),
		},
		log: logrus.New(), dbs: testDBs(t),
		http: srv.Client(),
	}
	a.loop(loadState(a.cfg.stateFile), false)
	if hit {
		t.Error("opted-out cluster must send no sync traffic at all")
	}
	if _, err := os.Stat(a.cfg.stateFile); !os.IsNotExist(err) {
		t.Error("opted-out loop must not write state")
	}
}

func findSpec(db, table string) tableSpec {
	for _, s := range syncedTables {
		if s.db == db && s.table == table {
			return s
		}
	}
	return tableSpec{}
}

func TestFriendsMergeAcceptedWins(t *testing.T) {
	dbs := testDBs(t)
	// Production shape (the shared helper builds PK-less TEXT tables):
	// the pair upsert needs PRIMARY KEY(pid_a,pid_b) for ON CONFLICT.
	if _, err := dbs.mmog.Exec(`DROP TABLE player_friends`); err != nil {
		t.Fatal(err)
	}
	if _, err := dbs.mmog.Exec(`CREATE TABLE player_friends(pid_a TEXT, pid_b TEXT,
		requester_id TEXT, state TEXT, created_at TEXT, PRIMARY KEY(pid_a,pid_b))`); err != nil {
		t.Fatal(err)
	}
	a := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	b := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	apply := func(rows []map[string]any) {
		t.Helper()
		if err := applyBundle(dbs, &bundle{UserID: a,
			Tables: map[string][]map[string]any{"mmog.player_friends": rows}}); err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
	state := func() string {
		t.Helper()
		var s string
		if err := dbs.mmog.QueryRow(`SELECT state FROM player_friends WHERE pid_a=? AND pid_b=?`,
			a, b).Scan(&s); err != nil {
			t.Fatalf("read: %v", err)
		}
		return s
	}
	mk := func(requester, st string) []map[string]any {
		return []map[string]any{{"pid_a": a, "pid_b": b, "requester_id": requester,
			"state": st, "created_at": "2026-09-29T12:00:00Z"}}
	}
	if _, err := dbs.mmog.Exec(`INSERT INTO player_friends(pid_a,pid_b,requester_id,state,created_at)
		VALUES(?,?,?,'pending','2026-09-29T11:00:00Z')`, a, b, a); err != nil {
		t.Fatal(err)
	}
	// Incoming accept settles a pending pair.
	apply(mk(a, "accepted"))
	if state() != "accepted" {
		t.Fatalf("state = %q, want accepted", state())
	}
	// A stale pending report must not reopen an accepted pair.
	apply(mk(a, "pending"))
	if state() != "accepted" {
		t.Fatalf("state = %q, want still accepted", state())
	}
	// Cross request (both pending, different requester) auto-accepts, like live.
	if _, err := dbs.mmog.Exec(`UPDATE player_friends SET state='pending',requester_id=?`, a); err != nil {
		t.Fatal(err)
	}
	apply(mk(b, "pending"))
	if state() != "accepted" {
		t.Fatalf("state = %q, want accepted via cross request", state())
	}
	// Pair reads match either side: B's bundle carries the same row.
	rows, err := readRows(dbs.mmog, findSpec("mmog", "player_friends"), b)
	if err != nil || len(rows) != 1 {
		t.Fatalf("B-side read = %d rows, err %v", len(rows), err)
	}
}

func TestIgnoresRoam(t *testing.T) {
	dbs := testDBs(t)
	pid, ignored := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := dbs.mmog.Exec(`INSERT INTO player_ignores(pid,ignored_id,created_at)
		VALUES(?,?,'2026-09-29T11:00:00Z')`, pid, ignored); err != nil {
		t.Fatal(err)
	}
	b, err := buildBundle(dbs, pid, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Tables["mmog.player_ignores"]) != 1 {
		t.Fatalf("bundle ignores = %d, want 1", len(b.Tables["mmog.player_ignores"]))
	}
	other := testDBs(t)
	if err := applyBundle(other, b); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := other.mmog.QueryRow(`SELECT COUNT(*) FROM player_ignores WHERE pid=? AND ignored_id=?`,
		pid, ignored).Scan(&n); err != nil || n != 1 {
		t.Fatalf("ignore not applied (n=%d, err=%v)", n, err)
	}
}

func TestCareerClaimsTakeMax(t *testing.T) {
	dbs := testDBs(t)
	// The shared helper builds all-TEXT tables; claims need the production
	// INTEGER affinity or MAX() compares lexically ('10' < '9').
	if _, err := dbs.mmog.Exec(`DROP TABLE player_career_claims`); err != nil {
		t.Fatal(err)
	}
	if _, err := dbs.mmog.Exec(`CREATE TABLE player_career_claims(user_id TEXT, goal_id TEXT,
		claimed_stages INTEGER, updated_at TEXT, PRIMARY KEY(user_id, goal_id))`); err != nil {
		t.Fatal(err)
	}
	u := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	apply := func(stages int, ts string) {
		t.Helper()
		err := applyBundle(dbs, &bundle{UserID: u, Tables: map[string][]map[string]any{
			"mmog.player_career_claims": {{"user_id": u, "goal_id": "g1",
				"claimed_stages": stages, "updated_at": ts}},
		}})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
	stages := func() (int, string) {
		t.Helper()
		var n int
		var ts string
		if err := dbs.mmog.QueryRow(`SELECT claimed_stages,updated_at FROM player_career_claims
			WHERE user_id=? AND goal_id='g1'`, u).Scan(&n, &ts); err != nil {
			t.Fatalf("read: %v", err)
		}
		return n, ts
	}
	apply(2, "2026-09-29T11:00:00Z")
	apply(10, "2026-09-29T12:00:00Z")
	if n, ts := stages(); n != 10 || ts != "2026-09-29T12:00:00Z" {
		t.Fatalf("got (%d,%s), want (10, newer ts)", n, ts)
	}
	// A stale lower report must not revert the count.
	apply(3, "2026-09-29T10:00:00Z")
	if n, _ := stages(); n != 10 {
		t.Fatalf("stages = %d, want 10 (max, not last)", n)
	}
}

func TestKeyReceiveFirstWriteWins(t *testing.T) {
	dir := t.TempDir()
	a := &agent{cfg: config{secretFile: filepath.Join(dir, "sync.env")}, log: testLogger(t)}
	post := func(tlsOn bool, body string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/sync/key", strings.NewReader(body))
		if tlsOn {
			req.TLS = &tls.ConnectionState{}
		}
		a.handleKeyReceive(rec, req)
		return rec.Code
	}
	good := `{"key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	// Plaintext is refused even with a good body.
	if code := post(false, good); code != http.StatusForbidden {
		t.Errorf("plaintext: got %d, want 403", code)
	}
	// First write over https sticks.
	if code := post(true, good); code != http.StatusOK {
		t.Fatalf("first write: got %d, want 200", code)
	}
	if loadSecret(a.cfg) == "" {
		t.Fatal("secret file was not written")
	}
	// Second write is refused (first-write-wins, even over https).
	if code := post(true, good); code != http.StatusConflict {
		t.Errorf("second write: got %d, want 409", code)
	}
	// Garbage is refused.
	if code := post(true, `{"key":"x"}`); code != http.StatusConflict {
		t.Errorf("garbage after stored: got %d, want 409", code)
	}
}

func testLogger(t *testing.T) *logrus.Logger {
	t.Helper()
	log := logrus.New()
	log.SetOutput(io.Discard)
	return log
}

func TestApplyIdentitySkipsDuplicateName(t *testing.T) {
	dbs := testDBs(t)
	// Production shape (the shared helper builds UNIQUE-less tables):
	// username and email are UNIQUE in auth, which is what makes the
	// pulled duplicate collide instead of duplicating.
	if _, err := dbs.auth.Exec(`DROP TABLE users`); err != nil {
		t.Fatal(err)
	}
	if _, err := dbs.auth.Exec(`CREATE TABLE users(id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE,
		email TEXT NOT NULL UNIQUE, password_hash TEXT, created_at TEXT, banned_at TEXT, steam_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	// Local account owns the address; a pulled account with a different id
	// but the same email (registered twice in the sync window) must not
	// abort the apply — it is skipped, the local row wins by staying.
	if _, err := dbs.auth.Exec(`INSERT INTO users(id,username,email,password_hash,created_at)
		VALUES('11111111-1111-1111-1111-111111111111','alice','a@x.org','local','2026-09-29T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	pulled := []map[string]any{
		{"user_id": "22222222222222222222222222222222", "username": "alice2",
			"email": "a@x.org", "password_hash": "remote", "created_at": "2026-09-29T11:00:00Z"},
		{"user_id": "33333333333333333333333333333333", "username": "bob",
			"email": "b@x.org", "password_hash": "remote", "created_at": "2026-09-29T11:00:00Z"},
	}
	if err := applyIdentity(dbs, pulled, nil); err != nil {
		t.Fatalf("apply must not fail on a duplicate: %v", err)
	}
	var kept string
	if err := dbs.auth.QueryRow(`SELECT password_hash FROM users
		WHERE id='11111111-1111-1111-1111-111111111111'`).Scan(&kept); err != nil || kept != "local" {
		t.Fatalf("local row changed (hash=%q, err=%v)", kept, err)
	}
	var n int
	if err := dbs.auth.QueryRow(`SELECT COUNT(*) FROM users WHERE id='22222222-2222-2222-2222-222222222222'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("duplicate id applied (n=%d, err=%v)", n, err)
	}
	if err := dbs.auth.QueryRow(`SELECT COUNT(*) FROM users WHERE id='33333333-3333-3333-3333-333333333333'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("clean account missing (n=%d, err=%v)", n, err)
	}
}

func TestHandleSyncNow(t *testing.T) {
	uid := "ffffffffffffffffffffffffffffffff"
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/sync/push":
			_, _ = w.Write([]byte(`{"status":"ok","accepted":0,"skipped":0}`))
		case "/sync/pull":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"users": []any{}, "bans": []any{},
				"snapshots": []any{map[string]any{
					"user_id": uid, "updated_at": "2026-09-29T11:00:00Z",
					"tables": map[string]any{},
				}},
				"now": "2026-09-29T13:00:00Z",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer master.Close()
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "sync-state.json")
	// The snapshot (11:00) is older than our last push (12:00): a normal
	// run skips it, a forced run applies it.
	prestate := `{"last_pull":"","pushed_at":"","users":{"` + uid + `":"2026-09-29T12:00:00Z"}}`
	if err := os.WriteFile(stateFile, []byte(prestate), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &agent{
		cfg: config{masterURL: master.URL, cluster: "trig", secret: "s", stateFile: stateFile},
		log: testLogger(t), dbs: testDBs(t), http: master.Client(),
	}
	call := func(tlsOn bool, body string) (int, map[string]any) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/sync/now", strings.NewReader(body))
		if tlsOn {
			req.TLS = &tls.ConnectionState{}
		}
		a.handleSyncNow(rec, req)
		var doc map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &doc)
		return rec.Code, doc
	}
	if code, _ := call(false, `{}`); code != http.StatusForbidden {
		t.Fatalf("plaintext: got %d, want 403", code)
	}
	if code, _ := call(true, `{"force":`); code != http.StatusBadRequest {
		t.Fatalf("garbage body: got %d, want 400", code)
	}
	code, doc := call(true, `{}`)
	if code != http.StatusOK {
		t.Fatalf("trigger: got %d (%v)", code, doc)
	}
	if doc["applied"] != float64(0) {
		t.Fatalf("normal run must skip the older snapshot, got %v", doc)
	}
	// Immediate rerun is throttled.
	if code, _ := call(true, `{}`); code != http.StatusTooManyRequests {
		t.Fatalf("rerun: got %d, want 429", code)
	}
	// Forced rerun (throttle reset) applies the older snapshot.
	a.mu.Lock()
	a.lastTrigger = time.Time{}
	a.mu.Unlock()
	code, doc = call(true, `{"force":true}`)
	if code != http.StatusOK || doc["applied"] != float64(1) || doc["forced"] != true {
		t.Fatalf("forced run: got %d (%v), want applied=1 forced=true", code, doc)
	}
}
