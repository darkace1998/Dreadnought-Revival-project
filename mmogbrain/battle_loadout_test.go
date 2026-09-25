package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The game's loadout record is positional (0x34E550 copies 2 weapon, 4 ability
// and 4 perk slots verbatim), so empty slots must stay in place as 0.
func TestBattleLoadoutKeepsEmptySlotsInPlace(t *testing.T) {
	loadout := mmogShipLoadoutSeed{
		precastLoadoutID:  33489267,
		loadoutName:       "Dover",
		weaponPrimaryID:   84017210,
		weaponSecondaryID: 84017966,
		abilityIDs:        [4]int32{67240123, 0, 83820699, 83820692},
	}
	out := formatBattleLoadout("Default__VH_SniperLight_T2_PrecastLoadout_BP_C", "p", loadout)
	for _, want := range []string{
		"id=Default__VH_SniperLight_T2_PrecastLoadout_BP_C\n",
		"precast=33489267\n",
		"name=Dover\n",
		"abilities=67240123,0,83820699,83820692\n",
		"perks=0,0,0,0\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestBattleLoadoutRefusesNonLoopback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/battle/loadout?pid=a&id=b", nil)
	req.RemoteAddr = "10.0.0.26:5000"
	rec := httptest.NewRecorder()
	battleLoadoutHandler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 for a non-loopback caller", rec.Code)
	}
}
