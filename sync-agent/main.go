package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"
)

// Sync agent: roams accounts across clusters. Every interval it pushes every
// local user to the master directory and pulls everyone else's changes,
// last-write-wins per user. Small-community design: full pushes, no deltas
// on the way up (tens of users, kilobytes each). Deletes are not propagated
// as tombstones — a reset wipes local rows, and the next push carries the
// emptied state, which replaces everywhere.
//
// What it deliberately never touches: sessions (login tokens stay local),
// queue/matches/slots (live matchmaking), battle_results + match_history
// (per-cluster audit), chat (local chatter).
//
// SQLite note: separate handles from the services are safe (WAL +
// _busy_timeout); the agent is the only writer of nobody's hot path.

type config struct {
	masterURL    string
	cluster      string
	secretFile   string
	secret       string
	authDB       string
	mmogDB       string
	legacyDB     string
	noMasterFile string
	stateFile    string
	listenAddr   string
	tlsCert      string
	tlsKey       string
	interval     time.Duration
}

func loadConfig() config {
	interval := 60 * time.Second
	if v, err := time.ParseDuration(os.Getenv("SYNC_INTERVAL")); err == nil && v >= 10*time.Second {
		interval = v
	}
	return config{
		masterURL:  strings.TrimRight(strings.TrimSpace(os.Getenv("SYNC_MASTER_URL")), "/"),
		cluster:    strings.TrimSpace(os.Getenv("CLUSTER_NAME")),
		secretFile: getenv("SYNC_SECRET_FILE", "run/sync.env"),
		secret:     strings.TrimSpace(os.Getenv("SYNC_SECRET")),
		authDB:     getenv("AUTH_DB", "run/auth.db"),
		mmogDB:     getenv("MMOG_DB", "run/mmog.db"),
		legacyDB:   getenv("LEGACY_DB", "run/legacy.db"),
		// Same opt-out file the dn-dedicated registrar honors: present means
		// this cluster wants nothing to do with the directory — no listing
		// AND no account replication (neither push nor pull).
		noMasterFile: getenv("DN_NO_MASTER_SERVER_FILE", "run/dn-no-master-server.txt"),
		stateFile:    getenv("SYNC_STATE_FILE", "run/sync-state.json"),
		listenAddr:   getenv("SYNC_ADDR", ":8093"),
		tlsCert:      getenv("TLS_CERT", "certs/server.crt"),
		tlsKey:       getenv("TLS_KEY", "certs/server.key"),
		interval:     interval,
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadSecret reads run/sync.env (written by the /sync/key endpoint or by
// hand), with SYNC_SECRET env overriding for containers.
func loadSecret(cfg config) string {
	if cfg.secret != "" {
		return cfg.secret
	}
	raw, err := os.ReadFile(cfg.secretFile)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SYNC_SECRET=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "SYNC_SECRET="))
		}
	}
	return ""
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=10000", path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, db.Ping()
}

type agent struct {
	cfg config
	log *logrus.Logger
	dbs databases
	http *http.Client

	mu sync.Mutex
	// lastTrigger throttles POST /sync/now: one manual run per window,
	// so a stuck dashboard (or a curious stranger) cannot spin the loop.
	lastTrigger time.Time
}

// syncTriggerWindow is the minimum gap between two manual /sync/now runs.
const syncTriggerWindow = 10 * time.Second

type syncState struct {
	LastPull string            `json:"last_pull"`
	PushedAt string            `json:"pushed_at"`
	Users    map[string]string `json:"users"`
}

