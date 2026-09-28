package main

import "testing"

// Every EYScoringEventID in the client exe, from its UTF-16 enum strings
// (YSEID_*, minus Invalid and Max): 75 events.
var knownScoringEvents = []string{
	"AITargetH", "AITargetHAssist", "AITargetL", "AITargetLAssist", "AITargetM", "AITargetMAssist",
	"Assist", "Avenger", "BloodThurst", "BossCarrierCaptainKill", "BossCarrierCriticalAssist",
	"CaptainKill_HigherTier1", "CaptainKill_HigherTier2", "CaptainKill_LowerTier1", "CaptainKill_LowerTier2",
	"CaptainKill_SameTier", "Cheat", "Comeback", "Concentrator",
	"CriticalAssist_HigherTier1", "CriticalAssist_HigherTier2", "CriticalAssist_LowerTier1",
	"CriticalAssist_LowerTier2", "CriticalAssist_SameTier", "CutTheLifeline", "Desolator", "Dominator",
	"EagleEye", "Fearless", "FinalBlow", "Finisher", "FirstStrike", "GoalOriented", "GoodSport", "Guardian",
	"HavocXPBossPickup", "HavocXPKill", "HavocXPPickup", "IncomingRawDamage", "IncomingRawDamageThisLife",
	"IncomingRawDamageThisLife2", "Intervention", "MVP", "ManOfTheDay", "Marksman", "MatchEnd", "Mayham",
	"Ninja", "PODTDMPickup", "PVEQuickKill", "Parry", "Payback", "PlayTime", "Predator", "Rampage",
	"RecentIncomingRawDamageXCloseEnemies", "Repair", "Saboteur", "Saviour", "SecondHand", "Supporter",
	"Suppression", "TerritoryCapturedPoint", "TerritoryCapturingPoint", "TerritoryCleared",
	"TerritoryContribution", "TerritoryKill", "TerritoryProtectCP", "Vanguard", "Warfare", "Winner",
	"droneCaptainKill", "droneCriticalAssist", "transportCaptainKill", "transportCriticalAssist",
}

func TestPvPScoringTableNamesRealEvents(t *testing.T) {
	if len(knownScoringEvents) != 75 {
		t.Fatalf("%d known events, want 75", len(knownScoringEvents))
	}
	known := map[string]bool{}
	for _, e := range knownScoringEvents {
		known[e] = true
	}
	seen := map[string]bool{}
	for _, e := range pvpScoringTable {
		if !known[e.Event] {
			t.Errorf("%q is not an EYScoringEventID in the client", e.Event)
		}
		if seen[e.Event] {
			t.Errorf("%q listed twice", e.Event)
		}
		seen[e.Event] = true
		if e.Points < 0 {
			t.Errorf("%q has negative points", e.Event)
		}
	}
}
