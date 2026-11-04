package matchmaker

import "testing"

// DN_ESCALATION_QUEUE hands one client queue to the Escalation mode: the
// control plane is asked for "Escalation" (a mod-made TDM) on a TDM map, the
// match keeps its queued mode's record, and other queues are untouched.
func TestEscalationQueue(t *testing.T) {
	t.Setenv("DN_ESCALATION_QUEUE", "BC")
	database := pvpTestDB(t)
	m, requests := fleetTierMatchmaker(t, database, 1)
	if _, err := database.Exec(
		`INSERT INTO queue_entries(id,user_id,game_mode,tier_min,status,queued_at,fleet_type) VALUES
		 ('q1','alice','BC',1,'waiting','2026-11-04T10:00:00Z',1),
		 ('q2','bob','TDM',1,'waiting','2026-11-04T10:00:01Z',1)`); err != nil {
		t.Fatal(err)
	}
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 2 {
		t.Fatalf("%d battle servers requested, want 2", len(*requests))
	}
	modes := map[string]bool{}
	for _, r := range *requests {
		modes[r["game_mode"].(string)] = true
		if r["game_mode"] == "Escalation" {
			tdm := false
			for _, gm := range mapsByGameMode["TDM"] {
				tdm = tdm || gm.Name == r["map"]
			}
			if !tdm {
				t.Errorf("Escalation on %v, not a TDM map", r["map"])
			}
		}
	}
	if !modes["Escalation"] || !modes["TDM"] {
		t.Fatalf("control plane asked for %v, want one Escalation (Proving Grounds) and one TDM", modes)
	}
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM matches WHERE game_mode IN ('BC','TDM')`).Scan(&n); err != nil || n != 2 {
		t.Errorf("matches recorded under their queued modes: %d of 2 (%v)", n, err)
	}
}

func TestEscalationQueueOffByDefault(t *testing.T) {
	t.Setenv("DN_ESCALATION_QUEUE", "")
	if IsEscalationQueue("BC") || IsEscalationQueue("") {
		t.Fatal("no queue runs Escalation unless DN_ESCALATION_QUEUE names it")
	}
}
