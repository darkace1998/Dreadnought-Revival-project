package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

const syncTestKey = "test-sync-secret-aaaaaaaaaaaaaaaa"

func seedSyncCluster(t *testing.T, h *Handler) string {
	t.Helper()
	// Direct insert: register endpoint tested elsewhere; sync needs a secret.
	id := "11111111-2222-3333-4444-555555555555"
	if _, err := h.DB.Exec(`INSERT INTO clusters(id,name,web_url,battle_ip,secret_hash,last_heartbeat,registered_at)
		VALUES(?,'Sync Cluster','https://x.example','203.0.113.9',?,datetime('now'),datetime('now'))`,
		id, secretHash(syncTestKey)); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	return id
}

func authedSyncReq(t *testing.T, method, path, body, key string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-Sync-Key", key)
	}
	rec := httptest.NewRecorder()
	return rec, req
}

func pushPayload(user, ts string) string {
	raw, _ := json.Marshal(map[string]any{
		"users": []any{map[string]any{
			"user_id": user, "username": "alice", "email": "a@x.org",
			"password_hash": "hash", "created_at": ts, "updated_at": ts,
		}},
		"snapshots": []any{map[string]any{
			"user_id": user, "updated_at": ts, "credits": 15000, "rank": 5, "ships": 3,
			"tables": map[string]any{},
		}},
		"bans": []any{},
	})
	return string(raw)
}

