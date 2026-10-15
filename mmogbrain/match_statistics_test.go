package main

import (
	"bytes"
	"net/url"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

func TestMatchStatValuesFromTheModReport(t *testing.T) {
	dom, _ := scoringEventOrdinal("Dominator")
	fs, _ := scoringEventOrdinal("FirstStrike")
	q := url.Values{"score": {"1234"}, "heal": {"5600"}, "ct": {"14.300,5.60,3.40,99.10"}}
	v := matchStatValues(q, map[int]int{dom: 3, fs: 1}, "TDM", nil, 0)
	want := map[string]int64{statScore: 1234, statHealing: 5600, statDoubleKills: 3, statFirstStrikes: 1,
		statTimePrefix + "Destroyer": 360, statTimePrefix + "ArtilleryCruiser": 40}
	for k, w := range want {
		if v[k] != w {
			t.Errorf("%s = %d, want %d", k, v[k], w)
		}
	}
	if len(v) != len(want) {
		t.Errorf("unexpected stats %v", v)
	}
}

// Without the newer mod: score from the event counts, time split over the
// ships flown.
func TestMatchStatValuesFallbacks(t *testing.T) {
	if len(originalScoringTable()) == 0 {
		t.Skip("original scoring table not available")
	}
	dom, _ := scoringEventOrdinal("Dominator")
	v := matchStatValues(url.Values{}, map[int]int{dom: 2}, "TDM",
		[]string{"Default__VH_DreadnoughtLight_Nimbus_HeroLoadout_BP_C", "Default__VH_SniperLight_T2_PrecastLoadout_BP_C"}, 600)
	r, _ := findOriginalScoringRow("Dominator")
	if want := int64(2 * scoringValueFor(r.EventScore, "TDM")); v[statScore] != want {
		t.Errorf("score %d, want %d", v[statScore], want)
	}
	if v[statTimePrefix+"Dreadnought"] != 300 || v[statTimePrefix+"ArtilleryCruiser"] != 300 {
		t.Errorf("time split %v", v)
	}
	if _, ok := v[statHealing]; ok {
		t.Error("healing has no fallback")
	}
}

func TestPlayerStatisticsCarriesMatchStats(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	a := "0123456789abcdef0123456789abcdef"
	recordMatchStats(database, "m1", a, map[string]int64{statScore: 1000, statDoubleKills: 2, statTimePrefix + "Corvette": 90})
	recordMatchStats(database, "m2", a, map[string]int64{statScore: 500, statHealing: 70})
	recordMatchStats(database, "m2", a, map[string]int64{statScore: 999}) // same match again: ignored
	reply := buildMmogPlayerStatisticsPayload(a, statisticsRequest(t, a))
	stats := extractNamedMmogArray(t, reply, "stats")
	for _, f := range []struct{ name, value string }{{"ScoreSum", "1500"}, {"HealPoints", "70"},
		{"DoubleKillNum", "2"}, {"FirstStrikeNum", "0"}, {"CorvetteTimeSec", "90"}} {
		if !bytes.Contains(stats, protocol.AppendStringField(nil, f.name, f.value)) {
			t.Errorf("stats lack %s=%s", f.name, f.value)
		}
	}
}
