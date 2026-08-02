package server

import "testing"

// Ready is what the API exposes so mmogbrain can hold its YA_Connect travel push
// until the battle server is genuinely hosting. It must never block -- it is
// answered inside an HTTP handler -- and must not report ready before the engine
// says so, because the client travels the instant YA_Connect arrives.
func TestInstanceReadyReflectsTheEngineSignal(t *testing.T) {
	inst := &Instance{ready: make(chan struct{})}
	if inst.Ready() {
		t.Fatal("a freshly launched instance reports ready")
	}
	inst.markReady()
	if !inst.Ready() {
		t.Fatal("markReady did not make Ready true")
	}
	// The log scraper calls markReady on every matching line, and both stdout
	// and stderr are scraped, so it has to survive being called again.
	inst.markReady()
	if !inst.Ready() {
		t.Fatal("a second markReady changed the result")
	}
}

// The engine lines that mean "hosting". These are the exact strings a healthy
// launch prints; anything looser would let ordinary startup noise be read as
// readiness and send the client to a server still loading its map.
func TestIsReadyLine(t *testing.T) {
	ready := []string{
		"[2026.08.02-19.00.00:000][  0]LogGameMode: Match State Changed from EnteringMap to WaitingToStart",
		"LogGameMode: Match State Changed from WaitingToStart to InProgress",
	}
	notReady := []string{
		"LogInit: Display: Starting Game.",
		"LogGameMode: Match State Changed from WaitingToStart to LeavingMap",
		"LogNet: Error: UDP bind failed",
		"",
	}
	for _, line := range ready {
		if !isReadyLine(line) {
			t.Errorf("not detected as readiness: %q", line)
		}
	}
	for _, line := range notReady {
		if isReadyLine(line) {
			t.Errorf("wrongly detected as readiness: %q", line)
		}
	}
}
