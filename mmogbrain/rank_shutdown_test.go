package main

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/handlers"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// The PR ladder is the server's own: rank N starts at the XP the server
// needs for rank N (the sum provisioning uses), so the client's rank from
// "rep" = current_xp equals current_rank.
func TestProgressionDataCarriesTheServersRankLadder(t *testing.T) {
	p := buildMmogProgressionDataPayload()
	if n := bytes.Count(p, []byte("\x02RP")); n != playerRankCount {
		t.Fatalf("%d PR entries, want %d (the client's rank names)", n, playerRankCount)
	}
	total := int32(0)
	for rank := int32(1); rank <= playerRankCount; rank++ {
		total += handlers.RankXPThreshold(rank)
		if !bytes.Contains(p, protocol.AppendStringField(nil, "RP", strconv.Itoa(int(total)))) {
			t.Fatalf("no PR entry with RP=%d for rank %d", total, rank)
		}
	}
	if !bytes.Contains(p, protocol.AppendStringField(nil, "RP", "0")) {
		t.Error("rank 1 must start at 0 reputation")
	}
}

// A lost battle server tells its players -- except those whose match already
// reported a result (it ended normally for them).
func TestHostLostPushesServerShutdown(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	h := newSquadHub()
	old := squadHubInstance
	squadHubInstance = h
	t.Cleanup(func() { squadHubInstance = old })
	const stuck, finished = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	h.connected(stuck)
	h.connected(finished)
	if _, err := database.Exec(`INSERT INTO battle_results(match_id,user_id,outcome) VALUES('bm1-r1',?,'win')`, finished); err != nil {
		t.Fatal(err)
	}
	notifyHostLost("bm1", []string{stuck, finished})
	got := h.drainPushes(stuck)
	if len(got) != 1 || protocol.FirstStringField(got[0], "RT") != "YA_ServerShutdown" ||
		!bytes.Contains(got[0], protocol.AppendStringField(nil, "roomState", "6")) {
		t.Errorf("stuck player got %v, want YA_ServerShutdown roomState=6 (Closed)", got)
	}
	if got := h.drainPushes(finished); len(got) != 0 {
		t.Errorf("a player whose match ended normally got %d pushes", len(got))
	}
}
