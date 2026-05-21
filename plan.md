# Hangar implementation plan

## Problem
The client now reaches hangar entry reliably, but it still does not complete the outpost/home flow because the server is only providing a minimal subset of the state the stock client expects. Decompiled logic and the singleplayer mod both point to the same missing areas: coherent per-player loadout state, fleet/flagship state, inventory-owned items, and complete market/catalog data.

## Proposed approach
Implement the remaining hangar prerequisites in the same order the client appears to consume them:

1. Make fleet + flagship + active ship selection internally consistent across `YA_PlayerFleets`, `YA_RequestStaticFleetData`, and `YA_PlayerGet`.
2. Add real per-player loadout state using precast loadout item IDs, equipped items, and active-loadout markers instead of only ship ownership.
3. Populate hangar-facing item data the loadout/inventory managers expect, including modules, abilities, perks, and bonuses with the correct slot counts and IDs.
4. Complete market/inventory bootstrap data so home-screen initialization can finish after hangar entry.
5. Re-verify the hangar flow against logs and adjust any remaining field mismatches discovered during testing.

## Todos

### 1. Normalize fleet and flagship state
- Audit all fleet-producing payloads so the same flagship/loadout/ship IDs are reused everywhere.
- Ensure the selected starter fleet resolves to a real flagship ship and real active loadout.
- Confirm the static fleet payload and player fleets payload describe the same ship/loadout set.

### 2. Add real loadout records
- Introduce explicit server-side loadout definitions backed by real precast loadout item IDs from `ItemIDConversionTable.json`.
- Emit active-loadout state the client can map to `GetActiveLoadout` / `SetActiveLoadoutByName` style behavior.
- Preserve valid ship-to-loadout-to-pawn-family relationships for the starter roster.

### 3. Populate equipped item / preview / bonus data
- Fill the hangar-facing loadout data sets the client expects:
  - equipped loadout items
  - available-to-equip items
  - preview loadout items
  - hangar loadout detail data
  - battle-ready fleets data
- Model the confirmed slot counts:
  - 4 ability slots
  - 4 perk/feat slots
  - module list capacity consistent with the client UI data
- Add module / bonus / perk placeholders only when they use valid IDs and coherent ownership state.

### 4. Finish inventory and market bootstrap
- Expand catalog responses so `item_catalog_real`, `currency_catalog_real`, `item_catalog_virtual`, `currency_catalog_virtual`, and `bundles` are all present and structurally complete.
- Align inventory-owned items with the loadouts and ships we expose in MMOG responses.
- Keep manufacturer / tech tree / ownership / inventory views mutually consistent.

### 5. Verify against hangar gating
- Compare a fresh private-server session against the known-good singleplayer flow:
  - fleet found
  - flagship found
  - inventory initialized
  - market data updated
  - hangar preview resolves from the selected ship/loadout
- If the client still stalls, instrument the three blocking hangar responses (`YA_RequestStaticFleetData`, `YA_PlayerFleets`, `YA_GetTechTree`) and the home/outpost bootstrap data to find the next mismatch.

## Notes
- Current evidence says the remaining blocker is most likely **loadout/inventory state**, not transport ordering.
- `YA_RequestStaticFleetData`, `YA_PlayerFleets`, and `YA_GetTechTree` are the explicit MMOG gate for `YMS_HANGAR_ENTRY -> YMS_HANGAR`.
- Outpost/home readiness also depends on inventory initialization, fleet/flagship resolution, and complete market catalogs/bundles.
- The singleplayer mod succeeds by fabricating `m_loadouts`, precast loadouts, fleets, manufacturers, and hangar-ready state locally; the stock client still expects the server to provide coherent equivalents.
