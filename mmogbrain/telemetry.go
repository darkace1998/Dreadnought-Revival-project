package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

// Client telemetry.
//
// The client reports what it does and what it showed the player through
// fire-and-forget requests: the name of each is referenced only by its sender
// in the exe, never by the reply dispatcher (0x142A236C2-0x142A31A32), so the
// reply is an acknowledgement nobody reads. The DATA is the point -- it is the
// client's own account of events the server otherwise only sees from its side:
//
//   - YA_GameModeEvent: GameMode, Event ("Started_Match"/"Completed_Match").
//   - YA_AnalyticsInvestEvent (sender 0x142A44830): itemID, shipXp, freeXp,
//     remainingShipXp, remainingFreeXp -- research as the client applied it.
//   - YA_AnalyticsReceiveCreditsEvent (0x142A44F20) / ...ReceiveXPEvent
//     (0x142A45580): BattleID (= matches.battle_match_id) and the reward
//     breakdown the end-of-match screen showed, {Amount, Source} entries (XP
//     also per ShipID).
//   - YA_AnalyticsEvent: category/context plus free fields, e.g.
//     client_statistics_hardware (GPU, CPU, OS, memory).
//   - YA_AnalyticsBegin/Update/EndTransaction: timed client operations
//     (category client_loading_time, ...), keyed by transactionId.
//   - tutorial, onboarding movie and button events.
//
// All of it was answered with a bare "ok" or, for the four above, "Unknown
// command" and dropped (2026-10-03). Now every call is stored in
// client_telemetry with all its fields, and the two that state something the
// server also knows are checked against it:
//
//   - research: the client's remainingFreeXp against the server's free XP
//     after the unlock (telemetryInvestCheck);
//   - rewards: the dashboard shows the client's breakdown beside what
//     battle_results says was paid for the same BattleID.
//
// Rows older than telemetryRetention are pruned.

const telemetryRetention = 30 * 24 * time.Hour

// telemetryRequests are the requests recorded as telemetry. Each is still
// acknowledged by the dispatcher exactly as before.
var telemetryRequests = map[string]bool{
	"YA_AnalyticsEvent":                true,
	"YA_AnalyticsBeginTransaction":     true,
	"YA_AnalyticsUpdateTransaction":    true,
	"YA_AnalyticsEndTransaction":       true,
	"YA_AnalyticsTutorialEvent":        true,
	"YA_AnalyticsTutorialSummaryEvent": true,
	"YA_AnalyticsOnboardingMovie":      true,
	"YA_AnalyticsButtonClicked":        true,
	"YA_AnalyticsInvestEvent":          true,
	"YA_AnalyticsReceiveXPEvent":       true,
	"YA_AnalyticsReceiveCreditsEvent":  true,
	"YA_GameModeEvent":                 true,
}

// telemetryField is one field as sent, in order.
type telemetryField struct {
	Name  string `json:"n"`
	Value any    `json:"v"`
}

func telemetryFields(payload []byte) []telemetryField {
	var out []telemetryField
	for _, s := range protocol.Scalars(payload) {
		if s.Name == "RT" {
			continue
		}
		if s.IsStr {
			out = append(out, telemetryField{s.Name, s.Str})
		} else {
			out = append(out, telemetryField{s.Name, s.Num})
		}
	}
	return out
}

func telemetryStr(fields []telemetryField, name string) string {
	for _, f := range fields {
		if strings.EqualFold(f.Name, name) {
			if s, ok := f.Value.(string); ok {
				return s
			}
			return fmt.Sprint(f.Value)
		}
	}
	return ""
}

func telemetryNum(fields []telemetryField, name string) (int64, bool) {
	for _, f := range fields {
		if strings.EqualFold(f.Name, name) {
			switch v := f.Value.(type) {
			case int64:
				return v, true
			case string:
				n, err := strconv.ParseInt(v, 10, 64)
				return n, err == nil
			}
		}
	}
	return 0, false
}

// telemetryAmounts lists the {Amount, Source} entries of a reward event, with
// the ShipID that precedes an entry when there is one (XP per ship).
func telemetryAmounts(fields []telemetryField) []string {
	var out []string
	ship := ""
	amount := ""
	for _, f := range fields {
		switch f.Name {
		case "ShipID":
			ship = fmt.Sprint(f.Value)
		case "Amount":
			amount = fmt.Sprint(f.Value)
		case "Source":
			entry := fmt.Sprintf("%v %s", f.Value, amount)
			if ship != "" {
				entry = "ship " + ship + ": " + entry
			}
			out = append(out, entry)
			amount = ""
		}
	}
	return out
}

