package matchmaker

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
func TestPollBattleServersStampsOnlyWhenReady(t *testing.T) {
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

	m.pollBattleServers()
	if v := readyAt(t, database, "m1"); v.Valid {
		t.Fatalf("stamped %q while the server was still loading", v.String)
	}

	ready = true
	m.pollBattleServers()
	if v := readyAt(t, database, "m1"); !v.Valid || v.String == "" {
		t.Fatal("no stamp after the control plane reported ready")
	}
}

// A control plane with no readiness route -- game-manager has none -- must leave
// the stamp unset so the push falls back to the fixed delay rather than never
// firing or firing immediately.
func TestPollBattleServersToleratesAControlPlaneWithoutIt(t *testing.T) {
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

			readinessMatchmaker(t, database, srv.URL).pollBattleServers()
			if v := readyAt(t, database, "m1"); v.Valid {
				t.Fatalf("stamped %q from a control plane that reported nothing usable", v.String)
			}
		})
	}
}

// A match with no instance handle cannot be polled at all.
//
// This used to also assert that an ALREADY-STAMPED match is never polled again,
// on the grounds that the poll would otherwise run every 3s for the life of the
// match. That assertion is now wrong on purpose: the poll is what notices the
// battle server going away, so it has to keep watching a match after it has gone
// ready. What it must still not do is overwrite an existing stamp, which is
// asserted below.
func TestPollBattleServersSkipsMatchesItCannotPoll(t *testing.T) {
	database := sweepTestDB(t)
	insertActiveMatch(t, database, "no-instance", "")
	insertActiveMatch(t, database, "already-ready", "inst-2")
	if _, err := database.Exec(
		`UPDATE matches SET server_ready_at='2026-08-02T00:00:00Z' WHERE id='already-ready'`); err != nil {
		t.Fatalf("stamp: %v", err)
	}

	var polledPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polledPaths = append(polledPaths, r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ready":true,"running":true}`))
	}))
	defer srv.Close()

	readinessMatchmaker(t, database, srv.URL).pollBattleServers()
	for _, path := range polledPaths {
		if strings.Contains(path, "no-instance") || path == "/instances/" {
			t.Errorf("polled a match with no instance handle: %s", path)
		}
	}
	if v := readyAt(t, database, "already-ready"); v.String != "2026-08-02T00:00:00Z" {
		t.Fatalf("existing stamp was overwritten: %q", v.String)
	}
}

// The whole point of continuing to poll: when the control plane says the host is
// gone, the match ends and its players are freed. Without this a dead host left
// everyone in it "matched" until MaxMatchLifetime expired 45 minutes later --
// unable to queue again, and now liable to be pushed straight back at an address
// with nothing behind it, since a player who logs in mid-match gets the travel
// push.
func TestPollBattleServersEndsAMatchWhoseHostIsGone(t *testing.T) {
	database := sweepTestDB(t)
	insertActiveMatch(t, database, "m1", "inst-1")
	if _, err := database.Exec(`INSERT INTO match_slots(match_id,user_id,team) VALUES('m1','player-1',0)`); err != nil {
		t.Fatalf("seed slot: %v", err)
	}

	alive := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !alive {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ready":true,"running":true}`))
	}))
	defer srv.Close()

	m := readinessMatchmaker(t, database, srv.URL)
	m.pollBattleServers() // sees it alive, which is what makes the later 404 readable
	if status := matchStatus(t, database, "m1"); status != "active" {
		t.Fatalf("match ended while its host was alive: %q", status)
	}

	alive = false
	m.pollBattleServers()
	if status := matchStatus(t, database, "m1"); status != "ended" {
		t.Errorf("match status = %q, want ended once the host was gone", status)
	}
	var slots int
	if err := database.QueryRow(`SELECT COUNT(*) FROM match_slots WHERE match_id='m1'`).Scan(&slots); err != nil {
		t.Fatalf("count slots: %v", err)
	}
	if slots != 0 {
		t.Errorf("%d slots left; the players are still pinned to a dead match", slots)
	}
}

