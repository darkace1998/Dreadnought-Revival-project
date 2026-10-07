package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

// LOADOUT B: a ship's second loadout.
//
// How the client does it (verified in the exe, 2026-10-07):
//
//   - The edit-ship screen has two buttons, Button_Loadout_A/B
//     (SID_CustomizationOption_EditLoadoutA/B, "LOADOUT A"/"LOADOUT B"), which
//     call SetLoadoutCategory(EYLoadoutCategory) (thunk 0xBC3410, body
//     0xADE280). That stores the category and resolves the loadout through
//     0xAA8F60: the loadout manager's list for the ship (0x340950, a bucket per
//     ship id at mgr+0x108) indexed by the category -- and only when the
//     category is below the list's length. Every ship had one loadout, so
//     LOADOUT B resolved to null and did nothing (operator report 2026-10-07).
//   - The loadout manager builds that list from YA_PlayerGet's ShipLoadouts
//     and nothing else (0x34FF90; see the comment at its emitter): one
//     loadout object per entry whose ID (an FName, stored at loadout+0xB0 by
//     0x34D690) is not already loaded, added in order. The ID names no
//     blueprint -- the object is a plain UYShipLoadout filled from the entry.
//   - A save says which one it is: YA_UpdateShipLoadout's LoadoutSlotNum is
//     GetLoadoutVariationOrderNum (0x542050 -> 0x3407A0), the loadout's
//     1-based place in its ship's list, found by that same FName. Every save
//     so far was 1 (269 of 269 on 2026-10-07).
//   - In a match, the ship-select slot (YShipSelectSlotData:
//     m_showCustomLoadouts, m_currentLoadoutVariationSelected) picks between
//     them, and the pick reaches the host as the loadout's ID
//     (ServerPlayerClickedShipLoadout), which /battle/loadout resolves.
//
// So B is a second ShipLoadouts entry right after A, with its own ID, and
// saves with LoadoutSlotNum 2 are stored apart from A.
//
// No game restart is needed to get it: the YA_PlayerGet handler (0x142A3D820)
// fires the player-data event (mmog +0xE40) that runs InitializeFromPlayerData
// (0x34BD00), which loads every entry whose ID is not loaded yet -- and
// clients log in again by themselves when mmogbrain restarts (three within
// 10 s of the 2026-10-07 01:29 restart). A ship bought in-session gets B
// through the claim push's addedLoadouts.
//
// DN_LOADOUT_B: on for everyone unless "0"; a comma list of player ids or
// names limits it to those players.

// loadoutVariantSuffix tells a ship's loadouts apart in their ID.
// GUESS: the original server's ids for B are not known; anything distinct
// works for the client (0x34FF90 compares the whole FName).
func loadoutVariantSuffix(variant int32) string {
	if variant <= 0 {
		return ""
	}
	return "_" + string(rune('A'+variant))
}

// loadoutBEnabled reports whether the player gets a LOADOUT B.
func loadoutBEnabled(playerPID string) bool {
	setting := strings.TrimSpace(os.Getenv("DN_LOADOUT_B"))
	switch strings.ToLower(setting) {
	case "0", "off", "false":
		return false
	case "", "1", "on", "true", "all":
		return true
	}
	pid := normalizedPlayerStatePID(playerPID)
	var name string
	for _, entry := range strings.Split(setting, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if normalizedPlayerStatePID(entry) == pid && pid != "" {
			return true
		}
		if name == "" {
			name = strings.TrimSpace(mmogPlayerStateForPID(pid).displayName)
		}
		if strings.EqualFold(entry, name) {
			return true
		}
	}
	return false
}

// shipLoadoutHasVariants reports whether the ship gets a LOADOUT B. Heroes do
// not: their fit is fixed. GUESS: from the hero description ("They cannot be
// customised"); CanCurrentShipHaveLoadoutsBeEdited (0xBB20D0) was not traced.
func shipLoadoutHasVariants(loadout mmogShipLoadoutSeed) bool {
	if loadout.variant != 0 || loadout.precastLoadoutID == 0 {
		return false
	}
	_, hero := heroByID(loadout.precastLoadoutID)
	return !hero
}

