package main

import "testing"

// The profile gate needs guid == profile == the own id the client took from
// server.notice user_token, all as a dashed GUID (0x142AA9B60 parses it).
func TestSelfUserProfileEventMatchesTheOwnID(t *testing.T) {
	const pid = "0123456789abcdef0123456789abcdef"
	ev := selfUserProfileEvent(pid, "peer")
	if ev["type"] != "user.profile" {
		t.Fatalf("type = %v", ev["type"])
	}
	data := ev["data"].(map[string]any)
	want := dashedPlayerGUID(pid)
	if want != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatalf("dashedPlayerGUID = %q", want)
	}
	if data["guid"] != want || data["profile"] != want {
		t.Fatalf("guid=%v profile=%v, want %s", data["guid"], data["profile"], want)
	}
	if data["status"] != 1 {
		t.Fatalf("status = %v, want 1 (online)", data["status"])
	}
}
