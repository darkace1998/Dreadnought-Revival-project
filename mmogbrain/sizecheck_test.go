//nolint:goconst // Size snapshots intentionally key by repeated MMOG message names for direct comparison.
package main

import (
	"fmt"
	"testing"

	"github.com/dreadnought-ps/mmogbrain/protocol"
)

var targetSizes = map[string]int{
	// Was 174 — issue #50 fix: removed dead flat credits/premiumCurrency/
	// freexp/xp fields (the client's YA_UserLogin handler never reads them),
	// added real credits/freexp/gp fields nested under LoginStreak instead.
	"YA_UserLogin":                 153,
	"YA_UserOnline":                81,
	// Was 5250, +6 after fixing int32-blindness in the FleetTypes Tiers
	// sub-array (appendMmogStaticFleetTypeEntry) — see buildMmogStaticFleetDataPayload.
	"YA_RequestStaticFleetData":    5256,
	"YA_GetFeatureToggle":          111,
	"YA_GetGameConfigData":         534,
	"YA_GetStaticCareerData":       3531,
	"YA_GetProgressionData":        126,
	"YA_GetScoringData":            5753,
	"YA_GetDailyContractsData":     227,
	"YA_GetBoosterData":            1856,
	"YA_GetCareerProgression":      3437,
	"YA_GetPlayerScores":           277,
	// Was 39062, +360 after fixing int32-blindness in appendMmogItemPriceDataFields
	// (m_realCurrency/m_hardCurrency/m_softCurrency/m_freeXP/m_shipXP now
	// numeric strings, matching the rest of this payload's m_-prefixed fields).
	// Was 39422, +16677 after adding the 47 real Hero-variant ships (issue #40).
	"YA_GetTechTree":               56099,
	// Was 1035, +185 after fixing int32-blindness (CurrentXP/CurrentRank/
	// RankXP/XPToNextRank/NumUnlockedShips and per-ship shipID/xp/tier now
	// numeric strings, matching the rest of this payload family).
	// Was 1220, +2820 after adding the 47 real Hero-variant ships' per-ship
	// progression entries (issue #40).
	"YA_GetPlayerProgression":      4040,
	"YA_GetPlayerPurchases":        100,
	// Was 305, -72 after removing fabricated Eligible/isEligible bool
	// fields (issue #51 — zero footprint in the client binary).
	"YA_FleetEligibility":          233,
	// Was 368116 (full tuning tables) — that overflowed the 16-bit mmog frame
	// size field and desynced the client stream, blocking hangar entry. Now sends
	// empty override tables (client uses its backup asset tuning); see
	// buildMmogTunePayload. Must stay well under 65535.
	"YA_Tune":                      299,
	"YA_GetSeasonData":             1091,
	// YA_PlayerGet's Officers array schema was fixed (#41) to send the
	// type/disp/rep fields the client's per-entry parser actually reads,
	// replacing the far longer m_enabling/m_triggers/m_effects DSL text
	// that parser never looked up — legitimately smaller, not a regression.
	// Was 5961 — grew by 72 bytes after fixing the int32-blindness bug for
	// tll/tpl/tc/rep/repXX_X/ReputationGoalID/Membership.ExpireTime/
	// DailyContract*/FreeXp/ServerTime/ClientTime/tslm (all now sent as
	// numeric strings instead of raw int32, which the client's parser
	// silently read as 0) and adding the previously-missing "tc" field.
	// Was 6033, +42 after fixing Officers entries' int32-blindness (type/rep
	// now numeric strings, matching FUN_142a70b10's restrictive parser).
	// Was 6075, +18 after adding the previously-missing "Quests" array
	// (issue #43 — empty here since the sizecheck's default player has no
	// active contracts in this DB-less context).
	"YA_PlayerGet":                 6093,
	"YA_PlayerFleets":              2198,
	"YA_GetSeasonProgress":         146,
	"YA_GetPlayersInformation":     326,
	// Was 115, -14 after removing fabricated ReturnValue field (issue #52).
	"YA_CheckReturn":               101,
	"YA_AnalyticsBeginTransaction": 124,
	"YA_AnalyticsEvent":            85,
	"YA_SaveCtAData":               82,
	"YA_GetPlayerStatsCounterData": 338,
}