// A control plane with no per-instance route 404s EVERY id, forever. Reading
// that as "the host is gone" would end every live match on the older
// game-manager, so a 404 only counts once that instance has answered a 200.
func TestPollBattleServersIgnoresA404FromAControlPlaneWithoutTheRoute(t *testing.T) {
	database := sweepTestDB(t)
	insertActiveMatch(t, database, "m1", "inst-1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := readinessMatchmaker(t, database, srv.URL)
	m.pollBattleServers()
	m.pollBattleServers()
	if status := matchStatus(t, database, "m1"); status != "active" {
		t.Errorf("match status = %q; a 404 from a control plane that never knew the instance must not end it", status)
	}
}

// A running host that has not finished loading must not be mistaken for a dead
// one.
func TestPollBattleServersKeepsAMatchWhoseHostIsStillLoading(t *testing.T) {
	database := sweepTestDB(t)
	insertActiveMatch(t, database, "m1", "inst-1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ready":false,"running":true}`))
	}))
	defer srv.Close()

	readinessMatchmaker(t, database, srv.URL).pollBattleServers()
	if status := matchStatus(t, database, "m1"); status != "active" {
		t.Errorf("match status = %q, want active", status)
	}
}

func matchStatus(t *testing.T, database *sql.DB, id string) string {
	t.Helper()
	var status string
	if err := database.QueryRow(`SELECT status FROM matches WHERE id=?`, id).Scan(&status); err != nil {
		t.Fatalf("read match status: %v", err)
	}
	return status
}

// This asserted the opposite until 2026-08-03: that every queued mode is
// redirected to TM, because TM is the only mode whose GAME MODE supplies a
// loadout. The assertion was inverted after measuring what that redirect
// actually did to a host.
//
// TM is also the only mode where ActivateBattlePlayerStarts finds no orbit spawn
// locations, so the player never reaches ship selection at all -- measured on a
// host with no client, same map and binary, only the URL's game option
// differing: TM loads 12 sublevels and logs the error, while TDM, BC and the
// map's own default each load 13 and do not. Trading a working ship-selection
// screen for a speculative pawn spawn was the wrong way round.
func TestQueuedGameModeIsHonouredByDefault(t *testing.T) {
	os.Unsetenv("DN_FORCE_GAME_MODE")
	for _, queued := range []string{"TDM", "BC", "Onslaught", "TER", "TM"} {
		if got := runnableGameMode(queued); got != queued {
			t.Errorf("runnableGameMode(%q) = %q; the queued mode must be honoured", queued, got)
		}
	}
}

// The override is still there for anyone who wants to experiment with the TM
// trade, by name or with a plain "1".
func TestForcedGameModeOverride(t *testing.T) {
	t.Setenv("DN_FORCE_GAME_MODE", "TMBasic")
	if got := runnableGameMode("TDM"); got != "TMBasic" {
		t.Errorf("got %q, want TMBasic", got)
	}
	t.Setenv("DN_FORCE_GAME_MODE", "1")
	if got := runnableGameMode("TDM"); got != DefaultSpawnableGameMode {
		t.Errorf(`DN_FORCE_GAME_MODE=1 = %q, want %q`, got, DefaultSpawnableGameMode)
	}
	t.Setenv("DN_FORCE_GAME_MODE", "")
	if got := runnableGameMode("TDM"); got != "TDM" {
		t.Errorf("empty DN_FORCE_GAME_MODE should honour the queued mode, got %q", got)
	}
	// A typo must not send every match to a mode the client does not know.
	t.Setenv("DN_FORCE_GAME_MODE", "NotAMode")
	if got := runnableGameMode("TDM"); got != "TDM" {
		t.Errorf("an invalid override should fall back to the queued mode, got %q", got)
	}
}
