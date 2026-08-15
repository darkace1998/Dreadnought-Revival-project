package main

import (
	"encoding/json"
	"testing"

	dreadconfig "github.com/darkace1998/Dreadnought-Revival-project/shared/dreadgameconfig"
)

// The client looks tuning rows up by BLUEPRINT ASSET NAME. These four names are
// not invented: they are the exact strings a live client asked for on
// 2026-08-15 and could not find, once per weapon per pawn:
//
//	LogYTuneManager:Error: LoadWeaponRow() Weapon Data for
//	  'WP_CreepPrimary01_weapon01_BP' Couldn't be found.
//	LogYWeaponGroup:Error: Couldn't find OTS data for weapon
//	  WP_CreepPrimary01_weapon01_BP on ship VH_Creep_Pawn_BP_C_22.
//	  Trying in offline datatable.
//
// They cover the three ways the old builder broke: a creep weapon (no player
// item id, so it was dropped entirely), a tiered player weapon, and an ability
// turret whose name ends in _C.
var clientRequestedWeaponRows = []string{
	"WP_CreepPrimary01_weapon01_BP",
	"WP_DreadnoughtMPri01_weapon01_T1_BP",
	"WP_DreadnoughtSecMid01_weapon01_T0_BP",
	"AB_DN_Per_Tur_Def_weapon01_BP_C",
}

func TestWeaponsTuneUsesAssetRowNames(t *testing.T) {
	var rows []map[string]any
	if err := json.Unmarshal([]byte(dreadconfig.WeaponsTuneJSON()), &rows); err != nil {
		t.Fatalf("WeaponsTune is not a JSON array: %v", err)
	}

	byName := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		name, _ := row["RowName"].(string)
		if name == "" {
			t.Fatalf("a WeaponsTune row has no RowName: %v", row)
		}
		byName[name] = row
	}
	t.Logf("WeaponsTune carries %d rows", len(byName))

	for _, want := range clientRequestedWeaponRows {
		row, ok := byName[want]
		if !ok {
			t.Errorf("WeaponsTune has no row %q; the client logs "+
				"\"Weapon Data for '%s' Couldn't be found\" for exactly this",
				want, want)
			continue
		}
		// A row that exists but carries only its name is the same failure with
		// extra steps -- the old ProjectilesTune builder emitted precisely that.
		if len(row) < 10 {
			t.Errorf("row %q has only %d fields; the cooked table carries 47",
				want, len(row))
		}
	}
}

// Officers go through the same verbatim path, so the same shape assertion
// applies: real row names, real fields.
func TestOfficersTuneUsesAssetRowNames(t *testing.T) {
	var rows []map[string]any
	if err := json.Unmarshal([]byte(dreadconfig.OfficersTuneJSON()), &rows); err != nil {
		t.Fatalf("OfficersTune is not a JSON array: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("OfficersTune is empty")
	}
	for _, row := range rows {
		if name, _ := row["RowName"].(string); name == "" {
			t.Fatalf("an OfficersTune row has no RowName: %v", row)
		}
	}
	t.Logf("OfficersTune carries %d rows", len(rows))
}
