package spawner

import (
	"sync/atomic"
	"testing"

	"github.com/sirupsen/logrus"
)

func quietLog() *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	return log
}

// Readiness has exactly one source on this build: the engine's own match-state
// line on stdout. Nothing else tells this process the server is hosting -- the
// UDP port cannot be probed reliably (a duplicate bind succeeds on Windows) and
// the engine exposes no status endpoint.
func TestLogWriterDetectsTheEngineHostingLine(t *testing.T) {
	for _, line := range []string{
		"[2026.08.02-19.00.00:000][  0]LogGameMode: Match State Changed from EnteringMap to WaitingToStart\n",
		"[2026.08.02-19.00.05:000][  0]LogGameMode: Match State Changed from WaitingToStart to InProgress\n",
	} {
		var fired atomic.Bool
		w := newInstanceLogWriter(quietLog(), "i1", "stdout", func() { fired.Store(true) })
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("write: %v", err)
		}
		if !fired.Load() {
			t.Errorf("no readiness signal for %q", line)
		}
	}
}

// The line arrives in whatever chunks the pipe delivers, so a marker split
// across two writes must still be detected -- it is only complete once the
// newline shows up.
func TestLogWriterDetectsReadinessAcrossWrites(t *testing.T) {
	var fired atomic.Bool
	w := newInstanceLogWriter(quietLog(), "i1", "stdout", func() { fired.Store(true) })
	if _, err := w.Write([]byte("LogGameMode: Match State Changed from Enteri")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if fired.Load() {
		t.Fatal("signalled on a partial line")
	}
	if _, err := w.Write([]byte("ngMap to WaitingToStart\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !fired.Load() {
		t.Fatal("no readiness signal once the line completed")
	}
}

// Ordinary startup noise must not be mistaken for readiness: a client told to
// travel early lands on a server that is not accepting connections.
func TestLogWriterIgnoresLinesThatAreNotReadiness(t *testing.T) {
	var fired atomic.Bool
	w := newInstanceLogWriter(quietLog(), "i1", "stdout", func() { fired.Store(true) })
	for _, line := range []string{
		"LogInit: Display: Starting Game.\n",
		"LogGameMode: Match State Changed from WaitingToStart to LeavingMap\n",
		"LogNet: Error: UDP bind failed\n",
	} {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if fired.Load() {
		t.Fatal("a non-readiness line was treated as readiness")
	}
}

// Ready reads through a POINTER to the flag on purpose: List() and Get() hand
// out Instance by value, and a copied flag would report whatever was true when
// the snapshot was taken -- which for the /instances/{id} poll is always "not
// ready", since the copy is made before the engine finishes loading.
func TestInstanceReadyIsVisibleThroughACopy(t *testing.T) {
	inst := &Instance{ready: new(atomic.Bool)}
	snapshot := *inst
	if snapshot.Ready() {
		t.Fatal("a fresh instance reports ready")
	}
	inst.markReady()
	if !snapshot.Ready() {
		t.Fatal("a copy taken before markReady does not observe it")
	}
}

// An Instance built without the flag (zero value, or any construction path that
// predates it) must report not-ready rather than panic.
func TestInstanceReadyToleratesAMissingFlag(t *testing.T) {
	var inst Instance
	if inst.Ready() {
		t.Fatal("zero-value instance reports ready")
	}
	inst.markReady() // must not panic
}
