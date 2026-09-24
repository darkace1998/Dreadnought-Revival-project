package matchmaker

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"
)

// A match is ONE fleet tier: the battle server reads FleetTier= from its map
// URL once, into the GameState (0x3A5831 -> GameState+0x1D48). So players with
// different fleet types must never be put in the same match, and the tier must
// reach the control plane.

func queueFleetPlayer(t *testing.T, database *sql.DB, id, user string, fleetType int, queuedAt string) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO queue_entries(id,user_id,game_mode,tier_min,status,queued_at,fleet_type) VALUES(?,?,'TDM',1,'waiting',?,?)`,
		id, user, queuedAt, fleetType); err != nil {
		t.Fatalf("queue %s: %v", user, err)
	}
}

func fleetTierMatchmaker(t *testing.T, database *sql.DB, playersPerMatch int) (*Matchmaker, *[]map[string]interface{}) {
	t.Helper()
	var mu sync.Mutex
	var requests []map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ip":"10.0.0.73","port":7777,"instance_id":"inst-1"}`))
	}))
	t.Cleanup(srv.Close)
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	return &Matchmaker{DB: database, Log: log, GameMgrURL: srv.URL, InternalKey: "k", PlayersPerMatch: playersPerMatch}, &requests
}

func TestFleetTypesNeverShareAMatch(t *testing.T) {
	database := pvpTestDB(t)
	m, requests := fleetTierMatchmaker(t, database, 2)

	// One Recruit and one Veteran: two players, but no compatible pair.
	queueFleetPlayer(t, database, "q1", "alice", 1, "2026-09-24T10:00:00Z")
	queueFleetPlayer(t, database, "q2", "bob", 2, "2026-09-24T10:00:05Z")
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if n := len(*requests); n != 0 {
		t.Fatalf("a Recruit and a Veteran were matched together (%d battle servers requested)", n)
	}

	// A second Veteran: now the two Veterans match, the Recruit keeps waiting.
	queueFleetPlayer(t, database, "q3", "carol", 2, "2026-09-24T10:00:10Z")
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if n := len(*requests); n != 1 {
		t.Fatalf("%d battle servers requested, want 1 for the two Veterans", n)
	}
	var waiting string
	if err := database.QueryRow(`SELECT user_id FROM queue_entries WHERE status='waiting'`).Scan(&waiting); err != nil || waiting != "alice" {
		t.Fatalf("the Recruit should still be waiting, got %q (%v)", waiting, err)
	}
	// Veteran decodes from FleetTier=4 on the battle server.
	if got := (*requests)[0]["fleet_tier"]; got != float64(4) {
		t.Errorf("Veteran match sent fleet_tier %v, want 4", got)
	}
}

func TestRecruitMatchesSendNoFleetTier(t *testing.T) {
	database := pvpTestDB(t)
	m, requests := fleetTierMatchmaker(t, database, 1)
	queueFleetPlayer(t, database, "q1", "alice", 1, "2026-09-24T10:00:00Z")
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 1 {
		t.Fatalf("%d battle servers requested, want 1", len(*requests))
	}
	if _, present := (*requests)[0]["fleet_tier"]; present {
		t.Error("a Recruit match sent fleet_tier; Recruit is the battle server's default and needs none")
	}
}

func TestFleetTierURLValueMatchesTheClientDecoder(t *testing.T) {
	// The decoder (0x3A5831): 4 -> Veteran, 5 -> Legendary, else Recruit.
	for fleetType, want := range map[int]int{0: 0, 1: 0, 2: 4, 3: 5, 9: 0} {
		if got := fleetTierURLValue(fleetType); got != want {
			t.Errorf("fleetTierURLValue(%d) = %d, want %d", fleetType, got, want)
		}
	}
}
