package main

import (
	"strconv"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// The quest catalog the client's quest cycle needs (verified 2026-09-25).
//
// The client files the reply to its YA_GetDailyContractsData request by
// REQUEST ID (slot mmog+0x3690, written by the sender at 0x2A336A8, which logs
// "Sending daily contracts data request"). The dispatcher hands that reply's
// document root to 0x2A6B7F0, which reads:
//
//	ContractTable          array, one row per quest (row parser 0x2A69840)
//	ContractConfigTable    object (0x2A695C0)
//	ContractNextResetTime  Unix seconds, number or string
//
// into mmog+0x44A0..+0x44C0, and then sets mmog+0x44C8 = 1 UNCONDITIONALLY.
// UYPlayerMPQuestCycle::OnBackendDataAvailable (0x3FE800) waits for that flag,
// copies the rows into the quest collection (0x3FC1B0 -> 0x404440, second
// argument in RDX; the decompiler hides it) and resolves each row's Id against
// the collection's quest assets (0x3FBE90 compares it with the quest CDO's
// m_id at +0x48). Rows that resolve land in the collection's map at +0x98.
//
// With NO row resolving, that map stays empty, the "loaded" callback (0x402DB0)
// broadcasts anyway, and OnBackendDataAvailable re-enters with identical state:
// the ~2824-frame stack overflow on hangar entry seen in July. Our reply never
// carried ContractTable -- it sent DailyContractStateID/Quests/Contracts, the
// fields of the YA_ContractReplace handler (0x2A69310) -- so no row ever
// resolved. The rows below come from the client's own MPQuestCollection
// (scripts/gen-mpquest-table.py), so every one resolves.
type mpQuestRow struct {
	id   string // the quest's m_id (what the client compares), not its asset name
	path string
	rank int32 // m_requiredRank
}

// Row fields as the row parser reads them (0x2A69840): Id -> FName at +0x00,
// Path -> +0x08, CR -> +0x18, RP -> +0x1C, EnabledInGame -> +0x20, Rank -> +0x28.
// The loaded callback copies EnabledInGame == 1 into the quest's m_enableInGame
// and Rank into m_requiredRank.
//
// GUESS: CR and RP. Nothing on the quest-cycle path reads +0x18/+0x1C, and no
// client reader of them has been found; their meaning (and the original
// values) lived in the exported QuestsTable.cfg. Sent as 0.
func appendMmogContractCatalog(b []byte, stack []int, now time.Time) ([]byte, []int) {
	b, stack = protocol.AppendArrayStart(b, stack, "ContractTable")
	for _, row := range mpQuestTable {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "Id", row.id)
		b = protocol.AppendStringField(b, "Path", row.path)
		b = protocol.AppendStringField(b, "CR", "0")
		b = protocol.AppendStringField(b, "RP", "0")
		b = protocol.AppendStringField(b, "EnabledInGame", "1")
		b = protocol.AppendStringField(b, "Rank", strconv.Itoa(int(row.rank)))
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)

	b, stack = protocol.AppendObjectStart(b, stack, "ContractConfigTable")
	b = protocol.AppendStringField(b, "NumBaseContractSlots", strconv.Itoa(mpQuestNumBaseContractSlots))
	b = protocol.AppendStringField(b, "NumEliteContractSlots", strconv.Itoa(mpQuestNumEliteContractSlots))
	b = protocol.AppendStringField(b, "ContractResetTimeHour", strconv.Itoa(contractResetHourUTC))
	b = protocol.AppendStringField(b, "ContractResetTimeMinute", strconv.Itoa(contractResetMinuteUTC))
	b, stack = protocol.AppendObjectEnd(b, stack)

	b = protocol.AppendStringField(b, "ContractNextResetTime",
		strconv.FormatInt(nextContractReset(now).Unix(), 10))
	return b, stack
}

// GUESS: the daily reset time. The slot counts come from the collection's
// m_dailyContractsConfig; the reset time was mmogbrain configuration and is
// not in any client asset. Midnight UTC.
const (
	contractResetHourUTC   = 0
	contractResetMinuteUTC = 0
)

// nextContractReset is the first reset strictly after now.
func nextContractReset(now time.Time) time.Time {
	now = now.UTC()
	reset := time.Date(now.Year(), now.Month(), now.Day(),
		contractResetHourUTC, contractResetMinuteUTC, 0, 0, time.UTC)
	if !reset.After(now) {
		reset = reset.Add(24 * time.Hour)
	}
	return reset
}
