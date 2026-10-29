package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
	"github.com/sirupsen/logrus"
)

// The ORIGINAL scoring table (issue #67), from the operator's datamine
// (2026-10-07): data/scoring/ScoringTable.json, written by
// scripts/gen-scoring-table.py from the workbook's "old Scoring 1.11.1" and
// "old Scoring 1.12.0" sheets (1.12.0 for Conquest, 1.11.1 for the rest, as
// the live game had it). The sheets' columns are the fields the battle
// server's row parser reads (0x2A7A030), and their per-mode strings
// ("TDM(65) : TE(110) : IVN(30)") the format of its value parser (0x42DF40:
// spaces dropped, then "<MODE>(n)"). The mode codes are the host's own: TDM,
// TE, IVN (Onslaught), POD_TDM, BC, TM, TER (seen in host logs; TURBO_TDM is
// GUESSED from TDM by the generator).
//
// Without the file the old operator-chosen table (pvpScoringTable) is served.

type originalScoringRow struct {
	EventName       string `json:"EventName"` // the table's own name ("FighterKill")
	Enum            string `json:"Enum"`      // its EYScoringEventID ("AITargetL")
	EventVisibility int    `json:"EventVisibility"`
	NameForPlayer   string `json:"NameForPlayer"`
	EventScore      string `json:"EventScore"`
	EventXP         string `json:"EventXP"`
	EventCredits    string `json:"EventCredits"`
	Parameters      string `json:"Parameters"`
	EventsForRibbon int    `json:"EventsForRibbon"`
	RibbonName      string `json:"RibbonName"`
	RibbonDesc      string `json:"RibbonDesc"`
	RibbonScore     string `json:"RibbonScore"`
	RibbonXP        string `json:"RibbonXP"`
	RibbonCredits   string `json:"RibbonCredits"`
	SummaryCategory string `json:"SummaryCategory"`
	GameModes       string `json:"GameModes"`
}

var (
	originalScoringOnce sync.Once
	originalScoringRows []originalScoringRow
)

