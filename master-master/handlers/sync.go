package handlers

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// Stage 3 account roaming: clusters push per-user snapshots here and pull
// everyone else's. Merge rule is last-write-wins per user on updated_at
// (ties break toward the lexicographically greater source cluster, so two
// hosts decide identically). Clock skew between hosts directly corrupts this,
// so clusters are expected to run NTP — stated plainly because silent
// skew-induced reverts are the classic failure of timestamp merging.
//
// Timestamps are compared parsed (RFC3339), never as strings: mixed offsets
// do not sort lexicographically.

// syncTime parses an RFC3339 timestamp; unparseable means zero time, which
// loses every comparison (safe default: never overwrite real data with it).
func syncTime(raw string) time.Time {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// emailPattern is a deliberately loose contact check: deliverability is the
// human's problem (they mail the secret by hand), this only rejects obvious
// non-addresses.
var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// secretHash identifies a cluster by its sync secret without storing it.
func secretHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// syncAuth resolves the calling cluster from X-Sync-Key. Unknown or revoked
// keys get ok=false; callers answer 403.
func (h *Handler) syncAuth(r *http.Request) (id, name string, ok bool) {
	provided := r.Header.Get("X-Sync-Key")
	if provided == "" {
		return "", "", false
	}
	want := secretHash(provided)
	var stored string
	if err := h.DB.QueryRow(`SELECT id,name,secret_hash FROM clusters WHERE secret_hash=? AND secret_hash!=''`,
		want).Scan(&id, &name, &stored); err != nil {
		return "", "", false
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(want)) != 1 {
		return "", "", false
	}
	return id, name, true
}

// syncLog records one sync communication (both directions, success and
// failure): the audit trail the dashboard shows.
func (h *Handler) syncLog(clusterID, direction, endpoint string, users int, status, detail string) {
	_, _ = h.DB.Exec(`INSERT INTO sync_log(cluster_id,direction,endpoint,users,status,detail)
		VALUES(?,?,?,?,?,?)`, clusterID, direction, endpoint, users, status, detail)
}

// syncUser is one account row as clusters send and receive it.
type syncUser struct {
	UserID       string `json:"user_id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	PasswordHash string `json:"password_hash"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// syncSnapshot is one user's full state bundle plus headline columns.
// InMatch is this user's live state on the sending cluster right now (active
// match slot); the master records it as presence, separately from the
// last-write-wins snapshot, because freshness — not recency — is the point.
type syncSnapshot struct {
	UserID        string         `json:"user_id"`
	UpdatedAt     string         `json:"updated_at"`
	Credits       int            `json:"credits"`
	Rank          int            `json:"rank"`
	Ships         int            `json:"ships"`
	InMatch       bool           `json:"in_match"`
	Tables        map[string]any `json:"tables"`
}

// presenceWindow is how long a cluster's in-match report counts as live. It
// is twice the default push interval, so one missed beat does not instantly
// clear a player — but a dead cluster stops blocking logins after two minutes.
const presenceWindow = 120 * time.Second

// SyncPush handles POST /sync/push — a cluster uploads changed users and
// snapshots. Everything is last-write-wins against what is stored.
func (h *Handler) SyncPush(w http.ResponseWriter, r *http.Request) {
	clusterID, clusterName, ok := h.syncAuth(r)
	if !ok {
		h.syncLog("", "in", "push", 0, "denied", "unknown or revoked key")
		writeError(w, http.StatusForbidden, "unknown or revoked sync key")
		return
	}
	var req struct {
		Users     []syncUser     `json:"users"`
		Snapshots []syncSnapshot `json:"snapshots"`
		Bans      []syncBan     `json:"bans"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<24)).Decode(&req); err != nil {
		h.syncLog(clusterID, "in", "push", 0, "error", "invalid body")
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	accepted, skipped := 0, 0
	var pushedUsers []string
	for _, u := range req.Users {
		if strings.TrimSpace(u.UserID) == "" || strings.TrimSpace(u.Username) == "" {
			skipped++
			continue
		}
		var stored string
		err := h.DB.QueryRow(`SELECT updated_at FROM sync_users WHERE user_id=?`, u.UserID).Scan(&stored)
		if err == nil && !syncNewer(u.UpdatedAt, clusterName, stored, "") {
			skipped++
			continue
		}
		if _, err := h.DB.Exec(`INSERT INTO sync_users(user_id,username,email,password_hash,created_at,updated_at,source_cluster)
			VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET username=excluded.username,
			email=excluded.email, password_hash=excluded.password_hash, updated_at=excluded.updated_at,
			source_cluster=excluded.source_cluster`,
			u.UserID, u.Username, u.Email, u.PasswordHash, orNow(u.CreatedAt), orNow(u.UpdatedAt), clusterName); err != nil {
			h.Log.WithError(err).Warn("sync push: upsert user")
			skipped++
			continue
		}
		pushedUsers = append(pushedUsers, u.UserID)
		accepted++
	}
	// Bans replace per pushed user (not upsert-only): an unban deletes the
	// local row, and the empty set must propagate too, or a ban would be
	// un-liftable from anywhere but the master.
	for _, uid := range pushedUsers {
		if _, err := h.DB.Exec(`DELETE FROM sync_bans WHERE user_id=?`, uid); err != nil {
			h.Log.WithError(err).Warn("sync push: clear bans")
		}
	}
	for _, b := range req.Bans {
		if strings.TrimSpace(b.ID) == "" || strings.TrimSpace(b.UserID) == "" {
			skipped++
			continue
		}
		if _, err := h.DB.Exec(`INSERT INTO sync_bans(id,user_id,reason,banned_by,expires_at,created_at)
			VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET reason=excluded.reason,
			banned_by=excluded.banned_by, expires_at=excluded.expires_at`,
			b.ID, b.UserID, b.Reason, b.BannedBy, nullIfEmpty(b.ExpiresAt), orNow(b.CreatedAt)); err != nil {
			h.Log.WithError(err).Warn("sync push: upsert ban")
			skipped++
			continue
		}
		accepted++
	}
	for _, s := range req.Snapshots {
		if strings.TrimSpace(s.UserID) == "" {
			skipped++
			continue
		}
		// Presence is the cluster's own live state, keyed per (user,
		// cluster): no merge conflict, always refresh — even when the
		// snapshot itself loses last-write-wins below. Stale rows expire by
		// the presenceWindow filter in SyncPresence, so a cluster that stops
		// pushing stops blocking that account within two minutes.
		inMatch := 0
		if s.InMatch {
			inMatch = 1
		}
		if _, err := h.DB.Exec(`INSERT INTO sync_presence(user_id,cluster_id,in_match,updated_at)
			VALUES(?,?,?,?) ON CONFLICT(user_id,cluster_id) DO UPDATE SET in_match=excluded.in_match,
			updated_at=excluded.updated_at`,
			s.UserID, clusterID, inMatch, time.Now().UTC().Format(time.RFC3339)); err != nil {
			h.Log.WithError(err).Warn("sync push: upsert presence")
		}
		raw, err := json.Marshal(s.Tables)
		if err != nil {
			skipped++
			continue
		}
		var stored, storedSource string
		err = h.DB.QueryRow(`SELECT updated_at,source_cluster FROM sync_snapshots WHERE user_id=?`,
			s.UserID).Scan(&stored, &storedSource)
		if err == nil && !syncNewer(s.UpdatedAt, clusterName, stored, storedSource) {
			skipped++
			continue
		}
		if _, err := h.DB.Exec(`INSERT INTO sync_snapshots(user_id,updated_at,source_cluster,credits,rank,ships,data)
			VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET updated_at=excluded.updated_at,
			source_cluster=excluded.source_cluster, credits=excluded.credits, rank=excluded.rank,
			ships=excluded.ships, data=excluded.data`,
			s.UserID, orNow(s.UpdatedAt), clusterName, s.Credits, s.Rank, s.Ships, string(raw)); err != nil {
			h.Log.WithError(err).Warn("sync push: upsert snapshot")
			skipped++
			continue
		}
		accepted++
	}
	h.syncLog(clusterID, "in", "push", accepted, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "accepted": accepted, "skipped": skipped})
}

// syncBan is one ban row as clusters send it.
type syncBan struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Reason    string `json:"reason"`
	BannedBy  string `json:"banned_by"`
	ExpiresAt string `json:"expires_at"`
	CreatedAt string `json:"created_at"`
}

// syncNewer reports whether an incoming row (time, source) wins over a stored
// one. Newer timestamp wins; ties break toward the lexicographically greater
// source so every host decides the same way.
func syncNewer(inTime, inSource, storedTime, storedSource string) bool {
	in, stored := syncTime(inTime), syncTime(storedTime)
	if in.After(stored) {
		return true
	}
	if in.Equal(stored) && inSource > storedSource {
		return true
	}
	return false
}

func orNow(raw string) string {
	if syncTime(raw).IsZero() {
		return time.Now().UTC().Format(time.RFC3339)
	}
	return raw
}

func nullIfEmpty(raw string) any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return raw
}

// SyncPull handles GET /sync/pull?since= — everything changed after since
// (users, bans, snapshots), so the cluster can apply it locally.
func (h *Handler) SyncPull(w http.ResponseWriter, r *http.Request) {
	clusterID, _, ok := h.syncAuth(r)
	if !ok {
		h.syncLog("", "in", "pull", 0, "denied", "unknown or revoked key")
		writeError(w, http.StatusForbidden, "unknown or revoked sync key")
		return
	}
	since := r.URL.Query().Get("since")
	sinceTime := syncTime(since)
	users := []syncUser{}
	rows, err := h.DB.Query(`SELECT user_id,username,email,password_hash,created_at,updated_at
		FROM sync_users`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	for rows.Next() {
		var u syncUser
		if err := rows.Scan(&u.UserID, &u.Username, &u.Email, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt); err != nil {
			_ = rows.Close()
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		if !sinceTime.IsZero() && !syncTime(u.UpdatedAt).After(sinceTime) {
			continue
		}
		users = append(users, u)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	bans := []syncBan{}
	brows, err := h.DB.Query(`SELECT id,user_id,reason,banned_by,expires_at,created_at FROM sync_bans`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	for brows.Next() {
		var b syncBan
		var expires any
		if err := brows.Scan(&b.ID, &b.UserID, &b.Reason, &b.BannedBy, &expires, &b.CreatedAt); err != nil {
			_ = brows.Close()
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		if s, ok := expires.(string); ok {
			b.ExpiresAt = s
		}
		bans = append(bans, b)
	}
	_ = brows.Close()
	snapshots := []map[string]any{}
	srows, err := h.DB.Query(`SELECT user_id,updated_at,source_cluster,credits,rank,ships,data
		FROM sync_snapshots`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	for srows.Next() {
		var userID, updatedAt, source string
		var credits, rank, ships int
		var data string
		if err := srows.Scan(&userID, &updatedAt, &source, &credits, &rank, &ships, &data); err != nil {
			_ = srows.Close()
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		if !sinceTime.IsZero() && !syncTime(updatedAt).After(sinceTime) {
			continue
		}
		var tables map[string]any
		if err := json.Unmarshal([]byte(data), &tables); err != nil {
			tables = map[string]any{}
		}
		snapshots = append(snapshots, map[string]any{
			"user_id": userID, "updated_at": updatedAt, "source_cluster": source,
			"credits": credits, "rank": rank, "ships": ships, "tables": tables,
		})
	}
	_ = srows.Close()
	h.syncLog(clusterID, "out", "pull", len(users)+len(snapshots), "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{
		"users": users, "bans": bans, "snapshots": snapshots,
		"now":   time.Now().UTC().Format(time.RFC3339),
	})
}

// SyncPresence handles GET /presence/{user_id}?except= — the launcher's
// "is this account already in a match somewhere else?" check. It answers
// in_match with the cluster name when any FRESH report says so.
// ?except= excludes the cluster the player is logging into (directory id or
// cluster name — manual server entries have no id, so the name matches too).
// Public: user ids are unguessable 32-hex and the answer is only online
// status, the same tradeoff every game lobby makes.
func (h *Handler) SyncPresence(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(mux.Vars(r)["user_id"])
	if userID == "" {
		writeError(w, http.StatusBadRequest, "user id required")
		return
	}
	except := strings.TrimSpace(r.URL.Query().Get("except"))
	cutoff := time.Now().UTC().Add(-presenceWindow).Format(time.RFC3339)
	rows, err := h.DB.Query(`SELECT p.cluster_id,COALESCE(c.name,'') FROM sync_presence p
		LEFT JOIN clusters c ON c.id=p.cluster_id
		WHERE p.user_id=? AND p.in_match=1 AND p.updated_at>?`, userID, cutoff)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer func() { _ = rows.Close() }()
	inMatch, cluster := false, ""
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}
		if except != "" && (id == except || name == except) {
			continue
		}
		inMatch = true
		if name != "" {
			cluster = name
		} else if cluster == "" {
			cluster = id
		}
	}
	_ = rows.Close()
	writeJSON(w, http.StatusOK, map[string]any{"in_match": inMatch, "cluster": cluster})
}

// SyncRegisterCheck handles GET /register-check?username=&email= — the
// launcher's "is this name or address already taken anywhere?" pre-check
// before creating an account on one cluster. It answers {taken} from the
// account mirror (exact match, same semantics as each cluster's own 409).
// Public like presence: registration itself is already a taken-oracle
// (409 vs 201 per cluster), so this adds no new capability, only one query
// instead of one per cluster. Unknown (empty mirror) means not taken — the
// cluster's own registration stays authoritative and keeps its 409.
func (h *Handler) SyncRegisterCheck(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	if username == "" && email == "" {
		writeError(w, http.StatusBadRequest, "username or email required")
		return
	}
	// Only the given fields participate: an empty parameter must never
	// match (no stored row has an empty name or address, but do not rely
	// on that).
	query, args := `SELECT 1 FROM sync_users WHERE username=? LIMIT 1`, []any{username}
	if username == "" {
		query, args = `SELECT 1 FROM sync_users WHERE email=? LIMIT 1`, []any{email}
	} else if email != "" {
		query, args = `SELECT 1 FROM sync_users WHERE username=? OR email=? LIMIT 1`, []any{username, email}
	}
	var one int
	err := h.DB.QueryRow(query, args...).Scan(&one)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"taken": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"taken": one == 1})
}

// getSetting reads one operator setting ("": unset). Currently only
// main_cluster_id (the rollout source) lives here.
func (h *Handler) getSetting(key string) string {
	var v string
	if err := h.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

func (h *Handler) setSetting(key, value string) error {
	_, err := h.DB.Exec(`INSERT INTO settings(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// agentHTTP is the client for talking to cluster agents (secret send,
// manual sync triggers). It trusts the system roots plus the operator CA
// (MASTER_TRUST_CA, default certs/ca.crt): self-hosted clusters present
// self-signed agent certificates from that same CA, and without it both the
// secret send and the manual sync fail closed on numeric IPs. A missing CA
// file just means system roots, as before.
func agentHTTP() *http.Client {
	pool, _ := x509.SystemCertPool()
	if pool == nil {
		pool = x509.NewCertPool()
	}
	caPath := strings.TrimSpace(os.Getenv("MASTER_TRUST_CA"))
	if caPath == "" {
		caPath = "certs/ca.crt"
	}
	if pem, err := os.ReadFile(caPath); err == nil {
		pool.AppendCertsFromPEM(pem)
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}
}

// triggerAgent runs one push/pull cycle on a cluster agent right now
// (POST {agent_url}/sync/now). force also applies pulled snapshots older
// than local state — the rollout path. http and https are both accepted:
// the trigger carries no secrets either way (unlike the secret send, which
// stays https-only), and insisting on https would lock out every numeric-IP
// cluster whose agent certificate no public CA signs.
func triggerAgent(agentURL string, force bool) (pushed, applied int, err error) {
	target := strings.TrimRight(strings.TrimSpace(agentURL), "/") + "/sync/now"
	parsed, perr := url.Parse(target)
	if perr != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return 0, 0, fmt.Errorf("not an http(s) agent URL")
	}
	body, _ := json.Marshal(map[string]bool{"force": force})
	resp, derr := agentHTTP().Post(target, "application/json", bytes.NewReader(body))
	if derr != nil {
		return 0, 0, derr
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("agent answered HTTP %d: %s", resp.StatusCode,
			strings.TrimSpace(string(raw)))
	}
	var doc struct {
		Pushed  int `json:"pushed"`
		Applied int `json:"applied"`
	}
	if jerr := json.Unmarshal(raw, &doc); jerr != nil {
		return 0, 0, fmt.Errorf("parse agent response: %w", jerr)
	}
	return doc.Pushed, doc.Applied, nil
}

// syncClusterRow is one cluster with somewhere to trigger.
type syncClusterRow struct {
	ID       string
	Name     string
	AgentURL string
}

func (h *Handler) triggerableClusters(exceptID string) []syncClusterRow {
	rows, err := h.DB.Query(`SELECT id,name,agent_url FROM clusters ORDER BY name`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []syncClusterRow
	for rows.Next() {
		var c syncClusterRow
		if err := rows.Scan(&c.ID, &c.Name, &c.AgentURL); err != nil {
			continue
		}
		if c.ID == exceptID || strings.TrimSpace(c.AgentURL) == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// triggerResult is one cluster's manual-sync outcome for the dashboard.
type triggerResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Pushed  int    `json:"pushed"`
	Applied int    `json:"applied"`
	Detail  string `json:"detail"`
}

// AdminSyncSettings handles GET/POST /admin/api/sync-settings — the manual
// sync configuration. The only setting is the main cluster: the rollout
// source whose state "roll out" copies everywhere. POST {"main_cluster_id":
// "<id>"|"")}: empty clears it; anything else must be a known cluster.
func (h *Handler) AdminSyncSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"main_cluster_id": h.getSetting("main_cluster_id")})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		MainClusterID string `json:"main_cluster_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	req.MainClusterID = strings.TrimSpace(req.MainClusterID)
	if req.MainClusterID != "" {
		var one int
		if err := h.DB.QueryRow(`SELECT 1 FROM clusters WHERE id=?`, req.MainClusterID).Scan(&one); err != nil {
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
	}
	if err := h.setSetting("main_cluster_id", req.MainClusterID); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	h.syncLog(req.MainClusterID, "admin", "sync-settings", 0, "ok", "main cluster set")
	writeJSON(w, http.StatusOK, map[string]any{"main_cluster_id": req.MainClusterID})
}

// AdminSyncNow handles POST /admin/api/sync-now — the dashboard's "sync
// everything now" button. It triggers a normal cycle (no force) on every
// cluster with an agent URL and reports per-cluster results. Slow or dead
// clusters fail individually; the rest still run.
func (h *Handler) AdminSyncNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	results := []triggerResult{}
	for _, c := range h.triggerableClusters("") {
		pushed, applied, err := triggerAgent(c.AgentURL, false)
		res := triggerResult{ID: c.ID, Name: c.Name, Pushed: pushed, Applied: applied}
		if err != nil {
			res.Detail = err.Error()
			h.syncLog(c.ID, "admin", "sync-now", 0, "error", err.Error())
		} else {
			res.OK = true
			h.syncLog(c.ID, "admin", "sync-now", pushed+applied, "ok", "")
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "count": len(results)})
}

// AdminRollout handles POST /admin/api/rollout — "copy the main cluster
// everywhere". Step 1 triggers a normal cycle on the main cluster (it pushes
// its fresh state to the mirror). Step 2 triggers a FORCED cycle on every
// other cluster: they apply everything the mirror holds, even snapshots
// older than their local state, then push — so afterwards every account the
// main cluster has reads identically everywhere. Accounts that exist only
// elsewhere are kept, never deleted: this is a converge, not a wipe.
// If step 1 fails, step 2 never runs (nothing is half-rolled-out).
func (h *Handler) AdminRollout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	mainID := h.getSetting("main_cluster_id")
	if mainID == "" {
		writeError(w, http.StatusBadRequest, "no main cluster selected")
		return
	}
	var main syncClusterRow
	if err := h.DB.QueryRow(`SELECT id,name,agent_url FROM clusters WHERE id=?`,
		mainID).Scan(&main.ID, &main.Name, &main.AgentURL); err != nil {
		writeError(w, http.StatusNotFound, "main cluster not found")
		return
	}
	if strings.TrimSpace(main.AgentURL) == "" {
		writeError(w, http.StatusBadRequest, "main cluster has no agent URL")
		return
	}
	mainPushed, mainApplied, mainErr := triggerAgent(main.AgentURL, false)
	mainRes := triggerResult{ID: main.ID, Name: main.Name, Pushed: mainPushed, Applied: mainApplied}
	if mainErr != nil {
		mainRes.Detail = mainErr.Error()
		h.syncLog(main.ID, "admin", "rollout", 0, "error", "main push failed: "+mainErr.Error())
		writeJSON(w, http.StatusBadGateway,
			map[string]any{"error": "main cluster did not sync — nobody else was touched", "main": mainRes})
		return
	}
	mainRes.OK = true
	h.syncLog(main.ID, "admin", "rollout", mainPushed+mainApplied, "ok", "main pushed")
	results := []triggerResult{}
	for _, c := range h.triggerableClusters(main.ID) {
		pushed, applied, err := triggerAgent(c.AgentURL, true)
		res := triggerResult{ID: c.ID, Name: c.Name, Pushed: pushed, Applied: applied}
		if err != nil {
			res.Detail = err.Error()
			h.syncLog(c.ID, "admin", "rollout", 0, "error", err.Error())
		} else {
			res.OK = true
			h.syncLog(c.ID, "admin", "rollout", pushed+applied, "ok", "forced from main")
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"main": mainRes, "results": results, "count": len(results)})
}

// AdminSyncLog handles GET /admin/api/synclog?limit= — the audit trail of
// every sync communication, newest first.
func (h *Handler) AdminSyncLog(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var n int
		if _, err := fmt.Sscanf(raw, "%d", &n); err == nil {
			limit = n
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := h.DB.Query(`SELECT l.time,l.cluster_id,COALESCE(c.name,''),l.direction,l.endpoint,
		l.users,l.status,l.detail FROM sync_log l LEFT JOIN clusters c ON c.id=l.cluster_id
		ORDER BY l.id DESC LIMIT ?`, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	type entry struct {
		Time      string `json:"time"`
		ClusterID string `json:"cluster_id"`
		Cluster   string `json:"cluster"`
		Direction string `json:"direction"`
		Endpoint  string `json:"endpoint"`
		Users     int    `json:"users"`
		Status    string `json:"status"`
		Detail    string `json:"detail"`
	}
	out := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.Time, &e.ClusterID, &e.Cluster, &e.Direction, &e.Endpoint,
			&e.Users, &e.Status, &e.Detail); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "count": len(out)})
}

// AdminSyncUsers handles GET /admin/api/syncusers?q=&limit= — every mirrored
// account with headline stats. The dashboard's user view.
func (h *Handler) AdminSyncUsers(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var n int
		if _, err := fmt.Sscanf(raw, "%d", &n); err == nil {
			limit = n
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	q := "%" + strings.TrimSpace(r.URL.Query().Get("q")) + "%"
	rows, err := h.DB.Query(`SELECT u.user_id,u.username,u.email,u.updated_at,u.source_cluster,
		COALESCE(s.credits,0),COALESCE(s.rank,1),COALESCE(s.ships,0),COALESCE(s.updated_at,''),
		EXISTS(SELECT 1 FROM sync_bans b WHERE b.user_id=u.user_id AND (b.expires_at IS NULL OR datetime(b.expires_at) > datetime('now')))
		FROM sync_users u LEFT JOIN sync_snapshots s ON s.user_id=u.user_id
		WHERE u.username LIKE ? OR u.email LIKE ? OR u.user_id LIKE ?
		ORDER BY u.updated_at DESC LIMIT ?`, q, q, q, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer func() {
		_ = rows.Close()
	}()
	type user struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
		Email    string `json:"email"`
		Updated  string `json:"updated_at"`
		Source   string `json:"source_cluster"`
		Credits  int    `json:"credits"`
		Rank     int    `json:"rank"`
		Ships    int    `json:"ships"`
		SyncedAt string `json:"synced_at"`
		Banned   bool   `json:"banned"`
	}
	out := []user{}
	for rows.Next() {
		var u user
		var banned int
		if err := rows.Scan(&u.UserID, &u.Username, &u.Email, &u.Updated, &u.Source,
			&u.Credits, &u.Rank, &u.Ships, &u.SyncedAt, &banned); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		u.Banned = banned != 0
		out = append(out, u)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out, "count": len(out)})
}

// AdminSecret handles POST /admin/api/clusters/{id}/secret — the three key
// buttons in one endpoint:
//   {"action":"generate"} → new secret, hash stored, plaintext returned ONCE
//     (copy it into the mail to the cluster owner; it is never stored).
//   {"action":"revoke"}   → hash cleared; the cluster's sync calls fail from
//     now on until a new secret is generated.
//   {"action":"send"}     → generate a FRESH secret (rotating any previous
//     one, whose plaintext is unknowable anyway) AND push it to the
//     cluster's agent URL right away (https only, never plaintext); returns
//     the plaintext once for the mail backup, plus whether the push landed.
func (h *Handler) AdminSecret(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	var name, agentURL string
	if err := h.DB.QueryRow(`SELECT name,agent_url FROM clusters WHERE id=?`,
		id).Scan(&name, &agentURL); err != nil {
		writeError(w, http.StatusNotFound, "cluster not found")
		return
	}
	switch req.Action {
	case "revoke":
		if _, err := h.DB.Exec(`UPDATE clusters SET secret_hash='' WHERE id=?`, id); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		h.Log.WithField("cluster_id", id).Warn("operator revoked a cluster sync secret")
		h.syncLog(id, "admin", "secret-revoke", 0, "ok", name)
		writeJSON(w, http.StatusOK, map[string]string{fieldStatus: "revoked"})
		return
	case "generate", "send":
	default:
		writeError(w, http.StatusBadRequest, "action must be generate, revoke or send")
		return
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "cannot generate secret")
		return
	}
	secret := hex.EncodeToString(raw)
	if _, err := h.DB.Exec(`UPDATE clusters SET secret_hash=? WHERE id=?`, secretHash(secret), id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	out := map[string]any{"status": "generated", "secret": secret}
	if req.Action == "send" {
		sent, sendErr := h.pushSecretToAgent(agentURL, secret)
		out["sent"] = sent
		if !sent {
			out["send_error"] = sendErr
		}
		h.syncLog(id, "out", "secret-send", 0, map[bool]string{true: "ok", false: "error"}[sent], sendErr)
	} else {
		h.syncLog(id, "admin", "secret-generate", 0, "ok", name)
	}
	h.Log.WithField("cluster_id", id).Warn("operator generated a cluster sync secret")
	writeJSON(w, http.StatusOK, out)
}

// pushSecretToAgent POSTs the fresh secret to the cluster's agent receive
// endpoint. HTTPS only: an http:// agent URL is refused outright, never
// downgraded — the secret must not travel in cleartext.
func (h *Handler) pushSecretToAgent(agentURL, secret string) (bool, string) {
	target := strings.TrimRight(strings.TrimSpace(agentURL), "/") + "/sync/key"
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return false, "agent URL is not https — refusing to send the secret in cleartext"
	}
	body, _ := json.Marshal(map[string]string{"key": secret})
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	// Same CA-trusting client as the sync triggers: self-hosted agents
	// present certificates from the operator CA, not public ones.
	resp, err := agentHTTP().Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, "agent answered HTTP " + resp.Status
	}
	return true, ""
}