func loadState(path string) syncState {
	st := syncState{Users: map[string]string{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(raw, &st)
	if st.Users == nil {
		st.Users = map[string]string{}
	}
	return st
}

func (a *agent) saveState(st syncState) {
	raw, _ := json.MarshalIndent(st, "", "  ")
	_ = os.WriteFile(a.cfg.stateFile, raw, 0o600)
}

func main() {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})
	cfg := loadConfig()
	if cfg.masterURL == "" || cfg.cluster == "" {
		log.Fatal("SYNC_MASTER_URL and CLUSTER_NAME must be set (see run/secrets.env)")
	}
	secret := loadSecret(cfg)
	if secret == "" {
		log.Warn("no sync secret yet (run/sync.env): waiting for the directory operator — see the master dashboard secret button")
	}

	authDB, err := openDB(cfg.authDB)
	if err != nil {
		log.WithError(err).Fatal("open auth db")
	}
	defer func() { _ = authDB.Close() }()
	mmogDB, err := openDB(cfg.mmogDB)
	if err != nil {
		log.WithError(err).Fatal("open mmog db")
	}
	defer func() { _ = mmogDB.Close() }()
	legacyDB, err := openDB(cfg.legacyDB)
	if err != nil {
		log.WithError(err).Fatal("open legacy db")
	}
	defer func() { _ = legacyDB.Close() }()

	a := &agent{cfg: cfg, log: log,
		dbs:  databases{auth: authDB, mmog: mmogDB, legacy: legacyDB},
		http: &http.Client{Timeout: 30 * time.Second}}

	mux := http.NewServeMux()
	mux.HandleFunc("/sync/key", a.handleKeyReceive)
	mux.HandleFunc("/sync/now", a.handleSyncNow)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"sync-agent"}`))
	})
	srv := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.WithField("addr", cfg.listenAddr).Info("sync-agent key receiver starting (https only)")
		if err := srv.ListenAndServeTLS(cfg.tlsCert, cfg.tlsKey); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("listen")
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(cfg.interval)
	defer ticker.Stop()
	a.loop(loadState(cfg.stateFile), false)
	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Shutdown(shutdownCtx)
			cancel()
			log.Info("sync-agent stopped")
			return
		case <-ticker.C:
			a.loop(loadState(cfg.stateFile), false)
		}
	}
}

// handleKeyReceive stores the sync secret pushed by the directory operator.
// First write wins: once a secret exists only a hand edit (plus restart)
// replaces it, so a stray or replayed push can never swap it under us.
// HTTPS only: r.TLS is nil behind plaintext, and those requests are refused
// outright — the secret must not travel in cleartext, per operator order.
func (a *agent) handleKeyReceive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if r.TLS == nil {
		http.Error(w, `{"error":"https only"}`, http.StatusForbidden)
		return
	}
	if loadSecret(a.cfg) != "" {
		http.Error(w, `{"error":"a secret is already stored"}`, http.StatusConflict)
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if len(req.Key) < 16 || len(req.Key) > 256 {
		http.Error(w, `{"error":"bad secret"}`, http.StatusBadRequest)
		return
	}
	if dir := filepath.Dir(a.cfg.secretFile); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			http.Error(w, `{"error":"cannot store secret"}`, http.StatusInternalServerError)
			return
		}
	}
	if err := os.WriteFile(a.cfg.secretFile,
		[]byte("SYNC_SECRET="+req.Key+"\n"), 0o600); err != nil {
		http.Error(w, `{"error":"cannot store secret"}`, http.StatusInternalServerError)
		return
	}
	a.log.Info("sync secret stored via operator push")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"stored"}`))
}

// optedOut reports whether the operator filed the no-directory opt-out.
// Same file, same semantics as the dn-dedicated registrar: opted out means
// invisible in the browser AND excluded from account replication.
func (a *agent) optedOut() bool {
	if a.cfg.noMasterFile == "" {
		return false
	}
	_, err := os.Stat(a.cfg.noMasterFile)
	return err == nil
}

