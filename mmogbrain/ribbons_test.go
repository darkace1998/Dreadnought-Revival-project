package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
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
