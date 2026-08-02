package matchmaker

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func readinessMatchmaker(t *testing.T, database *sql.DB, url string) *Matchmaker {
	t.Helper()
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	return &Matchmaker{DB: database, Log: log, GameMgrURL: url, InternalKey: "k"}
}

func insertActiveMatch(t *testing.T, database *sql.DB, id, instanceID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := database.Exec(
		`INSERT INTO matches(id,game_mode,map,server_ip,server_port,status,created_at,started_at,instance_id)
		 VALUES(?,'TM','Highlands','10.0.0.73',7777,'active',?,?,?)`,
		id, now, now, instanceID); err != nil {
		t.Fatalf("insert match: %v", err)
	}
}

func readyAt(t *testing.T, database *sql.DB, id string) sql.NullString {
	t.Helper()
	var v sql.NullString
	if err := database.QueryRow(`SELECT server_ready_at FROM matches WHERE id=?`, id).Scan(&v); err != nil {
		t.Fatalf("read server_ready_at: %v", err)
	}
	return v
}

// The stamp is what lets the YA_Connect push stop waiting out
// DN_CONNECT_PUSH_DELAY, so it must appear as soon as the control plane says the
// engine is hosting -- and not before.
func TestPollServerReadinessStampsOnlyWhenReady(t *testing.T) {
	database := sweepTestDB(t)
	insertActiveMatch(t, database, "m1", "inst-1")

	ready := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/instances/inst-1" {
			t.Errorf("polled %s, want /instances/inst-1", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		if ready {
			_, _ = w.Write([]byte(`{"id":"inst-1","ready":true,"running":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"inst-1","ready":false,"running":true}`))
	}))
	defer srv.Close()

	m := readinessMatchmaker(t, database, srv.URL)

	m.pollServerReadiness()
	if v := readyAt(t, database, "m1"); v.Valid {
		t.Fatalf("stamped %q while the server was still loading", v.String)
	}

	ready = true
	m.pollServerReadiness()
	if v := readyAt(t, database, "m1"); !v.Valid || v.String == "" {
		t.Fatal("no stamp after the control plane reported ready")
	}
}

// A control plane with no readiness route -- game-manager has none -- must leave
// the stamp unset so the push falls back to the fixed delay rather than never
// firing or firing immediately.
func TestPollServerReadinessToleratesAControlPlaneWithoutIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"no such route", http.StatusNotFound, ""},
		{"no ready field", http.StatusOK, `{"id":"inst-1","port":7777}`},
		{"error", http.StatusInternalServerError, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := sweepTestDB(t)
			insertActiveMatch(t, database, "m1", "inst-1")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if tc.body != "" {
					_, _ = w.Write([]byte(tc.body))
				}
			}))
			defer srv.Close()

			readinessMatchmaker(t, database, srv.URL).pollServerReadiness()
			if v := readyAt(t, database, "m1"); v.Valid {
				t.Fatalf("stamped %q from a control plane that reported nothing usable", v.String)
			}
		})
	}
}

// Matches with no instance handle cannot be polled, and one already stamped must
// not be polled again -- the poll runs every 3s for the life of the match
// otherwise.
func TestPollServerReadinessSkipsMatchesItCannotOrNeedNotPoll(t *testing.T) {
	database := sweepTestDB(t)
	insertActiveMatch(t, database, "no-instance", "")
	insertActiveMatch(t, database, "already-ready", "inst-2")
	if _, err := database.Exec(
		`UPDATE matches SET server_ready_at='2026-08-02T00:00:00Z' WHERE id='already-ready'`); err != nil {
		t.Fatalf("stamp: %v", err)
	}

	polled := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polled++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ready":true}`))
	}))
	defer srv.Close()

	readinessMatchmaker(t, database, srv.URL).pollServerReadiness()
	if polled != 0 {
		t.Fatalf("polled %d times; want none", polled)
	}
	if v := readyAt(t, database, "already-ready"); v.String != "2026-08-02T00:00:00Z" {
		t.Fatalf("existing stamp was overwritten: %q", v.String)
	}
}
