package main

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// statisticsRequest builds YA_GetPlayerStatistics as the client sends it: a
// "pids" array of 16-byte GUID entries (tag 0x02, unnamed).
func statisticsRequest(t *testing.T, pids ...string) []byte {
	t.Helper()
	b := protocol.AppendStringField(nil, "RT", "YA_GetPlayerStatistics")
	var stack []int
	b, stack = protocol.AppendArrayStart(b, stack, "pids")
	for _, pid := range pids {
		raw, err := hex.DecodeString(pid)
		if err != nil || len(raw) != 16 {
			t.Fatalf("bad pid %s", pid)
		}
		b = append(b, 0x00, 0x02)
		b = append(b, raw...)
	}
	b, _ = protocol.AppendObjectEnd(b, stack)
	return b
}

// The client reads stats[i] for every pid it gets back, so the reply must
// carry ROOT pids and one ROOT stats entry per pid -- an empty stats array
// crashed the client on opening Statistics.
func TestPlayerStatisticsMatchesTheRequestedPids(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	a, b := "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210"
	if _, err := database.Exec(`INSERT INTO battle_results(match_id,user_id,outcome,kills,deaths,assists) VALUES
		('m1',?,'win',5,1,2),('m2',?,'loss',3,4,0)`, a, a); err != nil {
		t.Fatal(err)
	}
	reply := buildMmogPlayerStatisticsPayload(a, statisticsRequest(t, a, b))
	pids := extractNamedMmogArray(t, reply, "pids")
	for _, pid := range []string{a, b} {
		if !bytes.Contains(pids, []byte(pid)) {
			t.Errorf("pids lacks %s", pid)
		}
	}
	stats := extractNamedMmogArray(t, reply, "stats")
	if n := bytes.Count(stats, []byte("TotalGameTimeSec")); n != 2 {
		t.Fatalf("%d stats entries for 2 pids", n)
	}
	for _, f := range []struct{ name, value string }{{"WinNum", "1"}, {"LoseNum", "1"}, {"KillNum", "8"}, {"DeathNum", "5"}, {"AssistNum", "2"}} {
		if !bytes.Contains(stats, protocol.AppendStringField(nil, f.name, f.value)) {
			t.Errorf("first entry lacks %s=%s", f.name, f.value)
		}
	}
}
