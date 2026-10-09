package main

import (
	"database/sql"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

// Ribbons: the original's, from the scoring table.
//
// A ribbon is a scoring event's second tier: every row with a RibbonName
// (44 of the original 65) is earned once per EventsForRibbon occurrences of
// its event in one match -- "Kill 2 enemies within 15 sec. without dying.
// (2 times within a match)" is Dominator with EventsForRibbon 2. They used to
// be twelve invented kill thresholds ("combat_efficiency", ...) that no client
// table knows.
//
// The client (read 2026-10-09):
//   - YA_PlayerGet "Ribbons" entries are parsed by 0x2A73070 into {int32 ID,
//     int32 amt} at player-data +0x118; ID goes through _wtoi, so a name
//     reads as 0. ID is the EYScoringEventID ordinal: the Ribbons screen
//     (CreateRibbonsButtonWidgetsAndPopulateDataList 0xAC2060-0xAC24F7) walks
//     ordinals 0..71, takes each scoring row that has a ribbon name, looks up
//     amt by ID == ordinal, and draws /Game/Generic/UI/ribbons/UI_ribbon_<ordinal>.
//   - Only the player-data parser (0x2A70DA0, YA_PlayerGet) reads them, so a
//     ribbon earned in a match shows from the next login. YA_GetRibbons is
//     never sent by this client (no request in a week of logs).
//
// Who awards them: not the host. Its reward path (0x426DC0, from
// AssignScoringReward's caller 0x5B30D0) applies only the EVENT half of a row
// (+0x94/+0x98/+0x9C; the ribbon half is +0xA4/+0xA8/+0xAC) and builds only
// type-1 (event) achievements -- 0x5A9D30, whose one caller passes 1; ribbons
// are type 2. It counts each event per player (0x431660) and leaves the rest
// to the backend that is gone ("RIBBONS FROM DATA BASE"). So mmogbrain does
// it from the per-event counts the battle-server mod reports ("ev=").
//
// GUESS: floor(count / EventsForRibbon) per match, i.e. a ribbon can be earned
// more than once in a match; the table says nothing either way.

// parseScoringEventCounts reads the mod's "ev" report: "ordinal.count,...".
func parseScoringEventCounts(v string) map[int]int {
	counts := map[int]int{}
	for _, part := range strings.Split(v, ",") {
		a, b, ok := strings.Cut(strings.TrimSpace(part), ".")
		if !ok {
			continue
		}
		id, err1 := strconv.Atoi(a)
		n, err2 := strconv.Atoi(b)
		if err1 != nil || err2 != nil || id < 0 || id >= len(scoringEventIDs) || n <= 0 || n > 100000 {
			continue
		}
		counts[id] += n
	}
	return counts
}

// matchRibbons is the ribbons one match earned, by event name, and the XP
// they carry in mode (the host's scoring mode code). Match Finish (MatchEnd)
// is earned by every reported result -- the mod reports at the end of the
// match -- and is not counted here: its event is kept from the host
// (scoringEndOfMatchEvents) and its XP is already paid (endOfMatchEventXP).
func matchRibbons(counts map[int]int, mode string) (ribbons map[string]int32, xp int32) {
	ribbons = map[string]int32{}
	if r, ok := findOriginalScoringRow("MatchEnd"); ok && r.RibbonName != "" {
		ribbons["MatchEnd"] = 1
	}
	for id, n := range counts {
		name := scoringEventIDs[id]
		if name == "MatchEnd" {
			continue
		}
		r, ok := findOriginalScoringRow(name)
		if !ok || r.RibbonName == "" || r.EventsForRibbon <= 0 {
			continue
		}
		if earned := int32(n / r.EventsForRibbon); earned > 0 {
			ribbons[name] += earned
			xp += earned * int32(scoringValueFor(r.RibbonXP, mode))
		}
	}
	return ribbons, xp
}

// recordRibbons adds a match's ribbons to the player's totals.
func recordRibbons(database *sql.DB, pid string, ribbons map[string]int32) {
	for name, n := range ribbons {
		if _, err := database.Exec(`INSERT INTO player_ribbons(user_id,ribbon_type,count,updated_at) VALUES(?,?,?,datetime('now'))
			ON CONFLICT(user_id,ribbon_type) DO UPDATE SET count=count+excluded.count, updated_at=datetime('now')`, pid, name, n); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{"player": pid, "ribbon": name}).Warn("ribbons: not recorded")
		}
	}
}
