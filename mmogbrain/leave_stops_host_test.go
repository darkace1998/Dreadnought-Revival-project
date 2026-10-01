package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/matchmaker"
	"github.com/sirupsen/logrus"
)

// A player who cancels after their match formed must not leave its battle
// server running: the match is ended AND its host is stopped. Only the match
// ended was, so a cancel-and-requeue left two hosts for one player
// (operator, 2026-10-01).
func TestLeavingAFormedMatchStopsItsBattleServer(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"
	stopped := make(chan string, 4)
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.Header.Get("X-Internal-Key") == "key" {
			stopped <- r.URL.Path
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer controlPlane.Close()
	activeMatchmaker = matchmaker.New(database, logrus.New(), controlPlane.URL, "key", 1)
	defer func() { activeMatchmaker = nil }()

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := database.Exec(`INSERT INTO matches(id,game_mode,map,server_ip,server_port,status,created_at,started_at,instance_id)
		VALUES('m1','TDM','Glacier','127.0.0.1',7900,'active',?,?,'inst-1')`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO match_slots(match_id,user_id,team,joined_at) VALUES('m1',?,1,?)`, pid, now); err != nil {
		t.Fatal(err)
	}
	buildMmogLeaveMatchmakingPayload("YA_LeaveMatchmaking", pid)

	var status string
	_ = database.QueryRow(`SELECT status FROM matches WHERE id='m1'`).Scan(&status)
	if status != "ended" {
		t.Errorf("match status %q, want ended", status)
	}
	select {
	case path := <-stopped:
		if path != "/instances/inst-1" {
			t.Errorf("stopped %s, want /instances/inst-1", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the battle server of the emptied match was not stopped")
	}
}
