package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/matchmaker"
	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// Season 1's four events take turns, one per period, in table order; the
// running one is what the matchmaker launches.
func TestPvESeasonSchedule(t *testing.T) {
	t.Setenv("DN_PVE_SEASON", "")
	t.Setenv("DN_PVE_EVENT_DAYS", "7")
	week := 7 * 24 * time.Hour
	want := []string{"PVE_S1E1", "PVE_S1E2", "PVE_S1E3", "PVE_S1E4", "PVE_S1E1"}
	for i, id := range want {
		_, events, active := pveSchedule(pveScheduleAnchor.Add(time.Duration(i)*week + time.Hour))
		if active < 0 || events[active].id != id {
			t.Fatalf("week %d: running %v, want %s", i, events, id)
		}
	}
	// S1E4 is the Escort episode.
	_, events, active := pveSchedule(pveScheduleAnchor.Add(3*week + time.Hour))
	if row := loadPVETables().events[events[active].id]; row.GameMode != "YGMT_ESCORT" {
		t.Errorf("S1E4 mode %s", row.GameMode)
	}
	t.Setenv("DN_PVE_SEASON", "off")
	if _, ok := activePvEEvent(); ok {
		t.Error("an event runs with the season switched off")
	}
}

// YA_GetSeasonData names the season and event, with the client's own texts;
// the mode list carries PVE; YA_Connect names the event of a PvE match.
func TestPvESeasonReachesTheClient(t *testing.T) {
	t.Setenv("DN_PVE_SEASON", "")
	ev, ok := activePvEEvent()
	if !ok || ev.HostMode == "" || ev.MapPath == "" {
		t.Fatalf("no running event: %+v", ev)
	}
	payload := buildMmogSeasonDataPayload()
	result := extractNamedMmogObject(t, payload, "result")
	if !bytes.Contains(result, protocol.AppendStringField(nil, "CurrentSeason", "PVE_Season1")) ||
		!bytes.Contains(result, protocol.AppendStringField(nil, "ActiveEvent", ev.ID)) {
		t.Fatal("YA_GetSeasonData does not declare the running season and event")
	}
	events, _, _, _, _ := pveSeasonDataJSON(time.Now().UTC())
	var rows []map[string]any
	if err := json.Unmarshal([]byte(events), &rows); err != nil || len(rows) != 4 {
		t.Fatalf("events blob: %v (%d rows)", err, len(rows))
	}
	if rows[0]["m_name"] != "Incident Management" || len(rows[0]["m_rewardLevels"].([]any)) != 9 {
		t.Errorf("first event: %v", rows[0]["m_name"])
	}

	found := false
	for _, m := range matchmaker.GameModeConfigs() {
		found = found || m.Name == matchmaker.PvEGameMode
	}
	if !found {
		t.Error("the mode list lacks PVE while an event runs")
	}

	connect := appendMmogConnectFields(nil, mmogMatchmakingStatus{gameMode: matchmaker.PvEGameMode, mapName: ev.ID,
		serverIP: "127.0.0.1", serverPort: 7900, matchID: "m"})
	if !bytes.Contains(connect, protocol.AppendStringField(nil, "PVEEvent", ev.ID)) {
		t.Error("YA_Connect lacks the PvE event")
	}
}
