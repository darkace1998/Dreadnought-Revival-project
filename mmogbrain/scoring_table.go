package main

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
	"github.com/sirupsen/logrus"
)

// PvP scoring table: points per EYScoringEventID (issue #67).
//
// The original values are lost. The table was authored as a YScoringAsset and
// exported to the original mmogbrain (DefaultScoring.ini:
// m_mmogbrainExportPath = .../ScoringTable.cfg), which served it to every game;
// it is not in the shipped paks, and the PvE scoring tables that are
// (data/datatables/PVE/*Scoring*) do not use these events. Every value below is
// therefore the OPERATOR'S CHOICE (2026-09-28), not recovered data. The one
// exception is FinalBlow: the client's own localisation has the UI string
// "+10 Final Blow".
//
// How it reaches the game (traced 2026-09-28). The battle server scores every
// event, and it reads its table from the YMmogbrain subsystem:
// UYScoringEventManager::InitializeData (0x423610) -> 0x423590 -> 0x423450
// copies subsystem +0x43F8/+0x4408/+0x4460 only when the flag at +0x4470 is
// set. The one writer of that block is the reply parser 0x2A75740, reached
// from the response dispatcher (0x2A2574D) for the request in slot +0x3680. The
// host never logs in, so the flag stayed 0 and every event scored 0.
// battle-server-mod now fetches scoringDocument() from GET /battle/scoring and
// runs it through that same parser on the host (see the mod's scoring hook).
//
// Events of other modes (Territory*, Havoc*, PvE, boss/drone/transport) and the
// debug Cheat event are left out, which is the same as 0 points.

type scoringTableEntry struct {
	Event  string // EYScoringEventID name without the YSEID_ prefix
	Points int32
}

var pvpScoringTable = []scoringTableEntry{
	// Kills: 100, or 200 on a ship one tier above yours (operator, 2026-09-28).
	// A match only mixes two neighbouring tiers (1/2, 2/3, 4/5), so the
	// two-tier events (CaptainKill_/CriticalAssist_HigherTier2, _LowerTier2)
	// cannot fire and are left out.
	{"CaptainKill_LowerTier1", 100},
	{"CaptainKill_SameTier", 100},
	{"CaptainKill_HigherTier1", 200},
	{"FinalBlow", 10}, // "+10 Final Blow" is in the client's own UI texts

	// Assists.
	{"Assist", 50},
	{"CriticalAssist_LowerTier1", 35},
	{"CriticalAssist_SameTier", 50},
	{"CriticalAssist_HigherTier1", 65},

	// AI ships (bots). GUESS: whether TDM bots raise these or CaptainKill.
	{"AITargetL", 50},
	{"AITargetM", 75},
	{"AITargetH", 100},
	{"AITargetLAssist", 25},
	{"AITargetMAssist", 35},
	{"AITargetHAssist", 50},

	// Support.
	{"Repair", 25},
	{"Supporter", 25},
	{"Saviour", 25},
	{"Guardian", 25},
	{"Intervention", 25},
	{"Parry", 25},
	{"Suppression", 25},
	{"Concentrator", 25},

	// Ribbons / feats.
	{"FirstStrike", 25},
	{"Payback", 25},
	{"Avenger", 25},
	{"BloodThurst", 25},
	{"Dominator", 25},
	{"EagleEye", 25},
	{"Marksman", 25},
	{"SecondHand", 25},
	{"Saboteur", 25},
	{"CutTheLifeline", 25},
	{"Finisher", 25},
	{"Fearless", 25},
	{"Ninja", 25},
	{"Predator", 25},
	{"Rampage", 25},
	{"Mayham", 25},
	{"Desolator", 25},
	{"Vanguard", 25},
	{"Comeback", 25},
	{"Warfare", 25},
	{"GoalOriented", 25},
	{"GoodSport", 25},
	{"PODTDMPickup", 25},

	// End of match.
	{"Winner", 250},
	{"MVP", 100},
	{"ManOfTheDay", 100},
	{"MatchEnd", 0},
	{"PlayTime", 0},

	// Damage-taken events: 0 until we know whether the value is multiplied by
	// the damage amount (a per-point value could dwarf every kill).
	{"IncomingRawDamage", 0},
	{"IncomingRawDamageThisLife", 0},
	{"IncomingRawDamageThisLife2", 0},
	{"RecentIncomingRawDamageXCloseEnemies", 0},
}

