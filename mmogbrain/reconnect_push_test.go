package main

import (
	"testing"

	"github.com/sirupsen/logrus"
)

func quietTestLog() *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	return log
}

// A player who was already in a match when the connection began never sends
// YA_EnterMatchmaking on it, so queuedForMatch stays false and the push path
// stays disarmed -- they sit in the hangar while their own match runs without
// them, and nothing ever tells them otherwise.
func TestLoggingInMidMatchArmsTheTravelPush(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed player: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO matches(id,game_mode,map,server_ip,server_port,status,created_at,started_at)
		 VALUES('m1','TM','Highlands','10.0.0.73',7777,'active',datetime('now'),datetime('now'))`); err != nil {
		t.Fatalf("seed match: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO match_slots(match_id,user_id,team) VALUES('m1',?,0)`, pid); err != nil {
		t.Fatalf("seed slot: %v", err)
	}

	state := &mmogConnState{playerPID: pid}
	armMatchPushForActiveMatch(quietTestLog(), "test", state)

	if !state.queuedForMatch {
		t.Error("queuedForMatch is still false; the player will never be told to travel")
	}
	if state.serverStartingPushed || state.connectPushed {
		t.Error("the pushes were marked as already sent, so neither will fire")
	}
}

// A player with no match must not arm anything: the flag is what keeps the
// per-frame push path from querying the database for everyone.
func TestLoggingInWithNoMatchArmsNothing(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed player: %v", err)
	}

	state := &mmogConnState{playerPID: pid}
	armMatchPushForActiveMatch(quietTestLog(), "test", state)
	if state.queuedForMatch {
		t.Error("armed the push for a player who is not in a match")
	}
}

// A match with no address is not somewhere to send anyone.
func TestLoggingInWithAnAddresslessMatchArmsNothing(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed player: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO matches(id,game_mode,map,server_ip,server_port,status,created_at,started_at)
		 VALUES('m1','TM','Highlands','',0,'active',datetime('now'),datetime('now'))`); err != nil {
		t.Fatalf("seed match: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO match_slots(match_id,user_id,team) VALUES('m1',?,0)`, pid); err != nil {
		t.Fatalf("seed slot: %v", err)
	}

	state := &mmogConnState{playerPID: pid}
	armMatchPushForActiveMatch(quietTestLog(), "test", state)
	if state.queuedForMatch {
		t.Error("armed the push for a match with no server address")
	}
}

// A player who queued normally in this session must not have their push state
// reset from under them -- that would re-send YA_ServerStarting and YA_Connect.
func TestArmingDoesNotDisturbAPlayerWhoQueuedThisSession(t *testing.T) {
	state := &mmogConnState{playerPID: "x", queuedForMatch: true, serverStartingPushed: true, connectPushed: true}
	armMatchPushForActiveMatch(quietTestLog(), "test", state)
	if !state.serverStartingPushed || !state.connectPushed {
		t.Error("rearmed a connection that had already been pushed; the client would be told to travel twice")
	}
}
