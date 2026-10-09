package main

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func resetAdminModeration(t *testing.T) {
	t.Helper()
	reset := func() {
		adminModeration.mu.Lock()
		adminModeration.loaded = false
		adminModeration.banned = map[string]bool{}
		adminModeration.kicked = map[string]time.Time{}
		adminModeration.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

type adminBanDetail struct {
	Banned    bool   `json:"banned"`
	BanReason string `json:"ban_reason"`
}

// A ban is enforced by mmogbrain itself (the launcher JWT outlives it), is
// recorded in the audit log, and an unban lifts it.
func TestAdminBanAndUnban(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	resetAdminModeration(t)
	if err := seedMmogPlayerState(database, adminTestPID); err != nil {
		t.Fatal(err)
	}
	r := adminTestRouter()
	base := "/admin/api/players/" + adminTestPID

	if rec := adminPost(r, base+"/ban", "test-admin-key", `{"reason":""}`); rec.Code != 400 {
		t.Errorf("ban without a reason: %d, want 400", rec.Code)
	}
	if rec := adminPost(r, base+"/ban", "wrong", `{"reason":"x"}`); rec.Code != 403 {
		t.Errorf("ban with a wrong key: %d, want 403", rec.Code)
	}
	rec := adminPost(r, base+"/ban", "test-admin-key", `{"reason":"cheating"}`)
	var d adminBanDetail
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &d) != nil || !d.Banned || d.BanReason != "cheating" {
		t.Fatalf("ban: %d %s", rec.Code, rec.Body.String())
	}
	// Dashed form too: the gateway sees the account id with dashes.
	dashed := adminTestPID[:8] + "-" + adminTestPID[8:12] + "-" + adminTestPID[12:16] + "-" + adminTestPID[16:20] + "-" + adminTestPID[20:]
	if !adminPlayerBanned(adminTestPID) || !adminPlayerBanned(dashed) {
		t.Fatal("banned player not refused")
	}
	if !adminConnectionRevoked(adminTestPID, time.Now()) {
		t.Fatal("a banned player's game connection is not dropped")
	}
	// The ban survives a restart (loaded from admin_bans).
	adminModeration.mu.Lock()
	adminModeration.loaded, adminModeration.banned = false, map[string]bool{}
	adminModeration.mu.Unlock()
	if !adminPlayerBanned(adminTestPID) {
		t.Fatal("ban lost after reloading")
	}

	rec = adminPost(r, base+"/unban", "test-admin-key", `{}`)
	d = adminBanDetail{}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &d) != nil || d.Banned {
		t.Fatalf("unban: %d %s", rec.Code, rec.Body.String())
	}
	if adminPlayerBanned(adminTestPID) {
		t.Fatal("still banned after unban")
	}

	var audit struct {
		Audit []adminAuditRow `json:"audit"`
	}
	rec = adminGet(r, base+"/audit", "test-admin-key")
	if err := json.Unmarshal(rec.Body.Bytes(), &audit); err != nil {
		t.Fatal(err)
	}
	if len(audit.Audit) != 2 || audit.Audit[0].Action != "unban" || audit.Audit[1].Action != "ban" ||
		!strings.Contains(audit.Audit[1].Details, "cheating") {
		t.Fatalf("audit: %+v", audit.Audit)
	}
}

// A kick closes the social socket now and drops game connections that were
// open before it, but not one opened afterwards (a kick is not a ban).
func TestAdminKick(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	resetAdminModeration(t)
	if err := seedMmogPlayerState(database, adminTestPID); err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	peer := newSocialPeer(adminTestPID, "peer", server)
	socialHubInstance.mu.Lock()
	socialHubInstance.peers[adminTestPID] = peer
	socialHubInstance.mu.Unlock()
	t.Cleanup(func() {
		socialHubInstance.mu.Lock()
		delete(socialHubInstance.peers, adminTestPID)
		socialHubInstance.mu.Unlock()
	})
	opened := time.Now().Add(-time.Minute)

	rec := adminPost(adminTestRouter(), "/admin/api/players/"+adminTestPID+"/kick", "test-admin-key", `{"reason":"afk"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"was_online":true`) {
		t.Fatalf("kick: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := client.Write([]byte("x")); err == nil {
		t.Error("the social socket is still open")
	}
	socialHubInstance.mu.RLock()
	_, still := socialHubInstance.peers[adminTestPID]
	socialHubInstance.mu.RUnlock()
	if still {
		t.Error("the player is still listed online")
	}
	if !adminConnectionRevoked(adminTestPID, opened) {
		t.Error("a game connection from before the kick is not dropped")
	}
	if adminConnectionRevoked(adminTestPID, time.Now().Add(time.Second)) {
		t.Error("a reconnect after the kick is dropped too")
	}
	if adminPlayerBanned(adminTestPID) {
		t.Error("a kick banned the player")
	}
}

func TestAdminPlayerMatchHistory(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	if err := seedMmogPlayerState(database, adminTestPID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO matches(id,battle_match_id,game_mode,map,status,fleet_type) VALUES('m1','bm1','TDM','/Game/Maps/MP/Amirani/MP_Amirani_P','ended',2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO battle_results(match_id,user_id,outcome,kills,credits,fleet_type) VALUES('bm1',?,'win',7,1234,2)`, adminTestPID); err != nil {
		t.Fatal(err)
	}
	rec := adminGet(adminTestRouter(), "/admin/api/players/"+adminTestPID+"/matches", "test-admin-key")
	var body struct {
		Matches []struct {
			Mode, Map, Fleet, Outcome string
			Kills                     int
			Credits                   int64
		} `json:"matches"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Matches) != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	m := body.Matches[0]
	if m.Mode != "TDM" || m.Map != "Amirani" || m.Fleet != "Veteran" || m.Outcome != "win" || m.Kills != 7 || m.Credits != 1234 {
		t.Fatalf("history row: %+v", m)
	}
}