// scoringEventIDs is EYScoringEventID in declaration order: the index is the
// ordinal a scoring row's "Id" byte carries (row +0x00, parser 0x2A7A030).
// Order read from the enum's registration code (0x2A9DE0E-0x2A9FA68, one name
// per entry, values assigned in sequence as UHT does).
var scoringEventIDs = []string{
	"CaptainKill_SameTier", "CaptainKill_LowerTier1", "CaptainKill_LowerTier2",
	"CaptainKill_HigherTier1", "CaptainKill_HigherTier2",
	"CriticalAssist_SameTier", "CriticalAssist_LowerTier1", "CriticalAssist_LowerTier2",
	"CriticalAssist_HigherTier1", "CriticalAssist_HigherTier2",
	"Assist", "Repair", "Guardian", "Payback", "Suppression",
	"AITargetL", "AITargetM", "AITargetH", "AITargetLAssist", "AITargetMAssist", "AITargetHAssist",
	"Dominator", "Rampage", "Comeback", "FirstStrike", "Desolator", "Warfare", "Vanguard",
	"Winner", "GoodSport", "Saviour", "Ninja", "Saboteur", "Avenger", "EagleEye", "Mayham",
	"Intervention", "CutTheLifeline", "Parry", "Marksman", "MatchEnd", "ManOfTheDay", "MVP",
	"GoalOriented", "Supporter", "BloodThurst", "SecondHand", "Concentrator", "Fearless",
	"Finisher", "Predator", "FinalBlow",
	"TerritoryContribution", "TerritoryKill", "TerritoryCleared", "TerritoryCapturedPoint",
	"TerritoryCapturingPoint", "BossCarrierCaptainKill", "BossCarrierCriticalAssist",
	"transportCaptainKill", "transportCriticalAssist", "droneCaptainKill", "droneCriticalAssist",
	"PlayTime", "IncomingRawDamage", "IncomingRawDamageThisLife", "IncomingRawDamageThisLife2",
	"RecentIncomingRawDamageXCloseEnemies", "HavocXPKill", "HavocXPPickup", "HavocXPBossPickup",
	"PVEQuickKill", "Cheat", "TerritoryProtectCP", "PODTDMPickup",
	// Invalid (75) and Max (76) follow.
}

// scoringEndOfMatchEvents are NOT served (2026-09-28). With them in the table
// every match that reached EndMatch overflowed the host's stack a few ms after
// "EndMatch() Could not score Fleet Wins" (12:32, 14:07, 14:19 TDM/BC); the
// same modes without the table, and kill/assist scoring during the match, ran
// clean. GUESS: awarding these at EndMatch loops through the host's own local
// player, like the ClientSetPlayerRestrictions loop the mod already cuts. The
// Wine fault address is in ntdll, so the looping game function is not
// identified. Leaving them out is the bisect: if the overflow stops, these
// are the trigger.
var scoringEndOfMatchEvents = map[string]bool{
	"Winner": true, "MVP": true, "ManOfTheDay": true, "MatchEnd": true, "PlayTime": true,
}

func scoringEventOrdinal(name string) (int, bool) {
	for i, n := range scoringEventIDs {
		if n == name {
			return i, true
		}
	}
	return 0, false
}

// scoringGameModes is a row's GameModes: every mode the row applies to,
// ":"-separated. The scoring manager keeps a row only when the current mode's
// name is in it (filter 0x424190: splits on ":" after removing spaces; an
// empty mode name or "Outpost" passes everything) and ParseRewardForEvent
// (0x429E20) substring-searches it again.
//
// The host's mode names are NOT the DefaultGame.ini aliases. Seen in the mod's
// log on live hosts (2026-09-28): "TDM", "TER", "IVN" (an Onslaught match:
// game=Onslaught in its URL) and "Outpost". The others are not seen yet, so
// the aliases stay listed as a GUESS; a new name in the mod's "building the
// match table for mode" line belongs here.
const scoringGameModes = "TDM:TER:IVN:Outpost:PodTDM:TurboTDM:TE:Territory:TM:TMBasic:BC:Bootcamp:Onslaught:Default"

