package main

import (
	"encoding/hex"
	"testing"
)

// capturedUpdateShipLoadout is a real YA_UpdateShipLoadout payload, taken off
// the wire on 2026-08-02 after the player edited the Agosta in the hangar. It is
// the fixture rather than a hand-built one on purpose: every field name this
// handler reads was wrong before, and only the client can settle what it sends.
//
//	ShipID 33489262 (the Agosta's PRECAST LOADOUT id, not its pawn id)
//	Name "Agosta"
//	DisplayInfo "335872027#335872028#335872026#335872025;352649226;369426573;386203669;402980885"
//	WeaponPrimary 84213772  WeaponSecondary 84214563
//	AbilityPrimary 83820574 AbilitySecondary 83820606
//	AbilityPerimeter 83820565 AbilityInternal 83820550
//	PerkCom/PerkWeapon/PerkNavigation/PerkEngineer 0 (a tier-1 hull has none)
//	LoadoutSlotNum 1
const capturedUpdateShipLoadout = "025254091400000059415f557064617465536869704c6f61646f757402494402defaaaed1ecaadbc000000000000000003504944028ccc837066154eb2858dcb5a01448e2106536869704944566e01ff01044e616d65090600000041676f7374610b446973706c6179496e666f094f0000003333353837323032372333333538373230323823333335383732303236233333353837323032353b3335323634393232363b3336393432363537333b3338363230333636393b3430323938303838350d576561706f6e5072696d617279560c0005050f576561706f6e5365636f6e6461727956230305050e4162696c6974795072696d617279561e00ff04104162696c6974795365636f6e64617279563e00ff04104162696c697479506572696d65746572561500ff040f4162696c697479496e7465726e616c560600ff04075065726b436f6d56000000000a5065726b576561706f6e56000000000e5065726b4e617669676174696f6e56000000000c5065726b456e67696e65657256000000000e4c6f61646f7574536c6f744e756d5601000000000e00000000"

func capturedUpdatePayload(t *testing.T) []byte {
	t.Helper()
	raw, err := hex.DecodeString(capturedUpdateShipLoadout)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return raw
}

// Before this, persistUpdateShipLoadout returned at its first statement for
// every request the client has ever sent: it looked the row up by "LoadoutID"
// and the client calls the field "ShipID". Nothing a player changed in the
// hangar was stored -- not a weapon, not a module, not the ship's appearance.
func TestUpdateShipLoadoutPersistsWhatTheClientSent(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed player: %v", err)
	}

	// seedMmogPlayerState already creates the starter fleet, and 33489262 is
	// the Agosta -- the very ship this captured request edits.

	if err := persistUpdateShipLoadout(database, pid, capturedUpdatePayload(t)); err != nil {
		t.Fatalf("persist: %v", err)
	}

	var primary, secondary, abilityPrimary, abilityInternal int32
	var displayInfo, name string
	if err := database.QueryRow(`SELECT weapon_primary_id,weapon_secondary_id,ability_primary_id,ability_internal_id,display_info,loadout_name
		FROM player_ship_loadouts WHERE user_id=? AND loadout_id=?`, pid, 33489262).
		Scan(&primary, &secondary, &abilityPrimary, &abilityInternal, &displayInfo, &name); err != nil {
		t.Fatalf("read back: %v", err)
	}

	for _, tc := range []struct {
		field string
		got   int32
		want  int32
	}{
		{"weapon_primary_id", primary, 84213772},
		{"weapon_secondary_id", secondary, 84214563},
		{"ability_primary_id", abilityPrimary, 83820574},
		{"ability_internal_id", abilityInternal, 83820550},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.field, tc.got, tc.want)
		}
	}
	const wantDisplay = "335872027#335872028#335872026#335872025;352649226;369426573;386203669;402980885"
	if displayInfo != wantDisplay {
		t.Errorf("display_info = %q, want %q", displayInfo, wantDisplay)
	}
	if name != "Agosta" {
		t.Errorf("loadout_name = %q, want %q", name, "Agosta")
	}
}

// The stored appearance has to come back out, or persisting it changes nothing
// the player can see. An untouched ship keeps the computed default.
func TestSavedDisplayInfoWinsOverTheComputedDefault(t *testing.T) {
	stock := mmogShipLoadoutSeed{ship: starterShips[0]}
	if stock.displayInfo() == "" {
		t.Fatal("a ship with no saved appearance should still get the computed default")
	}

	const saved = "335872027#335872028#335872026#335872025;352649226;369426573;386203669;402980885"
	customised := mmogShipLoadoutSeed{ship: starterShips[0], savedDisplayInfo: saved}
	if got := customised.displayInfo(); got != saved {
		t.Errorf("displayInfo() = %q, want the player's own %q", got, saved)
	}
}

// A payload that names none of the fields must leave the row alone rather than
// blanking it -- the failure mode that makes a bad request destroy real data.
func TestUpdateShipLoadoutIgnoresAnUnrelatedPayload(t *testing.T) {
	database := useTempMmogPlayerStateDB(t)
	const pid = "650dd79476a1484b8adcd01ac2f17354"
	if err := seedMmogPlayerState(database, pid); err != nil {
		t.Fatalf("seed player: %v", err)
	}
	if _, err := database.Exec(`UPDATE player_ship_loadouts SET weapon_primary_id=?, display_info=? WHERE user_id=? AND loadout_id=?`,
		100597772, "keep;me;;;", pid, 33489262); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := persistUpdateShipLoadout(database, pid, []byte("nothing useful here")); err != nil {
		t.Fatalf("persist: %v", err)
	}

	var primary int32
	var displayInfo string
	if err := database.QueryRow(`SELECT weapon_primary_id,display_info FROM player_ship_loadouts WHERE user_id=? AND loadout_id=?`,
		pid, 33489262).Scan(&primary, &displayInfo); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if primary != 100597772 || displayInfo != "keep;me;;;" {
		t.Errorf("row was modified: primary=%d display_info=%q", primary, displayInfo)
	}
}
