package main

import (
	"strings"
	"testing"
)

// The client splits the login reply's "Username" at '#' and stores the name
// only when both halves are non-empty (0x2AB1ED0); the bare name was never
// stored, so the hangar showed no player name.
func TestGatewayUsernameTagSplitsIntoNameAndNumber(t *testing.T) {
	pid := "0123456789abcdef0123456789abcdef"
	tag := gatewayUsernameTag("Pilot#X", pid)
	name, number, ok := strings.Cut(tag, "#")
	if !ok || name != "PilotX" || len(number) != 4 {
		t.Fatalf("tag %q", tag)
	}
	if again := gatewayUsernameTag("PilotX", pid); again != tag {
		t.Errorf("not stable: %q then %q", tag, again)
	}
	if gatewayUsernameTag("PilotX", "fedcba9876543210fedcba9876543210") == tag {
		t.Log("two ids share a number (allowed, display only)")
	}
	if got := gatewayUsernameTag("  ", pid); got != "" {
		t.Errorf("empty name gave %q", got)
	}
}