// scoringDocument is the scoring reply document the battle server's parser
// (0x2A75740) reads:
//
//	ScoringTable        array; each row parsed by 0x2A7A030 into 0xE8 bytes:
//	                    Id (byte ordinal), EventName, NameForPlayer, GameModes,
//	                    SummaryCategory, EventVisibility (int), Parameters,
//	                    EventScore/EventXP/EventCredits (STRINGS: a plain
//	                    number applies to every mode, "TDM(100)TER(150)" per
//	                    mode -- 0x42DF40), NotifyDuringMatch (bool), ribbon
//	                    fields, AssignXPToAllShips, Apply*Mlt, ApplyTierBonus,
//	                    RewardCategory.
//	ScoringParamsTable  object parsed by 0x2A75AB0 (GMCode forced to
//	                    "Default", XP/FXP/CR multipliers, CrBuckets, MaxScore)
//	                    plus a GMMultipliers list of the same shape.
//
// Only the fields that matter to the score are filled; the rest parse as
// empty/0. XP and credits stay 0 here: mmogbrain pays rewards itself
// (battle_result.go), so the host's own XP figures are not used.
func scoringDocument() []byte {
	var b []byte
	var stack []int
	b, stack = protocol.AppendArrayStart(b, stack, "ScoringTable")
	for _, e := range pvpScoringTable {
		id, ok := scoringEventOrdinal(e.Event)
		if !ok || e.Points <= 0 || scoringEndOfMatchEvents[e.Event] {
			continue
		}
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendInt32Field(b, "Id", int32(id))
		b = protocol.AppendStringField(b, "EventName", e.Event)
		b = protocol.AppendStringField(b, "NameForPlayer", nsLocText("DNScoring", e.Event, scoringDisplayName(e.Event)))
		b = protocol.AppendStringField(b, "GameModes", scoringGameModes)
		b = protocol.AppendStringField(b, "EventScore", strconv.Itoa(int(e.Points)))
		b = protocol.AppendStringField(b, "EventXP", "0")
		b = protocol.AppendStringField(b, "EventCredits", "0")
		b = protocol.AppendBoolField(b, "NotifyDuringMatch", true)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)

	// GUESS: the multipliers look like percentages (the operator's fleet
	// bonus is Recruit 100 / Veteran 125 / Legendary 150); they only shape the
	// host's own XP/credit figures, which mmogbrain does not use. MaxScore is
	// set far above any real score in case it is a cap.
	b, stack = protocol.AppendObjectStart(b, stack, "ScoringParamsTable")
	b = protocol.AppendStringField(b, "GMCode", "Default")
	for _, f := range []struct {
		name  string
		value int32
	}{
		{"XPTierMlt", 100}, {"XPVeteranMlt", 100}, {"XPPremiumMlt", 100},
		{"XPPremiumFreeXpPercentage", 100}, {"XPPremiumShipXpPercentage", 100},
		{"XPStandardFreeXpPercentage", 100}, {"XPStandardShipXpPercentage", 100},
		{"FXPRecrMlt", 100}, {"FXPVetMlt", 125}, {"FXPLegMlt", 150},
		{"CRVetMlt", 125}, {"CRLegMlt", 150},
		{"MaxScore", 1000000},
	} {
		b = protocol.AppendInt32Field(b, f.name, f.value)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	return protocol.AppendRootEnd(b)
}

// scoringDisplayName turns an event id into readable text: "CaptainKill_SameTier"
// -> "Captain Kill Same Tier".
func scoringDisplayName(event string) string {
	var out strings.Builder
	for i, r := range strings.ReplaceAll(event, "_", " ") {
		if i > 0 && r >= 'A' && r <= 'Z' && out.Len() > 0 && !strings.HasSuffix(out.String(), " ") {
			out.WriteByte(' ')
		}
		out.WriteRune(r)
	}
	return out.String()
}

// battleScoringHandler serves scoringDocument to battle-server-mod.
// GET /battle/scoring -> application/octet-stream, the document exactly as it
// would sit in the client's response slot (no frame header, no request id).
// Loopback only, like the other /battle/ endpoints.
func battleScoringHandler(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		http.Error(w, "loopback only", http.StatusForbidden)
		return
	}
	doc := scoringDocument()
	logrus.WithField("bytes", len(doc)).Info("battle scoring: table served")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(doc)))
	_, _ = w.Write(doc)
}
