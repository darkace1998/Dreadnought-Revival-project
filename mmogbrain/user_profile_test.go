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

// The join handler (0x142A377D0) sets the channel name only when
// data.notice.user parses to the same GUID as the player's own profile.
func TestJoinNoticeUserMatchesOwnProfileGUID(t *testing.T) {
	const pid = "0123456789abcdef0123456789abcdef"
	own := selfUserProfileEvent(pid, "peer")["data"].(map[string]any)["guid"]
	notice := chatJoinNotice("dreadnought.global", socialHubInstance.presenceEntry(pid))
	got := notice["data"].(map[string]any)["notice"].(map[string]any)["user"]
	if got != own {
		t.Fatalf("notice.user = %#v, own profile guid = %#v; they must be the same string", got, own)
	}
	// data.user too: the parser reads the data-level fields, and an object
	// there parsed as the zero GUID -- "User 00000000-... joined", the join
	// counted as someone else's and chat broke (2026-09-28).
	if top := notice["data"].(map[string]any)["user"]; top != own {
		t.Fatalf("data.user = %#v, want the own profile guid %#v", top, own)
	}
}

// Channel members go out as GUID strings: objects read as a zero GUID per
// member ("GetUsername called with empty guid", 2026-09-28).
func TestChannelInfoMembersAreGUIDStrings(t *testing.T) {
	info := socialHubInstance.channelInfo("dreadnought.global")
	for _, m := range info["members"].([]any) {
		if _, ok := m.(string); !ok {
			t.Fatalf("member %#v is not a GUID string", m)
		}
	}
}