func TestSyncPushPullRoundtrip(t *testing.T) {
	h := testHandler(t)
	seedSyncCluster(t, h)
	uid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	rec, req := authedSyncReq(t, "POST", "/sync/push", pushPayload(uid, "2026-09-29T10:00:00Z"), syncTestKey)
	h.SyncPush(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("push status %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"accepted":2`) {
		t.Fatalf("unexpected push result: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/sync/pull?since=2020-01-01T00:00:00Z", nil)
	req.Header.Set("X-Sync-Key", syncTestKey)
	h.SyncPull(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pull status %d", rec.Code)
	}
	var doc struct {
		Users     []syncUser         `json:"users"`
		Snapshots []map[string]any  `json:"snapshots"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(doc.Users) != 1 || len(doc.Snapshots) != 1 {
		t.Fatalf("unexpected pull: %s", rec.Body.String())
	}
	if doc.Users[0].Username != "alice" {
		t.Errorf("wrong user mirrored: %+v", doc.Users[0])
	}
}

func TestSyncPushRejectsBadKey(t *testing.T) {
	h := testHandler(t)
	rec, req := authedSyncReq(t, "POST", "/sync/push", `{}`, "wrong-key")
	h.SyncPush(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.SyncPull(rec, httptest.NewRequest(http.MethodGet, "/sync/pull", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rec.Code)
	}
}

func TestSyncPushLastWriteWins(t *testing.T) {
	h := testHandler(t)
	seedSyncCluster(t, h)
	uid := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	rec, req := authedSyncReq(t, "POST", "/sync/push", pushPayload(uid, "2026-09-29T12:00:00Z"), syncTestKey)
	h.SyncPush(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("push status %d", rec.Code)
	}
	// Older data must not overwrite newer.
	rec, req = authedSyncReq(t, "POST", "/sync/push", pushPayload(uid, "2026-09-29T10:00:00Z"), syncTestKey)
	h.SyncPush(rec, req)
	if !strings.Contains(rec.Body.String(), `"skipped":2`) {
		t.Fatalf("stale push not skipped: %s", rec.Body.String())
	}
	var credits int
	if err := h.DB.QueryRow(`SELECT credits FROM sync_snapshots WHERE user_id=?`, uid).Scan(&credits); err != nil {
		t.Fatal(err)
	}
	if credits != 15000 {
		t.Errorf("credits = %d, want the newer 15000", credits)
	}
}

func TestSyncPresenceBlocksOtherCluster(t *testing.T) {
	h := testHandler(t)
	seedSyncCluster(t, h)
	uid := "dddddddddddddddddddddddddddddddd"

	// Push with in_match=true: presence must report the cluster.
	raw, _ := json.Marshal(map[string]any{
		"users": []any{}, "bans": []any{},
		"snapshots": []any{map[string]any{
			"user_id": uid, "updated_at": "2026-09-29T12:00:00Z",
			"credits": 1, "rank": 1, "ships": 1, "in_match": true,
			"tables": map[string]any{},
		}},
	})
	rec, req := authedSyncReq(t, "POST", "/sync/push", string(raw), syncTestKey)
	h.SyncPush(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("push status %d", rec.Code)
	}

	presence := func(path string) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		// mux vars are the only router magic here; set directly.
		req = mux.SetURLVars(req, map[string]string{"user_id": uid})
		h.SyncPresence(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("presence %s: status %d", path, rec.Code)
		}
		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return doc
	}
	if doc := presence("/presence/" + uid); doc["in_match"] != true || doc["cluster"] != "Sync Cluster" {
		t.Fatalf("expected block on Sync Cluster, got %v", doc)
	}
	// Same cluster excluded (by id and by name): no block.
	cid := "11111111-2222-3333-4444-555555555555"
	if doc := presence("/presence/" + uid + "?except=" + cid); doc["in_match"] != false {
		t.Fatalf("own cluster id must be excluded, got %v", doc)
	}
	if doc := presence("/presence/" + uid + "?except=Sync+Cluster"); doc["in_match"] != false {
		t.Fatalf("own cluster name must be excluded, got %v", doc)
	}
	// Match over (push in_match=false) clears the block.
	raw, _ = json.Marshal(map[string]any{
		"users": []any{}, "bans": []any{},
		"snapshots": []any{map[string]any{
			"user_id": uid, "updated_at": "2026-09-29T12:05:00Z",
			"credits": 1, "rank": 1, "ships": 1, "in_match": false,
			"tables": map[string]any{},
		}},
	})
	rec, req = authedSyncReq(t, "POST", "/sync/push", string(raw), syncTestKey)
	h.SyncPush(rec, req)
	if doc := presence("/presence/" + uid); doc["in_match"] != false {
		t.Fatalf("finished match must clear, got %v", doc)
	}
	// Stale report (older than the window) counts as gone.
	if _, err := h.DB.Exec(`UPDATE sync_presence SET in_match=1,updated_at='2020-01-01T00:00:00Z'
		WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	if doc := presence("/presence/" + uid); doc["in_match"] != false {
		t.Fatalf("stale presence must not block, got %v", doc)
	}
}

func TestSyncRegisterCheck(t *testing.T) {
	h := testHandler(t)
	seedSyncCluster(t, h)
	rec, req := authedSyncReq(t, "POST", "/sync/push", pushPayload("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "2026-09-29T10:00:00Z"), syncTestKey)
	h.SyncPush(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("push status %d", rec.Code)
	}
	check := func(query string) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		h.SyncRegisterCheck(rec, httptest.NewRequest(http.MethodGet, "/register-check"+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("check %s: status %d", query, rec.Code)
		}
		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return doc
	}
	// pushPayload uses username alice / email a@x.org.
	if doc := check("?username=alice"); doc["taken"] != true {
		t.Fatalf("username must be taken: %v", doc)
	}
	if doc := check("?email=a@x.org"); doc["taken"] != true {
		t.Fatalf("email must be taken: %v", doc)
	}
	if doc := check("?username=bob&email=b@x.org"); doc["taken"] != false {
		t.Fatalf("fresh name+address must be free: %v", doc)
	}
	if doc := check("?username=alice&email=b@x.org"); doc["taken"] != true {
		t.Fatalf("taken name with free address must still block: %v", doc)
	}
	rec = httptest.NewRecorder()
	h.SyncRegisterCheck(rec, httptest.NewRequest(http.MethodGet, "/register-check", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty query: got %d, want 400", rec.Code)
	}
}

func TestSyncPushLogsEveryCall(t *testing.T) {
	h := testHandler(t)
	seedSyncCluster(t, h)
	rec, req := authedSyncReq(t, "POST", "/sync/push", pushPayload("cccccccccccccccccccccccccccccccc", "2026-09-29T10:00:00Z"), syncTestKey)
	h.SyncPush(rec, req)
	rec, req = authedSyncReq(t, "POST", "/sync/push", `{}`, "nope")
	h.SyncPush(rec, req)

	var n int
	if err := h.DB.QueryRow(`SELECT COUNT(*) FROM sync_log WHERE endpoint='push'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("sync_log push rows = %d, want 2 (ok + denied)", n)
	}
	var denied int
	if err := h.DB.QueryRow(`SELECT COUNT(*) FROM sync_log WHERE status='denied'`).Scan(&denied); err != nil {
		t.Fatal(err)
	}
	if denied != 1 {
		t.Errorf("denied rows = %d, want 1", denied)
	}
}

// fakeAgents serves any number of cluster agents: path /<tag>/sync/now
// records the force flag in call order; tags in fail answer HTTP 500.
func fakeAgents(t *testing.T, calls *[]string, fail map[string]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tag := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/sync/now")
		var req struct {
			Force bool `json:"force"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		force := "false"
		if req.Force {
			force = "true"
		}
		*calls = append(*calls, tag+":"+force)
		if fail[tag] {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","pushed":3,"applied":2}`))
	}))
}

func seedSyncClusterWithAgent(t *testing.T, h *Handler, id, name, agentURL string) {
	t.Helper()
	if _, err := h.DB.Exec(`INSERT INTO clusters(id,name,web_url,battle_ip,agent_url,last_heartbeat,registered_at)
		VALUES(?,?,?,?,?,datetime('now'),datetime('now'))`,
		id, name, "https://x.example", "203.0.113.9", agentURL); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
}

func TestAdminSyncSettings(t *testing.T) {
	h := testHandler(t)
	id := seedSyncCluster(t, h)

	get := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		h.AdminSyncSettings(rec, httptest.NewRequest(http.MethodGet, "/admin/api/sync-settings", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("get: status %d", rec.Code)
		}
		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decode: %v", err)
		}
		s, _ := doc["main_cluster_id"].(string)
		return s
	}
	post := func(body string) int {
		t.Helper()
		rec := httptest.NewRecorder()
		h.AdminSyncSettings(rec, httptest.NewRequest(http.MethodPost, "/admin/api/sync-settings",
			strings.NewReader(body)))
		return rec.Code
	}
	if got := get(); got != "" {
		t.Fatalf("fresh settings: %q, want empty", got)
	}
	if code := post(`{"main_cluster_id":"nope"}`); code != http.StatusNotFound {
		t.Fatalf("unknown cluster: got %d, want 404", code)
	}
	if code := post(`{"main_cluster_id":"` + id + `"}`); code != http.StatusOK {
		t.Fatalf("set main: got %d", code)
	}
	if got := get(); got != id {
		t.Fatalf("main = %q, want %q", got, id)
	}
	if code := post(`{"main_cluster_id":""}`); code != http.StatusOK {
		t.Fatalf("clear: got %d", code)
	}
	if got := get(); got != "" {
		t.Fatalf("after clear: %q", got)
	}
}

func TestAdminSyncNow(t *testing.T) {
	h := testHandler(t)
	var calls []string
	srv := fakeAgents(t, &calls, map[string]bool{"b": true})
	defer srv.Close()
	seedSyncClusterWithAgent(t, h, "id-a", "Alpha", srv.URL+"/a")
	seedSyncClusterWithAgent(t, h, "id-b", "Beta", srv.URL+"/b")
	seedSyncCluster(t, h) // Sync Cluster: no agent URL, skipped silently.

	rec := httptest.NewRecorder()
	h.AdminSyncNow(rec, httptest.NewRequest(http.MethodPost, "/admin/api/sync-now", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var doc struct {
		Results []triggerResult `json:"results"`
		Count   int             `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Count != 2 {
		t.Fatalf("count = %d, want 2 (agent-less cluster skipped)", doc.Count)
	}
	byName := map[string]triggerResult{}
	for _, r := range doc.Results {
		byName[r.Name] = r
	}
	if !byName["Alpha"].OK || byName["Alpha"].Pushed != 3 || byName["Alpha"].Applied != 2 {
		t.Fatalf("alpha wrong: %+v", byName["Alpha"])
	}
	if byName["Beta"].OK || byName["Beta"].Detail == "" {
		t.Fatalf("beta must fail with detail: %+v", byName["Beta"])
	}
	var logged int
	if err := h.DB.QueryRow(`SELECT COUNT(*) FROM sync_log WHERE endpoint='sync-now'`).Scan(&logged); err != nil || logged != 2 {
		t.Fatalf("sync-now log rows = %d (err %v), want 2", logged, err)
	}
}

func TestAdminRollout(t *testing.T) {
	h := testHandler(t)
	var calls []string
	srv := fakeAgents(t, &calls, map[string]bool{})
	defer srv.Close()
	seedSyncClusterWithAgent(t, h, "id-a", "Alpha", srv.URL+"/a")
	seedSyncClusterWithAgent(t, h, "id-b", "Beta", srv.URL+"/b")
	if err := h.setSetting("main_cluster_id", "id-a"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.AdminRollout(rec, httptest.NewRequest(http.MethodPost, "/admin/api/rollout", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	// Main first without force, then everyone else forced.
	if len(calls) != 2 || calls[0] != "a:false" || calls[1] != "b:true" {
		t.Fatalf("call order = %v, want [a:false b:true]", calls)
	}
	var doc struct {
		Main    triggerResult   `json:"main"`
		Results []triggerResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !doc.Main.OK || doc.Main.Name != "Alpha" || len(doc.Results) != 1 || !doc.Results[0].OK {
		t.Fatalf("unexpected rollout doc: %s", rec.Body.String())
	}
}

func TestAdminRolloutAbortsWhenMainFails(t *testing.T) {
	h := testHandler(t)
	var calls []string
	srv := fakeAgents(t, &calls, map[string]bool{"a": true})
	defer srv.Close()
	seedSyncClusterWithAgent(t, h, "id-a", "Alpha", srv.URL+"/a")
	seedSyncClusterWithAgent(t, h, "id-b", "Beta", srv.URL+"/b")
	if err := h.setSetting("main_cluster_id", "id-a"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.AdminRollout(rec, httptest.NewRequest(http.MethodPost, "/admin/api/rollout", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if len(calls) != 1 || calls[0] != "a:false" {
		t.Fatalf("calls = %v, want only the failed main", calls)
	}
}

func TestAdminRolloutNeedsMain(t *testing.T) {
	h := testHandler(t)
	rec := httptest.NewRecorder()
	h.AdminRollout(rec, httptest.NewRequest(http.MethodPost, "/admin/api/rollout", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