var preTrimTargetSizes = map[string]int{
	"YA_RequestStaticFleetData": 42160,
	"YA_PlayerGet":              56015,
	"YA_PlayerFleets":           3213,
}

func TestPayloadSizesVerify(t *testing.T) {
	pid := defaultMmogPlayerPID
	var reqID [16]byte

	builders := map[string]func() []byte{
		"YA_UserLogin":              func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogLoginSuccessPayload()) },
		"YA_UserOnline":             func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogUserOnlinePayload()) },
		"YA_RequestStaticFleetData": func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogStaticFleetDataPayload()) },
		"YA_GetFeatureToggle":       func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogFeatureTogglePayload()) },
		"YA_GetGameConfigData":      func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogGameConfigDataPayload()) },
		"YA_GetStaticCareerData":    func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogStaticCareerDataPayload()) },
		"YA_GetProgressionData":     func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogProgressionDataPayload()) },
		"YA_GetScoringData":         func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogScoringDataPayload()) },
		"YA_GetDailyContractsData":  func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogDailyContractsDataPayload()) },
		"YA_GetBoosterData":         func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogBoosterDataPayload()) },
		"YA_GetCareerProgression":   func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogCareerProgressionPayload()) },
		"YA_GetPlayerScores":        func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogPlayerScoresPayload()) },
		"YA_GetTechTree":            func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogTechTreePayload()) },
		"YA_GetPlayerProgression": func() []byte {
			return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogPlayerProgressionPayload(pid))
		},
		"YA_GetPlayerPurchases": func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogPlayerPurchasesPayload()) },
		"YA_FleetEligibility":   func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogFleetEligibilityPayload()) },
		"YA_Tune":               func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogTunePayload()) },
		"YA_GetSeasonData":      func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogSeasonDataPayload()) },
		"YA_PlayerGet":          func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogPlayerGetPayload(pid)) },
		"YA_PlayerFleets":       func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogPlayerFleetsPayload(pid)) },
		"YA_GetSeasonProgress":  func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogSeasonProgressPayload()) },
		"YA_GetPlayersInformation": func() []byte {
			return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogPlayersInformationPayload(pid, nil))
		},
		"YA_CheckReturn": func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogCheckReturnPayload()) },
		"YA_AnalyticsBeginTransaction": func() []byte {
			return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogAnalyticsBeginTransactionPayload("test-txid"))
		},
		"YA_AnalyticsEvent": func() []byte {
			return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogRequestSuccessPayload("YA_AnalyticsEvent"))
		},
		"YA_SaveCtAData": func() []byte {
			return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogRequestSuccessPayload("YA_SaveCtAData"))
		},
		"YA_GetPlayerStatsCounterData": func() []byte {
			return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogPlayerStatsCounterDataPayload())
		},
	}

	allPass := true
	for name, target := range targetSizes {
		builder, ok := builders[name]
		if !ok {
			t.Logf("MISSING: %s", name)
			continue
		}
		got := len(builder())
		delta := got - target
		status := "OK"
		if delta != 0 {
			status = fmt.Sprintf("FAIL delta=%+d", delta)
			allPass = false
		}
		t.Logf("%-40s target=%4d got=%4d %s", name, target, got, status)
	}
	if !allPass {
		t.Fatal("Payload sizes do not match reference")
	}
}

func TestPayloadRegressionFixShrinksHeavyBootstrapPayloads(t *testing.T) {
	pid := defaultMmogPlayerPID
	var reqID [16]byte

	builders := map[string]func() []byte{
		"YA_RequestStaticFleetData": func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogStaticFleetDataPayload()) },
		"YA_PlayerGet":              func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogPlayerGetPayload(pid)) },
		"YA_PlayerFleets":           func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogPlayerFleetsPayload(pid)) },
	}
	minReductions := map[string]int{
		"YA_RequestStaticFleetData": 30000,
		"YA_PlayerGet":              44376,
		"YA_PlayerFleets":           900,
	}

	for name, before := range preTrimTargetSizes {
		got := len(builders[name]())
		if reduction := before - got; reduction < minReductions[name] {
			t.Fatalf("%s reduction = %d, want at least %d (before=%d after=%d)", name, reduction, minReductions[name], before, got)
		}
	}
}
