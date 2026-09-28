package main

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
// NOT WIRED YET. The battle server fills its table from the YMmogbrain
// subsystem (+0x43F8/+0x4408/+0x4460, present flag +0x4470, copied by 0x423450
// from UYScoringEventManager::InitializeData 0x423610). The response that fills
// it on a client, its format and each event's ordinal are still untraced, so
// this is keyed by the enum NAME (from the exe) until they are.
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
