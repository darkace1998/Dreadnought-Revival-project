//go:build windows

package main

import "testing"

// The start URL must name the login map; a bare "?PlayerName=" start argument
// sent new players into the offline tutorial (2026-09-28).
func TestStartURLNamesTheLoginMap(t *testing.T) {
	t.Setenv("DN_NO_START_URL", "")
	for in, want := range map[string]string{
		"Player_1":   "/Game/Maps/Launch_P?PlayerName=Player_1",
		"a b?c=d":    "/Game/Maps/Launch_P?PlayerName=abcd",
		"":           "",
		"?#&":        "",
		"Name.With-": "/Game/Maps/Launch_P?PlayerName=Name.With-",
	} {
		if got := startURL(in); got != want {
			t.Errorf("startURL(%q) = %q, want %q", in, got, want)
		}
	}
	t.Setenv("DN_NO_START_URL", "1")
	if got := startURL("Player"); got != "" {
		t.Errorf("DN_NO_START_URL=1 still gives %q", got)
	}
}
