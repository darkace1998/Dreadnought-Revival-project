package server

import (
	"bytes"
	"strings"
	"testing"
)

// A battle server whose log holds only the dn-dedicated header is not a rare
// event: the client side reported five such captures, every one of them from a
// run the operator actually played in (AGENT-CHAT C35.7). The file looks
// perfectly healthy -- three header lines and a blank -- so nothing distinguishes
// "the engine died before it printed anything" from "this run is still young".
//
// That is the C33.5 shape in our own tooling, so the writer counts what the
// child produced and the exit marker records it.
func TestLogWriterCountsWhatTheChildProduced(t *testing.T) {
	var file, out bytes.Buffer
	w := newLogWriter(&out, &file, "abcdef01-0000-0000-0000-000000000000", false, nil)

	if lines, b := w.stats(); lines != 0 || b != 0 {
		t.Fatalf("fresh writer reports %d lines / %d bytes, want 0/0", lines, b)
	}

	// Split across writes, because the child's stdout arrives in arbitrary
	// chunks and a line can straddle two of them.
	if _, err := w.Write([]byte("LogInit: Starting Game.\nLogNet: par")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("tial line\n")); err != nil {
		t.Fatal(err)
	}

	lines, b := w.stats()
	if lines != 2 {
		t.Errorf("counted %d lines, want 2 -- a line split across two writes must count once", lines)
	}
	if b != int64(len("LogInit: Starting Game.\nLogNet: partial line\n")) {
		t.Errorf("counted %d bytes, want %d", b, len("LogInit: Starting Game.\nLogNet: partial line\n"))
	}
}

// An unterminated trailing fragment must not be counted as a line -- otherwise
// a child that emits a single partial write looks like it logged something and
// the zero-output warning never fires.
func TestLogWriterDoesNotCountAnUnterminatedFragment(t *testing.T) {
	var file, out bytes.Buffer
	w := newLogWriter(&out, &file, "abcdef01", false, nil)

	if _, err := w.Write([]byte("no newline yet")); err != nil {
		t.Fatal(err)
	}
	if lines, _ := w.stats(); lines != 0 {
		t.Errorf("counted %d lines for an unterminated fragment, want 0", lines)
	}
	if file.Len() != 0 {
		t.Errorf("wrote %q to the log before the line was complete", file.String())
	}
}

// The readiness callback must still fire while counting -- the counters are
// bookkeeping and must not change what Launch does.
func TestLogWriterStillDetectsReadinessWhileCounting(t *testing.T) {
	var file, out bytes.Buffer
	fired := 0
	w := newLogWriter(&out, &file, "abcdef01", false, func() { fired++ })

	if _, err := w.Write([]byte(
		"LogGameMode: Match State Changed from EnteringMap to WaitingToStart\n")); err != nil {
		t.Fatal(err)
	}
	if fired != 1 {
		t.Errorf("readiness callback fired %d times, want 1", fired)
	}
	if lines, _ := w.stats(); lines != 1 {
		t.Errorf("counted %d lines, want 1", lines)
	}
	if !strings.Contains(file.String(), "WaitingToStart") {
		t.Errorf("the line did not reach the log file: %q", file.String())
	}
}