// inLiveMatch reports whether the user currently holds a slot in an active
// match on this cluster — the presence signal the launcher checks before
// letting the same account play somewhere else. Both id spellings are
// checked: slots carry the binary-protocol pid (undashed), other writers
// may use the dashed form.
func inLiveMatch(db *sql.DB, uid string) bool {
	var one int
	err := db.QueryRow(`SELECT 1 FROM match_slots s JOIN matches m ON m.id=s.match_id
		WHERE (s.user_id=? OR s.user_id=?) AND m.status='active' LIMIT 1`,
		uid, denormID(uid)).Scan(&one)
	return err == nil && one == 1
}

// handleSyncNow runs one push/pull cycle immediately (the directory
// dashboard's "sync now" button calls this on every cluster). HTTPS only,
// like key receive. No secret needed: a run is idempotent (push latest,
// pull latest) and carries no secrets either way — but it is throttled to
// one run per window, so neither a stuck dashboard nor a stranger hammering
// the public agent URL can spin the loop. {"force":true} also applies pulled
// snapshots older than local state (the rollout path); without it only
// strictly newer state applies.
func (a *agent) handleSyncNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if r.TLS == nil {
		http.Error(w, `{"error":"https only"}`, http.StatusForbidden)
		return
	}
	var req struct {
		Force bool `json:"force"`
	}
	if raw, err := io.ReadAll(io.LimitReader(r.Body, 4096)); err != nil {
		http.Error(w, `{"error":"cannot read body"}`, http.StatusBadRequest)
		return
	} else if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
	}
	a.mu.Lock()
	if wait := syncTriggerWindow - time.Since(a.lastTrigger); wait > 0 {
		a.mu.Unlock()
		http.Error(w, fmt.Sprintf(`{"error":"sync ran recently, retry in %d seconds"}`,
			int(wait.Seconds())+1), http.StatusTooManyRequests)
		return
	}
	a.lastTrigger = time.Now()
	a.mu.Unlock()
	pushed, applied := a.loop(loadState(a.cfg.stateFile), req.Force)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok", "pushed": pushed, "applied": applied, "forced": req.Force,
	})
}

