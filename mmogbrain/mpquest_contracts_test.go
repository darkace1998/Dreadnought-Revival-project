package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// The reply to the client's own YA_GetDailyContractsData request is parsed by
// 0x2A6B7F0, which reads ContractTable / ContractConfigTable /
// ContractNextResetTime from the document ROOT. Without resolvable rows the
// client's quest cycle recursed until the stack overflowed (mpquest_contracts.go).
func TestDailyContractsReplyCarriesTheQuestCatalogAtTheRoot(t *testing.T) {
	payload := buildMmogDailyContractsDataPayload()

	table := extractNamedMmogArray(t, payload, "ContractTable")
	for _, row := range mpQuestTable {
		if !bytes.Contains(table, protocol.AppendStringField(nil, "Id", row.id)) {
			t.Errorf("ContractTable is missing quest %s", row.id)
		}
	}
	for _, row := range mpQuestTable {
		if !bytes.Contains(table, protocol.AppendStringField(nil, "Path", row.path)) {
			t.Errorf("ContractTable is missing the path of %s", row.id)
		}
	}

	config := extractNamedMmogObject(t, payload, "ContractConfigTable")
	for name, want := range map[string]string{
		"NumBaseContractSlots":  "3",
		"NumEliteContractSlots": "1",
	} {
		if !bytes.Contains(config, protocol.AppendStringField(nil, name, want)) {
			t.Errorf("ContractConfigTable.%s != %s", name, want)
		}
	}
	if !bytes.Contains(payload, []byte("ContractNextResetTime")) {
		t.Error("reply has no ContractNextResetTime")
	}
}

// The table is the client's MPQuestCollection: 24 quests, and the Id is the
// quest's m_id, which is NOT always its asset name. Sending the asset name for
// YMPQ_MatchScoreONS (m_id "YMPG_MatchScoreONS") would leave that row unresolved.
func TestQuestCatalogUsesTheQuestsOwnIDs(t *testing.T) {
	if len(mpQuestTable) != 24 {
		t.Fatalf("mpQuestTable has %d rows, MPQuestCollection lists 24", len(mpQuestTable))
	}
	seen := map[string]bool{}
	for _, row := range mpQuestTable {
		if seen[strings.ToLower(row.id)] {
			t.Errorf("duplicate quest id %s", row.id)
		}
		seen[strings.ToLower(row.id)] = true
		if !strings.HasPrefix(row.path, "/Game/Generic/MPQuests/") {
			t.Errorf("%s: unexpected path %s", row.id, row.path)
		}
	}
	if !seen["ympg_matchscoreons"] {
		t.Error("YMPQ_MatchScoreONS must be sent under its m_id YMPG_MatchScoreONS")
	}
	for _, initial := range mpQuestInitialContracts {
		if !seen[strings.ToLower(initial)] {
			t.Errorf("initial contract %s is not in the catalog", initial)
		}
	}
}

func TestNextContractResetIsTheNextMidnightUTC(t *testing.T) {
	for _, tc := range []struct{ now, want string }{
		{"2026-09-25T21:30:00Z", "2026-09-26T00:00:00Z"},
		{"2026-09-25T00:00:00Z", "2026-09-26T00:00:00Z"}, // strictly after now
		{"2026-09-25T23:59:59+02:00", "2026-09-26T00:00:00Z"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		want, _ := time.Parse(time.RFC3339, tc.want)
		if got := nextContractReset(now); !got.Equal(want) {
			t.Errorf("nextContractReset(%s) = %s, want %s", tc.now, got, want)
		}
	}
}

// Career goal texts go out as NSLOCTEXT macros (FText import); a bare string
// displayed blank on a live client.
func TestCareerGoalTextsAreNsLocTextMacros(t *testing.T) {
	if got, want := nsLocText("NS", "K", `say "hi" \ bye`), `NSLOCTEXT("NS", "K", "say \"hi\" \\ bye")`; got != want {
		t.Fatalf("nsLocText = %s, want %s", got, want)
	}
	var b []byte
	var stack []int
	b, _ = appendCareerGoalsConfig(b, stack)
	for _, goal := range careerGoalsConfig() {
		title := nsLocText(careerGoalTextNamespace, goal.id+".Title", goal.title)
		if !bytes.Contains(b, protocol.AppendStringField(nil, "m_title", title)) {
			t.Errorf("goal %s: m_title is not %s", goal.id, title)
		}
	}
}
