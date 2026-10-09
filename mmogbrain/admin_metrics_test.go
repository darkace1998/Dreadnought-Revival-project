package main

import (
	"testing"
	"time"
)

// Metrics aggregate per UTC day, from both timestamp formats in use
// (RFC 3339 in matches, datetime('now') elsewhere).
func TestAdminMetricsDaily(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for _, q := range []string{
		`INSERT INTO matches(id,battle_match_id,game_mode,map,status,created_at,started_at,ended_at,fleet_type) VALUES
			('ma','a','TDM','m','ended','2026-10-09T01:00:00Z','2026-10-09T01:00:00Z','2026-10-09T01:12:00Z',1),
			('mb','b','TDM','m','ended','2026-10-09T03:00:00Z','2026-10-09T03:00:00Z','2026-10-09T03:08:00Z',2),
			('mc','c','Onslaught','m','ended','2026-10-08T22:00:00Z','2026-10-08T22:00:00Z','2026-10-08T22:20:00Z',1),
			('mold','old','TDM','m','ended','2026-09-01T22:00:00Z',NULL,NULL,1)`,
		`INSERT INTO battle_results(match_id,user_id,outcome,kills,credits,created_at) VALUES
			('a','p1','win',3,100,'2026-10-09 01:12:00'),('a','p2','loss',1,50,'2026-10-09 01:12:00'),
			('b','p1','win',2,70,'2026-10-09 03:08:00'),('c','p1','loss',0,10,'2026-10-08 22:20:00'),
			('old','p3','win',0,0,'2026-09-01 22:30:00')`,
		`INSERT INTO admin_metrics_samples(ts,online,in_match) VALUES('2026-10-09 01:05:00',4,2),('2026-10-09 02:05:00',6,0)`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	m := adminCollectMetrics(database, 2, now)
	daily := m["daily"].([]adminMetricsDay)
	if len(daily) != 2 || daily[0].Day != "2026-10-08" || daily[1].Day != "2026-10-09" {
		t.Fatalf("days: %+v", daily)
	}
	if d := daily[1]; d.Matches != 2 || d.Players != 2 || d.Results != 3 || d.Credits != 220 || d.Kills != 6 || d.PeakOnline != 6 {
		t.Errorf("2026-10-09: %+v", d)
	}
	if d := daily[0]; d.Matches != 1 || d.Players != 1 {
		t.Errorf("2026-10-08: %+v", d)
	}
	modes := m["modes"].([]adminMetricsMode)
	if len(modes) != 2 || modes[0].Mode != "TDM" || modes[0].Matches != 2 || modes[0].AvgMinutes != 10 || modes[0].AvgPlayers != 1.5 {
		t.Errorf("modes: %+v", modes)
	}
	if h := m["hours_utc"].([]int); h[1] != 1 || h[3] != 1 || h[22] != 1 {
		t.Errorf("hours: %v", h)
	}
	if f := m["fleets"].(map[string]int); f["Recruit"] != 2 || f["Veteran"] != 1 {
		t.Errorf("fleets: %v", f)
	}
	if s := m["samples"].([]adminMetricsSample); len(s) != 2 {
		t.Errorf("samples: %+v", s)
	}
	if top := m["top_players"].([]adminMetricsTopPlayer); len(top) != 2 || top[0].PID != "p1" || top[0].Matches != 3 || top[0].Wins != 2 {
		t.Errorf("top: %+v", top)
	}
	if m["returning_players"].(int) != 0 {
		t.Errorf("returning: %v (p3 played only before the window)", m["returning_players"])
	}
}