// loop pushes every local user, then pulls remote changes. Errors are
// logged, never fatal: a down directory must not stop the game.
// force applies every pulled snapshot even when it is older than local
// state (manual rollout from the main cluster); without it only strictly
// newer state applies, so local unsynced earnings are never clobbered.
func (a *agent) loop(st syncState, force bool) (pushed, applied int) {
	if a.optedOut() {
		a.log.Info("sync skipped: opted out of the directory (no replication)")
		return 0, 0
	}
	secret := loadSecret(a.cfg)
	if secret == "" {
		a.log.Warn("sync skipped: no secret (waiting for operator)")
		return 0, 0
	}
	ids, err := userIDs(a.dbs.auth)
	if err != nil {
		a.log.WithError(err).Warn("sync: list users")
		return 0, 0
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var users []any
	var snapshots []any
	for _, id := range ids {
		uid := normID(id)
		if uid == "" {
			continue
		}
		b, err := buildBundle(a.dbs, uid, time.Now().UTC())
		if err != nil {
			a.log.WithError(err).WithField("user", uid).Warn("sync: build bundle")
			continue
		}
		users = append(users, bundleIdentity(b))
		credits, rank, ships := b.headline()
		snapshots = append(snapshots, map[string]any{
			"user_id": uid, "updated_at": b.UpdatedAt,
			"credits": credits, "rank": rank, "ships": ships,
			"in_match": inLiveMatch(a.dbs.mmog, uid), "tables": b.Tables,
		})
		st.Users[uid] = now
	}
	if err := a.post("/sync/push", map[string]any{"users": users, "snapshots": snapshots}, secret, &map[string]any{}); err != nil {
		a.log.WithError(err).Warn("sync: push failed")
	} else {
		pushed = len(users)
		a.log.WithFields(logrus.Fields{"users": len(users)}).Info("sync: pushed")
	}
	var pulled struct {
		Users     []map[string]any `json:"users"`
		Bans      []map[string]any `json:"bans"`
		Snapshots []pullSnapshot   `json:"snapshots"`
		Now       string           `json:"now"`
	}
	if err := a.get("/sync/pull?since="+urlQueryEscape(st.LastPull), secret, &pulled); err != nil {
		a.log.WithError(err).Warn("sync: pull failed")
		a.saveState(st)
		return pushed, 0
	}
	for _, s := range pulled.Snapshots {
		b := &bundle{UserID: normID(s.UserID), UpdatedAt: s.UpdatedAt, Tables: s.Tables}
		// Never clobber local unsynced earnings with stale remote state:
		// apply only what is strictly newer than what we last pushed or
		// applied for this user. (Without this, earning locally between
		// push and pull would be overwritten by the older master copy.)
		// force (manual rollout) skips this guard: the operator pointed at
		// the main cluster and means it.
		if !force && !syncTimeAfter(b.UpdatedAt, st.Users[b.UserID]) {
			continue
		}
		if err := applyBundle(a.dbs, b); err != nil {
			a.log.WithError(err).WithField("user", b.UserID).Warn("sync: apply snapshot")
			continue
		}
		applied++
		st.Users[b.UserID] = now
	}
	// Identity and bans apply directly (they ride no snapshot): upsert users,
	// replace each seen user's bans (an unban propagates as an empty set via
	// the push side, and as presence here — a user listed without bans keeps
	// none).
	if err := applyIdentity(a.dbs, pulled.Users, pulled.Bans); err != nil {
		a.log.WithError(err).Warn("sync: apply identity")
	}
	if pulled.Now != "" {
		st.LastPull = pulled.Now
	}
	a.saveState(st)
	a.log.WithFields(logrus.Fields{"applied": applied}).Info("sync: pulled")
	return pushed, applied
}

// syncTimeAfter reports whether RFC3339 a is strictly after b. Unparseable
// is never after: garbage timestamps lose every comparison instead of
// overwriting real data.
func syncTimeAfter(a, b string) bool {
	ta, errA := parseSyncTime(a)
	tb, errB := parseSyncTime(b)
	if errA != nil || ta.IsZero() {
		return false
	}
	if errB != nil || tb.IsZero() {
		return true
	}
	return ta.After(tb)
}

func parseSyncTime(raw string) (time.Time, error) {
	return time.Parse(time.RFC3339, raw)
}

type pullSnapshot struct {
	UserID    string                       `json:"user_id"`
	UpdatedAt string                       `json:"updated_at"`
	Tables    map[string][]map[string]any  `json:"tables"`
}

// bundleIdentity extracts the auth.users row for the push identity list,
// keyed as user_id the way the master expects it.
func bundleIdentity(b *bundle) map[string]any {
	for _, m := range b.Tables["auth.users"] {
		out := map[string]any{"user_id": b.UserID}
		for k, v := range m {
			if k == "id" {
				continue
			}
			out[k] = v
		}
		out["updated_at"] = b.UpdatedAt
		return out
	}
	return map[string]any{"user_id": b.UserID, "updated_at": b.UpdatedAt}
}

func (a *agent) authed(path, secret string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, a.cfg.masterURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Sync-Key", secret)
	return req, nil
}

func (a *agent) post(path string, payload any, secret string, out *map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := a.authed(path, secret, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, err := io.ReadAll(io.LimitReader(resp.Body, 1<<24))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("master answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return json.Unmarshal(rb, out)
}

func (a *agent) get(path, secret string, out any) error {
	req, err := http.NewRequest(http.MethodGet, a.cfg.masterURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Sync-Key", secret)
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, err := io.ReadAll(io.LimitReader(resp.Body, 1<<24))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("master answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return json.Unmarshal(rb, out)
}

func urlQueryEscape(s string) string {
	if s == "" {
		return "1970-01-01T00:00:00Z"
	}
	out := ""
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~':
			out += string(c)
		default:
			out += "%" + strings.ToUpper(fmt.Sprintf("%02x", c))
		}
	}
	return out
}