// telemetrySummary is the one line shown for an event.
func telemetrySummary(rt string, fields []telemetryField) string {
	switch rt {
	case "YA_GameModeEvent":
		return telemetryStr(fields, "GameMode") + " " + telemetryStr(fields, "Event")
	case "YA_AnalyticsInvestEvent":
		return fmt.Sprintf("item %s: ship XP %s, free XP %s; left ship XP %s, free XP %s",
			telemetryStr(fields, "itemID"), telemetryStr(fields, "shipXp"), telemetryStr(fields, "freeXp"),
			telemetryStr(fields, "remainingShipXp"), telemetryStr(fields, "remainingFreeXp"))
	case "YA_AnalyticsReceiveCreditsEvent", "YA_AnalyticsReceiveXPEvent":
		return strings.Join(telemetryAmounts(fields), "; ")
	case "YA_AnalyticsBeginTransaction", "YA_AnalyticsUpdateTransaction", "YA_AnalyticsEndTransaction":
		return strings.TrimSpace(telemetryStr(fields, "category") + " " + telemetryStr(fields, "transactionId"))
	case "YA_AnalyticsEvent":
		return telemetryStr(fields, "category")
	}
	var parts []string
	for _, f := range fields {
		if len(parts) == 6 {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, fmt.Sprintf("%s=%v", f.Name, f.Value))
	}
	return strings.Join(parts, " ")
}

var (
	telemetryPruneMu   sync.Mutex
	telemetryLastPrune time.Time
)

// recordClientTelemetry stores one telemetry request. Called by the
// dispatcher, after any persistence the request triggers (the research a
// YA_AnalyticsInvestEvent follows was committed by its own YA_UnlockItem).
func recordClientTelemetry(playerPID, rt string, payload []byte) {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return
	}
	pid := normalizedPlayerStatePID(playerPID)
	fields := telemetryFields(payload)
	summary := telemetrySummary(rt, fields)
	if rt == "YA_AnalyticsInvestEvent" {
		if note := telemetryInvestCheck(pid, fields); note != "" {
			summary += " -- " + note
		}
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		raw = []byte("[]")
	}
	if _, err := database.Exec(`INSERT INTO client_telemetry(user_id,rt,battle_id,summary,fields) VALUES(?,?,?,?,?)`,
		pid, rt, telemetryStr(fields, "BattleID"), summary, string(raw)); err != nil {
		logrus.WithError(err).WithField("rt", rt).Warn("telemetry: not stored")
	}
	logrus.WithFields(logrus.Fields{"player": pid, "rt": rt}).Debug("telemetry: " + summary)

	telemetryPruneMu.Lock()
	due := time.Since(telemetryLastPrune) > time.Hour
	if due {
		telemetryLastPrune = time.Now()
	}
	telemetryPruneMu.Unlock()
	if due {
		cutoff := time.Now().UTC().Add(-telemetryRetention).Format("2006-01-02 15:04:05")
		_, _ = database.Exec(`DELETE FROM client_telemetry WHERE created_at < ?`, cutoff)
	}
}

// telemetryInvestCheck compares the free XP the client says is left after a
// research with the server's balance less what the research spends. A mismatch means the two disagree about
// the player's XP -- a desync the player would see as a wrong balance or a
// refused research. Returns a note for the summary ("" when there is nothing
// to compare).
func telemetryInvestCheck(pid string, fields []telemetryField) string {
	clientFree, ok := telemetryNum(fields, "remainingFreeXp")
	if !ok {
		return ""
	}
	// The client sends this BEFORE the YA_UnlockItem it describes (live,
	// 2026-10-03: InvestEvent then UnlockItem, three researches in a row), so
	// the server has not charged it yet: its balance minus this research's
	// free XP is what the client's remainder must be. The first version
	// compared the balance as is and flagged every research that spent free
	// XP.
	spent, _ := telemetryNum(fields, "freeXp")
	serverFree := int64(mmogPlayerStateForPID(pid).freeXP) - spent
	if clientFree == serverFree {
		return "matches the server's free XP"
	}
	logrus.WithFields(logrus.Fields{"player": pid, "item": telemetryStr(fields, "itemID"),
		"client_free_xp": clientFree, "server_free_xp_after": serverFree}).
		Warn("telemetry: the client's free XP after research differs from the server's")
	return fmt.Sprintf("MISMATCH: server free XP after this research %d", serverFree)
}