// originalScoringTable is the original table, or nil when the file is missing
// or DN_SCORING_ORIGINAL=0.
func originalScoringTable() []originalScoringRow {
	if os.Getenv("DN_SCORING_ORIGINAL") == "0" {
		return nil
	}
	originalScoringOnce.Do(func() {
		path := filepath.Join(dreadconfig.DataDir(), "scoring", "ScoringTable.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			logrus.WithError(err).Warn("scoring: original table missing; serving the old operator table")
			return
		}
		var doc struct {
			Rows []originalScoringRow `json:"rows"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			logrus.WithError(err).Warn("scoring: original table unreadable; serving the old operator table")
			return
		}
		originalScoringRows = append(doc.Rows, laterScoringRows(doc.Rows)...)
	})
	return originalScoringRows
}

// laterScoringRows are the rows the game gained after the 1.11/1.12 sheets
// and that the datamine therefore lacks, each added only while the table has
// no row of its own for that event.
//
// PODTDMPickup (Ceres Awakens tech cache, ordinal 74): Grey Box's "Ceres
// Reawakens" notes (2021-04-23) "added score, ship xp, and credits for picking
// up buff pods and additional amounts for every 3 pods picked up". The client
// fires the event itself: AlliedBuffPod_Parent_Pickup_BP's
// AwardPickedUpScoringEvent calls FireScoringEventForPlayer (thunk 0x74C560 ->
// PC vtable +0xFD0 = 0x5782B0 -> the game mode's scoring manager) for the
// picking player, with PickedUpScoringEventType YSEID_PODTDMPickup on six of
// the seven pods (the Armor pod keeps the parent's Invalid and pays nothing).
// Without a row the host found no reward: no PODTDMPickup was ever counted
// in 41 Space02 matches (2026-10-08..10-29). The "every 3 pods" bonus rides on
// the ribbon half (EventsForRibbon 3), which mmogbrain pays per ribbon; the
// client has no ribbon icon for 74 and its Ribbons screen stops at 71, so it
// shows only in the match. GUESS: every value and both names -- the notes give
// none and the client's text tables have no entry for this event. The amounts
// copy the table's own POD_TDM tiers: a bonus event (Dominator/Payback 35
// score, 40 XP), the kill-- credits (6), and the common ribbon (50 score,
// 55 XP) with Repair's ribbon credits (10).
func laterScoringRows(have []originalScoringRow) []originalScoringRow {
	later := []originalScoringRow{{
		EventName:       "PODTDMPickup",
		Enum:            "PODTDMPickup",
		EventVisibility: 1,
		NameForPlayer:   "TECH CACHE",
		EventScore:      "POD_TDM(35)",
		EventXP:         "POD_TDM(40)",
		EventCredits:    "POD_TDM(6)",
		EventsForRibbon: 3,
		RibbonName:      "Tech Cache Hunter",
		RibbonDesc:      "Pick up 3 Tech Caches.",
		RibbonScore:     "POD_TDM(50)",
		RibbonXP:        "POD_TDM(55)",
		RibbonCredits:   "POD_TDM(10)",
		SummaryCategory: "Extra",
		GameModes:       "POD_TDM",
	}}
	var out []originalScoringRow
	for _, r := range later {
		found := false
		for _, h := range have {
			if h.Enum == r.Enum {
				found = true
				break
			}
		}
		if !found {
			out = append(out, r)
		}
	}
	return out
}

var scoringModeValue = regexp.MustCompile(`([A-Za-z_]+)\(([-\d.]+)\)`)

// scoringValueFor reads one mode's number out of a per-mode string the way the
// host does (0x42DF40): spaces dropped, then "<MODE>(n)". 0 when absent.
func scoringValueFor(field, mode string) int {
	for _, m := range scoringModeValue.FindAllStringSubmatch(strings.ReplaceAll(field, " ", ""), -1) {
		if m[1] == mode {
			v, _ := strconv.ParseFloat(m[2], 64)
			return int(v)
		}
	}
	return 0
}

func findOriginalScoringRow(event string) (originalScoringRow, bool) {
	for _, r := range originalScoringTable() {
		if r.EventName == event || r.Enum == event {
			return r, true
		}
	}
	return originalScoringRow{}, false
}

// scoringModeCode is the host's scoring code for a mode as mmogbrain records
// it (matches.game_mode).
func scoringModeCode(gameMode string) string {
	switch strings.ToLower(gameMode) {
	case "onslaught", "ivn":
		return "IVN"
	case "podtdm", "pod_tdm":
		return "POD_TDM"
	case "bc", "bootcamp":
		return "BC"
	case "ter", "territory", "conquest":
		return "TER"
	case "turbotdm", "turbo_tdm":
		return "TURBO_TDM"
	case "te":
		return "TE"
	case "tm":
		return "TM"
	}
	return "TDM"
}

// endOfMatchEventXP is the XP of the two original end-of-match awards the host
// is NOT given (scoringEndOfMatchEvents keeps them out of its table): the
// "Match Finish" ribbon (MatchEnd RibbonXP) and PlayTime (EventXP every
// Parameters T(30) seconds). GUESS: the play time is the match's, from its
// server becoming ready to now, not the player's own time in it.
func endOfMatchEventXP(database *sql.DB, battleMatchID string) (xp int32, mode string) {
	gameMode, readyAt := matchModeAndStart(database, battleMatchID)
	mode = scoringModeCode(gameMode)
	if r, ok := findOriginalScoringRow("MatchEnd"); ok {
		xp += int32(scoringValueFor(r.RibbonXP, mode))
	}
	if r, ok := findOriginalScoringRow("PlayTime"); ok && !readyAt.IsZero() {
		every := scoringValueFor(r.Parameters, "T")
		if every <= 0 {
			every = 30
		}
		secs := int(time.Since(readyAt).Seconds())
		if secs > 3600 {
			secs = 3600
		}
		if secs > 0 {
			xp += int32(scoringValueFor(r.EventXP, mode) * (secs / every))
		}
	}
	return xp, mode
}

func matchModeAndStart(database *sql.DB, battleMatchID string) (string, time.Time) {
	if database == nil {
		return "", time.Time{}
	}
	if i := strings.LastIndex(battleMatchID, "-r"); i > 0 {
		if _, err := strconv.Atoi(battleMatchID[i+2:]); err == nil {
			battleMatchID = battleMatchID[:i]
		}
	}
	var mode string
	var ready, created sql.NullString
	if err := database.QueryRow(`SELECT game_mode, server_ready_at, created_at FROM matches WHERE battle_match_id=? AND battle_match_id!=''`,
		battleMatchID).Scan(&mode, &ready, &created); err != nil {
		return "", time.Time{}
	}
	for _, s := range []sql.NullString{ready, created} {
		if s.Valid {
			if t, err := time.Parse(time.RFC3339, s.String); err == nil {
				return mode, t
			}
		}
	}
	return mode, time.Time{}
}
