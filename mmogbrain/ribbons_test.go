package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// Ribbons come from the original scoring table: one per EventsForRibbon
// occurrences of the event in a match, plus Match Finish for every match.
func TestMatchRibbonsFromEventCounts(t *testing.T) {
	dominator, _ := scoringEventOrdinal("Dominator") // EventsForRibbon 2
	assist, _ := scoringEventOrdinal("Assist")       // EventsForRibbon 10
	kill, _ := scoringEventOrdinal("CaptainKill_SameTier")
	counts := parseScoringEventCounts(strconv.Itoa(dominator) + ".5," + strconv.Itoa(assist) + ".9," +
		strconv.Itoa(kill) + ".12,999.4,bad")
	ribbons, xp := matchRibbons(counts, "TDM")
	if ribbons["Dominator"] != 2 {
		t.Errorf("5 dominations earned %d Domination ribbons, want 2", ribbons["Dominator"])
	}
	if _, ok := ribbons["Assist"]; ok {
		t.Error("9 assists earned a Combat Assistance ribbon (needs 10)")
	}
	if _, ok := ribbons["CaptainKill_SameTier"]; ok {
		t.Error("a ribbon for an event that has none")
	}
	if ribbons["MatchEnd"] != 1 {
		t.Errorf("Match Finish %d, want 1", ribbons["MatchEnd"])
	}
	if xp != 2*50 { // Dominator RibbonXP TDM(50); MatchEnd's XP is paid elsewhere
		t.Errorf("ribbon XP %d, want 100", xp)
	}
}

// The client reads ID as an int (_wtoi): the EYScoringEventID ordinal. A name
// read as 0, so every ribbon was "ribbon 0".
func TestRibbonEntriesCarryTheEventOrdinal(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "0123456789abcdef0123456789abcdef"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	recordRibbons(database, pid, map[string]int32{"Dominator": 2, "MatchEnd": 1})
	recordRibbons(database, pid, map[string]int32{"Dominator": 1})
	if _, err := database.Exec(`INSERT INTO player_ribbons(user_id,ribbon_type,count) VALUES(?,?,?)`, pid, "first_blood", 4); err != nil {
		t.Fatal(err)
	}
	payload := buildMmogRibbonsPayload(pid)
	dominator, _ := scoringEventOrdinal("Dominator")
	want := append(protocol.AppendStringField(nil, "ID", strconv.Itoa(dominator)), protocol.AppendStringField(nil, "amt", "3")...)
	if !bytes.Contains(payload, want) {
		t.Errorf("no Dominator entry with amt 3 in %x", payload)
	}
	if bytes.Contains(payload, []byte("first_blood")) || bytes.Count(payload, []byte("amt")) != 2 {
		t.Error("an invented ribbon was sent")
	}
}

// The battle server's event counts reach the player's ribbons once per match.
func TestBattleResultRecordsRibbonsOnce(t *testing.T) {
	t.Setenv("DN_DAILY_CONTRACTS", "0")
	database := useTempMmogPlayerStateDB(t)
	const pid = "0123456789abcdef0123456789abcdef"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatal(err)
	}
	rampage, _ := scoringEventOrdinal("Rampage") // EventsForRibbon 2
	url := "/battle/result?match=R1&pid=" + pid + "&team=1&final=1&kills=10&ev=" + strconv.Itoa(rampage) + ".2"
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.RemoteAddr = "127.0.0.1:5000"
		battleResultHandler(httptest.NewRecorder(), req)
	}
	var n int32
	_ = database.QueryRow(`SELECT count FROM player_ribbons WHERE user_id=? AND ribbon_type='Rampage'`, pid).Scan(&n)
	if n != 1 {
		t.Errorf("Rampage ribbons %d after one match reported twice, want 1", n)
	}
}

// MVP and Man Of The Day are reported by battle-server-mod under these
// ordinals (SCORING_EVENT_MVP / _MAN_OF_THE_DAY in dn_host_loadout.cpp); the
// two lists must agree.
func TestEndOfMatchRibbonOrdinalsMatchTheMod(t *testing.T) {
	for name, want := range map[string]int{"ManOfTheDay": 41, "MVP": 42} {
		if got, ok := scoringEventOrdinal(name); !ok || got != want {
			t.Errorf("%s ordinal %d, the mod uses %d", name, got, want)
		}
	}
}

func TestRibbonCreditsArePaidPerMode(t *testing.T) {
	if len(originalScoringTable()) == 0 {
		t.Skip("original scoring table not available")
	}
	mvp, _ := scoringEventOrdinal("MVP")
	motd, _ := scoringEventOrdinal("ManOfTheDay")
	ribbons, _ := matchRibbons(map[int]int{mvp: 1, motd: 1}, "TDM")
	if ribbons["MVP"] != 1 || ribbons["ManOfTheDay"] != 1 {
		t.Fatalf("ribbons %v", ribbons)
	}
	r, _ := findOriginalScoringRow("MVP")
	want := int32(scoringValueFor(r.RibbonCredits, "TDM"))
	if m, _ := findOriginalScoringRow("MatchEnd"); m.RibbonCredits != "" {
		want += int32(scoringValueFor(m.RibbonCredits, "TDM"))
	}
	if r2, _ := findOriginalScoringRow("ManOfTheDay"); r2.RibbonCredits != "" {
		want += int32(scoringValueFor(r2.RibbonCredits, "TDM"))
	}
	if got := ribbonCredits(ribbons, "TDM"); got != want || want <= 0 {
		t.Errorf("ribbon credits %d, want %d (> 0)", got, want)
	}
	// Paid on top of the formula's credits.
	base, _ := currentBattleRewards().forOutcome("win", 0, 1)
	rw := currentBattleRewards()
	rw.ribbonCredits = want
	if paid, _ := rw.forOutcome("win", 0, 1); paid <= base {
		t.Errorf("credits %d with ribbon credits, %d without", paid, base)
	}
}

func TestBattleRibbonsTableForTheHost(t *testing.T) {
	if len(originalScoringTable()) == 0 {
		t.Skip("original scoring table not available")
	}
	req := httptest.NewRequest(http.MethodGet, "/battle/ribbons?mode=TDM", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	battleRibbonsHandler(rec, req)
	body := rec.Body.String()
	dom, _ := scoringEventOrdinal("Dominator")
	r, _ := findOriginalScoringRow("Dominator")
	want := fmt.Sprintf("%d %d %d %d %d\n", dom, r.EventsForRibbon, scoringValueFor(r.RibbonScore, "TDM"),
		scoringValueFor(r.RibbonXP, "TDM"), scoringValueFor(r.RibbonCredits, "TDM"))
	if !strings.Contains(body, want) {
		t.Errorf("no Dominator line %q in\n%s", want, body)
	}
	if n := strings.Count(body, "\n"); n < 40 {
		t.Errorf("%d ribbon lines, want the original 44", n)
	}
	req.RemoteAddr = "10.0.0.5:5555"
	rec = httptest.NewRecorder()
	battleRibbonsHandler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-loopback got %d", rec.Code)
	}
}
