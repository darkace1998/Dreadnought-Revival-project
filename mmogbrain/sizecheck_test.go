//nolint:goconst // Size snapshots intentionally key by repeated MMOG message names for direct comparison.
package main

import (
	"fmt"
	"testing"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

var targetSizes = map[string]int{
	// Was 174 — issue #50 fix: removed dead flat credits/premiumCurrency/
	// freexp/xp fields (the client's YA_UserLogin handler never reads them),
	// added real credits/freexp/gp fields nested under LoginStreak instead.
	"YA_UserLogin":  153,
	"YA_UserOnline": 81,
	// Was 5250, +6 after fixing int32-blindness in the FleetTypes Tiers
	// sub-array (appendMmogStaticFleetTypeEntry) — see buildMmogStaticFleetDataPayload.
	// +56 on 2026-08-04: each of the four fleet loadout entries gained m_shipId
	// (14 bytes x 4). Without it the hangar loaded the LIGHT bay for every owned
	// ship -- all four starters are Mediums -- while tech tree ships, which
	// carry a ship id, loaded the correct bay for their size. See the comment
	// at the field in buildMmogPlayerFleetsPayload.
	"YA_RequestStaticFleetData": 7228,
	"YA_GetFeatureToggle":       111,
	// Now 967 (+433): GameModes is emitted at the message ROOT as well as inside
	// "result". The response's own handler calls GetGameModesData
	// (FUN_142a4ca40) on the parsed document and looks the array up as a DIRECT
	// child (FUN_140237c30 + child count at +0x20); it does not descend into
	// "result" the way the MaxSquadSize reader does, so a nested-only array read
	// as zero and the client had an EMPTY game-mode list -- no mode selectable,
	// Play could not start a match.
	"YA_GetGameConfigData":     967,
	"YA_GetStaticCareerData":   2153,
	"YA_GetProgressionData":    126,
	"YA_GetScoringData":        5753,
	"YA_GetDailyContractsData": 227,
	"YA_GetBoosterData":        1856,
	"YA_GetCareerProgression":  382,
	"YA_GetPlayerScores":       277,
	// Was 39062, +360 after fixing int32-blindness in appendMmogItemPriceDataFields
	// (m_realCurrency/m_hardCurrency/m_softCurrency/m_freeXP/m_shipXP now
	// numeric strings, matching the rest of this payload's m_-prefixed fields).
	// Was 39422 then 56099. Now MINIMAL: only the 10 validated T1+T2 nodes
	// with identity + unlock/ownership state; static ship/loadout/module
	// definitions come from the client's own Content. This keeps the frame far
	// under the client's 32KB (0x8000) mmog receive ring buffer.
	// Now 16938: the TechTrees document is the array-of-arrays shape
	// UYTechTreeManager's loader actually walks, carrying only the fields it
	// resolves by name (Id/ClassId/Manufacturer/Tier/Position/Visible/XPCost/
	// FPCost/NumTechTreeItemsRequired/ProxyType/Prereq/Wires). The previous
	// document was larger because it carried invented fields the loader never
	// read.
	// +176 since: every m_displayInfo grew from ";;;;" to the explicit
	// "-1#-1#-1#-1;-1;-1;-1;-1" form, which stops the client logging an "empty
	// entriy" Error for every vanity slot of every call.
	// Now 13768: the four development loadout aliases collapsed into the real
	// precast ids, leaving 14 items instead of 18 (see
	// nativeStarterLoadoutClassName).
	// Now 14461: +211 for ClassId becoming a real item id. It used to be the
	// 1..15 EYShipClass ordinal (1-2 characters), whose top byte is 0 -- and
	// the manager's store gate admits an item only when (ClassId >> 24) & 0xff
	// is 1 (precast) or 3 (hero), so every node was silently dropped and the
	// tech tree screen had no manufacturers at all. The value is now the hull
	// line's root loadout id, 8 characters, across ~100 nodes; the document is
	// zlib'd, so the frame grows by a fraction of the raw difference and stays
	// far under the client's 32KB receive ring buffer.
	// Now 14550: +89 for ClassId becoming each item's OWN id rather than the
	// hull line's root id. ClassId is the key of the array at manager+0x48,
	// which the client looks up by SHIP ID (FUN_1403f5050, called by
	// ComposeModuleUiDataForShip), so sharing a line root left every tier above
	// it unable to resolve its modules.
	// Now 16454: +1904 for the layout data the loader actually reads. Each item
	// gained a "UI" object holding one named child with Position{x,y}, Visible
	// and an (empty) Wires array -- Position/Visible sent flat on the item were
	// never read, because FUN_1403ffde0 reads them from UI's CHILDREN
	// (1404002b5 indexes the UI node's children pointer). Plus one layout-only
	// tier row per tier, whose Id sits in the negative sentinel range
	// [-2000000,-1000001] that routes it to the manager+0x58 (x, y, Tier) table
	// instead of the item store. Document is zlib'd, so this is a fraction of
	// the raw growth and leaves ~16KB of headroom under the 32KB ring.
	// Now 21505: +5051 for the per-ship MODULE entries. Every consumer of a
	// ship's modules reads the modules TArray at record+0x08 of
	// FindShipTechTreeData's array, and the loader files an entry there only
	// when its ProxyType is -1 (140401443); ProxyType 9 goes to the proxyItems
	// array at +0x18, which is what draws the tree. Sending 9 on everything
	// filled the tree and emptied the modules, so every ship read "0/0 modules
	// available". These entries are deliberately MINIMAL -- no UI, Position,
	// Visible, Prereq or Wires -- because only +0x20/+0x2C/+0x3C are read off
	// the stored record; the full form put the frame at 35103 bytes, over the
	// 32768-byte ring. 21505 leaves ~11KB of headroom.
	// Now 25846: the module entries became an UPGRADE PATH instead of a copy of
	// the equipped loadout. A slot's line is one asset name with a _T<n> token
	// and each tier is a separately registered item
	// (WP_AssaultMPri01_weapon01_T1_BP .. _T5_BP), so a slot now emits its
	// equipped item plus every STRICTLY higher tier of the same line -- 947
	// entries across 52 hulls, versus 237 before, which listed the current
	// loadout back at the player with nothing to research. Still minimal per
	// entry and ~7KB under the 32768-byte ring.
	// Now 25383: module entries are the ALTERNATIVES only. The client draws a
	// ship's fitted modules itself from its UYCachedItemIDData slot list (tags
	// 1..10), so anything sent for a slot is IN ADDITION to what is already on
	// the rail -- sending the equipped item too is what showed the default
	// loadout twice, three attempts running. The equipped LINE is excluded
	// whole, because a module's tier is not a separate node. 937 entries,
	// ~7.4KB under the 32768 ring.
	//
	// Now 23943 (-1440): module alternatives are gated to the HULL'S TIER. A
	// line whose lowest variant sits above the hull contributes nothing, where
	// it used to contribute that variant regardless -- which is how the Tier 1
	// Agosta's tech tree came to offer Tier 5 modules, reported from a live
	// client as "the module tech tree shows the wrong modules for each ship".
	// The payload SHRANK because tier-1 and tier-2 hulls lost entries they
	// should never have had; tier 5 GREW from 2 modules to ~23 in the same
	// change, because their fitted abilities sit on the previous build's
	// tier-less asset paths and were invisible to the slot index until
	// techTreeAbilityAssetUntiered.
	//
	// Now 23762 (-181): hull nodes no longer name THEMSELVES in ClassId. The
	// client recurses into the item its ClassId names (FUN_3F4880 ->
	// FUN_3F51A0 -> FUN_3F4880), so a hull pointing at itself was an infinite
	// loop and a guaranteed EXCEPTION_STACK_OVERFLOW on any module click. A
	// hull now points at its prerequisite, and a line root or hero at 0. The
	// shrink is those roots and heroes carrying "0" instead of an 8-digit id.
	//
	// KNOWN COST, accepted deliberately: the loader gate drops ClassId <= 0
	// (TEST R15D,R15D / JLE skip), so those 63 nodes are not stored. Verified
	// live on 2026-08-08 that the tree still works -- 12 module panels opened,
	// zero crashes, and none of the symptoms dropped nodes used to cause
	// ("Could not find a manufacturer with id", "TreeWidgetList of length 0")
	// appeared. TestLineRootsAndHeroesAreTheOnlyDroppedNodes pins the 63 so it
	// cannot grow quietly; giving roots a category-1 id that is not a tree row
	// would recover them and is the open refinement.
	//
	// Now 16272 (-7490): per-ship MODULE entries are no longer sent, at the
	// operator's instruction -- we were emitting hundreds per ship while the
	// client displayed a base plus two, so the two sets disagreed by an order
	// of magnitude and every module observation was made through a screen
	// showing the wrong thing. This is a deliberate reduction to a known-good
	// baseline, NOT a fix: why only three appear is still unexplained.
	// DN_TECHTREE_WITH_MODULES=1 restores them; see techTreeNoModules.
	"YA_GetTechTree": 16272,
	// Was 1035, +185 after fixing int32-blindness (CurrentXP/CurrentRank/
	// RankXP/XPToNextRank/NumUnlockedShips and per-ship shipID/xp/tier now
	// numeric strings, matching the rest of this payload family).
	// Was 1220/4040. Now 857: progression tracks only the 10 validated T1+T2
	// tech-tree ships (see techTreeShips).
	// Was 1097, +56: "owned" -> "m_isOwned" on every ship row (the old name is
	// absent from the client binary), and PurchasesData entries changed from
	// int32 to numeric strings so the client can actually read them.
	"YA_GetPlayerProgression": 1153,
	"YA_GetPlayerPurchases":   100,
	// Was 305, then 233 after removing fabricated Eligible/isEligible bool
	// fields (issue #51 — zero footprint in the client binary). Now 953: the
	// body is the FleetTypes/Maintenance shape FUN_142a78790 actually parses,
	// shared with YA_RequestStaticFleetData. The old "fleet_eligibility" array
	// was smaller because none of its field names were ones the client reads.
	"YA_FleetEligibility": 953,
	// Was 368116 (full tuning tables) — that overflowed the 16-bit mmog frame
	// size field and desynced the client stream, blocking hangar entry. Now sends
	// empty override tables (client uses its backup asset tuning); see
	// buildMmogTunePayload. Must stay well under 65535.
	"YA_Tune":          299,
	"YA_GetSeasonData": 650,
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
	// Was 7158, -39 after omitting the Membership object entirely for
	// never-purchased players instead of sending it with ExpireTime="0":
	// the client's parser (FUN_142a85120) drives a present-but-zero object
	// through its value-present branch, computing a 1970-01-01 epoch
	// FILETIME — the last thing logged before an EXCEPTION_STACK_OVERFLOW
	// crash during hangar entry. Sending no object at all uses the client's
	// own dedicated "no membership" branch instead.
	// Was 10810, then 10816, now 10825. The first step made credits/premium
	// numeric strings; that changed nothing in game, because the FIELD NAMES
	// were wrong. "gl" and "ob" appear ZERO times in the client binary (use
	// `strings -n 2` -- the 4-char default hides every short field name here),
	// while tll/tpl/tc/rep/FreeXp/Credits all appear. They were invented, so
	// credits and premium were never delivered at all. Renamed to the pair this
	// codebase already knew from YA_RewardCurrencies: root-level "Credits" and
	// "Points". +9 is the longer names.
	// +56 on 2026-08-04: each of the four fleet loadout entries gained m_shipId
	// (14 bytes x 4). Without it the hangar loaded the LIGHT bay for every owned
	// ship -- all four starters are Mediums -- while tech tree ships, which
	// carry a ship id, loaded the correct bay for their size. See the comment
	// at the field in buildMmogPlayerFleetsPayload.
	"YA_PlayerGet": 10881,
	// +56 on 2026-08-04: each of the four fleet loadout entries gained m_shipId
	// (14 bytes x 4). Without it the hangar loaded the LIGHT bay for every owned
	// ship -- all four starters are Mediums -- while tech tree ships, which
	// carry a ship id, loaded the correct bay for their size. See the comment
	// at the field in buildMmogPlayerFleetsPayload.
	"YA_PlayerFleets":          2144,
	"YA_GetSeasonProgress":     146,
	"YA_GetPlayersInformation": 326,
	// Was 115, -14 after removing fabricated ReturnValue field (issue #52).
	"YA_CheckReturn":               101,
	"YA_AnalyticsBeginTransaction": 124,
	"YA_AnalyticsEvent":            85,
	"YA_SaveCtAData":               82,
	"YA_GetPlayerStatsCounterData": 128,
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
		"YA_GetCareerProgression": func() []byte {
			return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogCareerProgressionPayload(pid))
		},
		"YA_GetPlayerScores": func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogPlayerScoresPayload()) },
		"YA_GetTechTree":     func() []byte { return protocol.BuildResponseFrame(reqID, 0x0320, buildMmogTechTreePayload()) },
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
		// YA_GetTechTree carries a zlib-compressed blob (see the TechTrees
		// field in buildMmogTechTreePayload). Compressed length shifts by a
		// byte or two with the player id embedded in the document, so an exact
		// match is not a meaningful assertion for it. The point of this guard
		// is to catch payload bloat against the client's 32KB receive ring, so
		// allow a small band and keep the exact check for everything else.
		tolerance := 0
		if name == "YA_GetTechTree" {
			tolerance = 64
		}
		if delta < -tolerance || delta > tolerance {
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