// loadLoadoutVariants reads the player's saved B loadouts, by loadout id.
func loadLoadoutVariants(database *sql.DB, playerPID string) map[int32]mmogShipLoadoutSeed {
	out := map[int32]mmogShipLoadoutSeed{}
	if database == nil {
		return out
	}
	rows, err := database.Query(`SELECT loadout_id, loadout_name,
		weapon_primary_id, weapon_secondary_id, ability_primary_id, ability_secondary_id, ability_perimeter_id, ability_internal_id,
		perk_com_id, perk_weapon_id, perk_navigation_id, perk_engineer_id, display_info
		FROM player_ship_loadout_variants WHERE user_id=? AND slot=2`, normalizedPlayerStatePID(playerPID))
	if err != nil {
		logrus.WithError(err).Warn("mmog: load loadout variants")
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int32
		var v mmogShipLoadoutSeed
		if err := rows.Scan(&id, &v.loadoutName, &v.weaponPrimaryID, &v.weaponSecondaryID,
			&v.abilityIDs[0], &v.abilityIDs[1], &v.abilityIDs[2], &v.abilityIDs[3],
			&v.perkIDs[0], &v.perkIDs[1], &v.perkIDs[2], &v.perkIDs[3], &v.savedDisplayInfo); err != nil {
			logrus.WithError(err).Warn("mmog: scan loadout variant")
			return out
		}
		out[id] = v
	}
	return out
}

// loadoutB is a ship's LOADOUT B: the saved one, or until the player saves it
// a copy of LOADOUT A. GUESS: what the original server put in a B that was
// never edited is not known; a copy keeps the ship fully fitted.
func loadoutB(a mmogShipLoadoutSeed, saved map[int32]mmogShipLoadoutSeed) mmogShipLoadoutSeed {
	b := a
	b.variant = 1
	b.active = false
	if v, ok := saved[a.loadoutID()]; ok {
		if strings.TrimSpace(v.loadoutName) != "" {
			b.loadoutName = v.loadoutName
		}
		b.weaponPrimaryID, b.weaponSecondaryID = v.weaponPrimaryID, v.weaponSecondaryID
		b.abilityIDs, b.perkIDs = v.abilityIDs, v.perkIDs
		b.savedDisplayInfo = v.savedDisplayInfo
	}
	return b
}

// withLoadoutVariants returns the loadouts with each ship's LOADOUT B right
// after its A, when the player has B. The order matters: the client numbers a
// ship's loadouts by their place in ShipLoadouts (0x3407A0).
func withLoadoutVariants(playerPID string, loadouts []mmogShipLoadoutSeed) []mmogShipLoadoutSeed {
	if !loadoutBEnabled(playerPID) {
		return loadouts
	}
	saved := loadLoadoutVariants(currentMmogPlayerStateDB(), playerPID)
	out := make([]mmogShipLoadoutSeed, 0, 2*len(loadouts))
	for _, a := range loadouts {
		out = append(out, a)
		if shipLoadoutHasVariants(a) {
			out = append(out, loadoutB(a, saved))
		}
	}
	return out
}

// ensureLoadoutVariantRow creates the player's saved row for a ship's loadout
// in the given slot from its LOADOUT A, so a save that changes one slot keeps
// the rest of the fit. False when the ship has no LOADOUT A row.
func ensureLoadoutVariantRow(database *sql.DB, playerPID string, loadoutID, slot int32) (bool, error) {
	if _, err := database.Exec(`INSERT OR IGNORE INTO player_ship_loadout_variants
		(user_id, loadout_id, slot, loadout_name, weapon_primary_id, weapon_secondary_id,
		 ability_primary_id, ability_secondary_id, ability_perimeter_id, ability_internal_id,
		 perk_com_id, perk_weapon_id, perk_navigation_id, perk_engineer_id, display_info)
		SELECT user_id, loadout_id, ?, loadout_name, weapon_primary_id, weapon_secondary_id,
		 ability_primary_id, ability_secondary_id, ability_perimeter_id, ability_internal_id,
		 perk_com_id, perk_weapon_id, perk_navigation_id, perk_engineer_id, display_info
		FROM player_ship_loadouts WHERE user_id=? AND loadout_id=?`, slot, playerPID, loadoutID); err != nil {
		return false, fmt.Errorf("create loadout variant: %w", err)
	}
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM player_ship_loadout_variants WHERE user_id=? AND loadout_id=? AND slot=?`,
		playerPID, loadoutID, slot).Scan(&n); err != nil {
		return false, fmt.Errorf("find loadout variant: %w", err)
	}
	return n > 0, nil
}
