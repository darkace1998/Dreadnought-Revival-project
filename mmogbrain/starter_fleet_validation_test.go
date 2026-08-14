package main

import (
	"strconv"
	"strings"
	"testing"
)

// The operator's four fleet ships, validated end to end.
//
// Asked for directly on 2026-08-14 while the battle server could not spawn
// anyone: is the loadout/fleet data we send for these four actually correct?
// The answer is yes, and this pins it, because the same question keeps coming
// back whenever something downstream breaks.
//
// The names must be the SHIPPING precast assets (…_PrecastLoadout_BP_C), never
// the development ones (…_Loadout_BP_C from /Loadouts/Precast/Development/).
// Sending development blueprints is what produced the whole
// 33489198/33489239/33489199/33489200 id space: the client instantiates the
// blueprint named here and then reports that blueprint's OWN m_precastLoadoutID,
// so a development BP reports a development id and nothing downstream matches.
//
// Independently corroborated by the client itself, which logs
// "UYLoadoutManager::ActivateLoadout | Default__VH_AssaultMedium_T1_PrecastLoadout_BP_C"
// for the Agosta and the Sniper variant for the Rurik -- the exact strings
// below. The battle server's failed lookup names that same string, which is how
// we know the host's problem is an empty loadout manager and not bad data.

var operatorStarterFleet = []struct {
	id        int32
	name      string
	loadoutBP string
}{
	{33489262, "Agosta", "Default__VH_AssaultMedium_T1_PrecastLoadout_BP_C"},
	{33489423, "Simargl", "Default__VH_DreadnoughtMedium_T1_PrecastLoadout_BP_C"},
	{33489263, "Rurik", "Default__VH_SniperMedium_T1_PrecastLoadout_BP_C"},
	{33489264, "Cerberus", "Default__VH_SupportMedium_T1_PrecastLoadout_BP_C"},
}

func TestStarterFleetResolvesShippingLoadoutBlueprints(t *testing.T) {
	for _, ship := range operatorStarterFleet {
		got, ok := nativeStarterLoadoutClassName(ship.id)
		if !ok {
			t.Errorf("%s (%d): no loadout blueprint resolved at all", ship.name, ship.id)
			continue
		}
		if got != ship.loadoutBP {
			t.Errorf("%s (%d) = %q, want %q", ship.name, ship.id, got, ship.loadoutBP)
		}
		if strings.Contains(got, "Development") || !strings.Contains(got, "_PrecastLoadout_BP_C") {
			t.Errorf("%s (%d) resolves to a non-shipping blueprint %q; the client would report "+
				"that blueprint's own id instead of ours", ship.name, ship.id, got)
		}
	}
}

// The ids have to reach the fleet, and the blueprint names have to reach the
// payloads that actually carry them. They are NOT in YA_PlayerFleets -- that
// one carries ids only -- so asserting against the wrong message reads as a
// missing name when nothing is missing.
func TestStarterFleetDataReachesTheWire(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"

	fleets := string(buildMmogPlayerFleetsPayload(pid))
	carriers := map[string]string{
		"YA_PlayerGet":              string(buildMmogPlayerGetPayload(pid)),
		"YA_RequestStaticFleetData": string(buildMmogStaticFleetDataPayload()),
	}

	for _, ship := range operatorStarterFleet {
		if !strings.Contains(fleets, strconv.Itoa(int(ship.id))) {
			t.Errorf("%s (%d) is not in YA_PlayerFleets", ship.name, ship.id)
		}
		found := false
		for _, payload := range carriers {
			if strings.Contains(payload, ship.loadoutBP) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: %q appears in no payload that carries loadout blueprints",
				ship.name, ship.loadoutBP)
		}
	}
}

// No development blueprint may reach the client, in any payload.
func TestNoDevelopmentLoadoutBlueprintIsEverSent(t *testing.T) {
	useTempMmogPlayerStateDB(t)
	const pid = "00000000000000000000000000000001"

	for name, payload := range map[string]string{
		"YA_PlayerGet":              string(buildMmogPlayerGetPayload(pid)),
		"YA_PlayerFleets":           string(buildMmogPlayerFleetsPayload(pid)),
		"YA_RequestStaticFleetData": string(buildMmogStaticFleetDataPayload()),
		"YA_GetTechTree":            string(buildMmogTechTreePayload(pid)),
	} {
		for _, bad := range []string{"/Development/", "_T1_Loadout_BP_C", "_T2_Loadout_BP_C"} {
			if strings.Contains(payload, bad) {
				t.Errorf("%s references a development loadout blueprint (%q)", name, bad)
			}
		}
	}
}
