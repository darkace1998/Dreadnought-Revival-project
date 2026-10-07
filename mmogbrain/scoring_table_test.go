package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestScoringEventOrdinalsCoverTheEnum(t *testing.T) {
	if len(scoringEventIDs) != 75 {
		t.Fatalf("%d ordinals, want 75 (Invalid is 75)", len(scoringEventIDs))
	}
	seen := map[string]bool{}
	for _, e := range scoringEventIDs {
		seen[e] = true
	}
	for _, e := range knownScoringEvents {
		if !seen[e] {
			t.Errorf("%s has no ordinal", e)
		}
	}
	for name, want := range map[string]int{"CaptainKill_SameTier": 0, "Assist": 10, "Winner": 28, "MVP": 42, "FinalBlow": 51, "PODTDMPickup": 74} {
		if got, _ := scoringEventOrdinal(name); got != want {
			t.Errorf("%s = %d, want %d (registration order 0x2A9DE0E)", name, got, want)
		}
	}
}

func TestScoringDocumentCarriesThePoints(t *testing.T) {
	t.Setenv("DN_SCORING_ORIGINAL", "0") // the old operator table
	doc := string(scoringDocument())
	rows := 0
	for _, e := range pvpScoringTable {
		if e.Points > 0 && !scoringEndOfMatchEvents[e.Event] {
			rows++
		}
	}
	if n := strings.Count(doc, "EventScore"); n != rows {
		t.Errorf("%d EventScore fields, want %d (one per scoring event)", n, rows)
	}
	if countWireStringField(doc, "EventScore", "200") != 1 || countWireStringField(doc, "EventName", "CaptainKill_HigherTier1") != 1 {
		t.Error("higher-tier kill is not in the document at 200")
	}
	if countWireStringField(doc, "EventName", "Winner") != 0 {
		t.Error("Winner is served; end-of-match events overflowed the host's stack")
	}
	if !strings.Contains(doc, "ScoringTable") || !strings.Contains(doc, "ScoringParamsTable") {
		t.Error("document lacks ScoringTable or ScoringParamsTable")
	}
	if !strings.HasSuffix(doc, "\x00\x0e\x00\x00\x00\x00") {
		t.Error("document does not end with the root terminator; the parser would resolve nothing inside it")
	}
}

func TestBattleScoringIsLoopbackOnly(t *testing.T) {
	for addr, want := range map[string]int{"127.0.0.1:5000": http.StatusOK, "10.0.0.5:5000": http.StatusForbidden} {
		req := httptest.NewRequest(http.MethodGet, "/battle/scoring", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		battleScoringHandler(rec, req)
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", addr, rec.Code, want)
		}
	}
}

// The original table (data/scoring/ScoringTable.json, from the operator's
// datamine) is what the host gets: every row an EYScoringEventID, the
// per-mode strings verbatim, and still no end-of-match event.
func TestScoringDocumentServesTheOriginalTable(t *testing.T) {
	rows := originalScoringTable()
	if len(rows) < 50 {
		t.Fatalf("%d original rows; data/scoring/ScoringTable.json missing?", len(rows))
	}
	known := map[string]bool{}
	for _, e := range knownScoringEvents {
		known[e] = true
	}
	for _, r := range rows {
		if !known[r.Enum] {
			t.Errorf("%s maps to %q, not an EYScoringEventID", r.EventName, r.Enum)
		}
	}
	doc := string(scoringDocument())
	for _, want := range [][2]string{
		{"EventName", "CaptainKill_SameTier"},
		{"EventScore", "TDM(65) : TM (10) : TE(110): IVN(30) : POD_TDM(70) : BC(65) : TURBO_TDM(65)"},
		{"EventName", "AITargetL"}, // the table's FighterKill
		{"EventScore", "IVN(8)"},
	} {
		if countWireStringField(doc, want[0], want[1]) == 0 {
			t.Errorf("document lacks %s %q", want[0], want[1])
		}
	}
	for _, eom := range []string{"Winner", "MVP", "ManOfTheDay", "MatchEnd", "PlayTime"} {
		if countWireStringField(doc, "EventName", eom) != 0 {
			t.Errorf("%s is served; end-of-match events stay with mmogbrain", eom)
		}
	}
	if !strings.Contains(doc, "TER(250)") {
		t.Error("Conquest's 1.12.0 values (TerritoryCapturedPoint TER(250)) are missing")
	}
}

// Read the way the host does (0x42DF40): spaces dropped, then "<MODE>(n)".
func TestScoringValueFor(t *testing.T) {
	const v = "TDM(65) : TM (10) : TE(110): IVN(30) : POD_TDM(70)"
	for mode, want := range map[string]int{"TDM": 65, "TM": 10, "TE": 110, "IVN": 30, "POD_TDM": 70, "TER": 0} {
		if got := scoringValueFor(v, mode); got != want {
			t.Errorf("%s = %d, want %d", mode, got, want)
		}
	}
	if scoringValueFor("T(30)", "T") != 30 {
		t.Error("PlayTime's T(30) not read")
	}
}
