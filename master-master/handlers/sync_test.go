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
