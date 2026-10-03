package server

import (
	"bytes"
	"sync/atomic"
	"testing"
	"time"
)

// A host is ready a settle time after its game mode starts, not at
// WaitingToStart: players who travelled in that early often had no ships to
// pick (13-19% of such joins, 4% once the game mode had run 3+ s).
func TestReadyWaitsForTheGameMode(t *testing.T) {
	gameModeSettle, readyFallback = 50*time.Millisecond, 400*time.Millisecond
	defer func() { gameModeSettle, readyFallback = 4*time.Second, 15*time.Second }()

	var fired atomic.Int32
	var out bytes.Buffer
	w := newLogWriter(&out, nil, "abcdef01", false, func() { fired.Add(1) })
	write := func(s string) {
		if _, err := w.Write([]byte(s + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	write("LogGameMode: Match State Changed from EnteringMap to WaitingToStart")
	time.Sleep(20 * time.Millisecond)
	if fired.Load() != 0 {
		t.Fatal("ready at WaitingToStart, before the game mode started")
	}
	write("[dn-host-loadout] bots: game mode 0000 (type 3) m_enableSpawnAI 0 -> 1. The game fills both teams")
	time.Sleep(20 * time.Millisecond)
	if fired.Load() != 0 {
		t.Fatal("ready without the settle time")
	}
	time.Sleep(80 * time.Millisecond)
	if fired.Load() == 0 {
		t.Fatal("not ready after the game mode started and settled")
	}

	// No game-mode line at all: the fallback still makes it ready.
	var fired2 atomic.Int32
	w2 := newLogWriter(&out, nil, "abcdef02", false, func() { fired2.Add(1) })
	if _, err := w2.Write([]byte("LogGameMode: Match State Changed from EnteringMap to WaitingToStart\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if fired2.Load() == 0 {
		t.Fatal("the fallback never made the host ready")
	}

	// The usual case: InProgress printed in the same instant as
	// WaitingToStart. It must not make the host ready before the game mode.
	var fired3 atomic.Int32
	w3 := newLogWriter(&out, nil, "abcdef03", false, func() { fired3.Add(1) })
	for _, line := range []string{
		"LogGameMode:Display: Match State Changed from EnteringMap to WaitingToStart",
		"LogGameMode:Display: Match State Changed from WaitingToStart to InProgress",
	} {
		if _, err := w3.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if fired3.Load() != 0 {
		t.Fatal("ready at InProgress, before the game mode started")
	}
}
