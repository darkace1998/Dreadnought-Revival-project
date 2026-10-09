package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

func adminTestRouter() *mux.Router {
	r := mux.NewRouter()
	registerAdminDashboard(r, "test-admin-key", "http://127.0.0.1:1", "")
	return r
}

func adminGet(r http.Handler, path, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("X-Admin-Key", key)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// The page is static and served to anyone; it holds no data.
func TestAdminDashboardPageServed(t *testing.T) {
	rec := adminGet(adminTestRouter(), "/admin/dashboard", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Server Admin") {
		t.Fatalf("dashboard page: %d", rec.Code)
	}
}

// Every data route needs the admin key.
func TestAdminAPIRequiresTheKey(t *testing.T) {
	r := adminTestRouter()
	for _, path := range []string{"/admin/api/overview", "/admin/api/instances", "/admin/api/online", "/admin/api/players",
		"/admin/api/matches", "/admin/api/reports", "/admin/api/logs?src=mmogbrain", "/admin/api/battle-logs",
		"/admin/api/metrics", "/admin/api/audit", "/admin/api/bans", "/admin/api/players/x/matches"} {
		for _, key := range []string{"", "wrong"} {
			if rec := adminGet(r, path, key); rec.Code != http.StatusForbidden {
				t.Errorf("%s with key %q: %d, want 403", path, key, rec.Code)
			}
		}
	}
	for _, action := range []string{"kick", "ban", "unban"} {
		if rec := adminPost(r, "/admin/api/players/"+adminTestPID+"/"+action, "", `{"reason":"x"}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s without the key: %d, want 403", action, rec.Code)
		}
	}
	del := httptest.NewRequest(http.MethodDelete, "/admin/api/instances/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, del)
	if rec.Code != http.StatusForbidden {
		t.Errorf("stopping an instance without the key: %d, want 403", rec.Code)
	}
}

// Log reading is limited to known files: no path from the request.
func TestAdminLogsRefuseArbitraryPaths(t *testing.T) {
	r := adminTestRouter()
	for _, q := range []string{"src=../../etc/passwd", "src=battle&name=../../../etc/passwd", "src=battle&name=/etc/passwd",
		"src=battle&name=secrets.env", "src=battle&name=battle-x/../../x.log"} {
		if rec := adminGet(r, "/admin/api/logs?"+q, "test-admin-key"); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", q, rec.Code)
		}
	}
	if rec := adminGet(r, "/admin/api/logs?src=mmogbrain", "test-admin-key"); rec.Code != http.StatusOK {
		t.Errorf("a known log: %d, want 200", rec.Code)
	}
}

// The store has ONE connection; a name lookup inside an open result set waits
// for itself forever. That deadlocked the server's whole database the first
// time the dashboard was opened (2026-09-29). Every data route must answer.
func TestAdminAPIDoesNotDeadlockTheStore(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO matches(id,game_mode,map,server_ip,server_port,status,created_at,started_at,battle_match_id) VALUES('m1','TDM','Glacier','127.0.0.1',7777,'ended','2026-09-29T20:00:00Z','2026-09-29T20:00:00Z','bm1')`,
		`INSERT INTO battle_results(match_id,user_id,outcome) VALUES('bm1','` + pid + `','win')`,
		`INSERT INTO client_reports(user_id,type,name,desc) VALUES('` + pid + `','T','N','D')`,
		`INSERT INTO client_telemetry(user_id,rt,battle_id,summary) VALUES('` + pid + `','YA_AnalyticsReceiveCreditsEvent','bm1','s')`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	r := adminTestRouter()
	for _, path := range []string{"/admin/api/matches", "/admin/api/reports", "/admin/api/telemetry", "/admin/api/telemetry?rt=YA_GameModeEvent&q=x", "/admin/api/players?q=", "/admin/api/online", "/admin/api/overview",
		"/admin/api/metrics?days=30", "/admin/api/audit", "/admin/api/bans", "/admin/api/players/" + pid + "/matches", "/admin/api/players/" + pid} {
		done := make(chan int, 1)
		go func() { done <- adminGet(r, path, "test-admin-key").Code }()
		select {
		case code := <-done:
			if code != http.StatusOK {
				t.Errorf("%s: %d", path, code)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s did not answer in 3 s: the single-connection store is deadlocked", path)
		}
	}
}
