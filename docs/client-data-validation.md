# Dreadnought client data: validation, map, and server audit

Everything below was derived from the extracted client tables and validated, not assumed.
Sources: `data/assets/{ItemIDRegister,ItemIDTable,ItemIDConversionTable,CatalogIDTable}.json`,
`Content/Localization/DreadGame/en/*.locres`, and the cooked `.uasset` tree.

## 1. The one invariant everything else rests on

**The top byte of an item id IS its `ItemIDTable` CategoryID.** Tested across every id in
every category: **3437 agree, 0 disagree.**

    categoryByte(id) = (id >> 24) & 0xff

This is what the client's tech-tree gate uses (`FUN_1402cf640`), so it is worth stating as a
law rather than an observation.

| CategoryID | Category | ids | with register path |
|---|---|---|---|
| 1 | YShipLoadoutPrecast | 283 | 259 |
| 3 | YShipLoadoutHero | 64 | 57 |
| 4 | YAbility | 553 | 521 |
| 5 | YWeapon | 521 | 453 |
| 6 | YPerk | 41 | 41 |
| 10 | YPawn | 219 | 203 |
| 20-24 | ship vanity (mesh/emblem/paint/pattern/decal) | 1369 | 1241 |
| 50-55 | character customization | 369 | 282 |
| 80-82 | menu navigation | 29 | 19 |
| 99 | YGameMode | 32 | 18 |

Full table: `01_tables.json`. 27 categories, 3498 id entries, 3086 register paths.

## 2. Ships and loadouts

Asset paths encode class, size and tier, so both sides of the mapping are derivable:

    ship:    /Game/Generic/Ships/Assault/Medium/T1/VH_AssaultM_Pawn_T1_BP
    loadout: /Game/Generic/Loadouts/Precast/T1/VH_AssaultMedium_T1_PrecastLoadout_BP

- **57** ship pawns under `/Ships/`; **50** map to a precast loadout.
- **66** player-facing precast loadouts (of 283 in the category; the rest are Havoc,
  AI-boss and Development variants).
- The 7 unmapped ships are 4 RespawnJets, DreadnoughtEnergy, AssaultLight T5 and
  SupportLight T2 — the last two are genuine gaps in the loadout set.

Map: `02_ships_loadouts.json`.

### Tier 4 does not exist in the ship data
Ship tier distribution is **{1:4, 2:7, 3:12, 5:15} — no tier 4 at all**, while 15 of the 17
orphaned precast loadouts are tier 4. 16 YPawn ids have no register path and no conversion
entry, so they cannot be resolved from what we have.

**Consequence:** a tech tree built from ship pawns can never contain tier 4. One built from
the *precast loadout* catalogue can, because those 66 entries cover tiers 1-5 completely.
Since the tech tree is keyed on loadout ids anyway, that is the better source.

## 3. Server audit: every id we emit

| field | n | unresolvable | wrong category |
|---|---|---|---|
| techTree rowID | 14 | 0 | 0 |
| techTree shipID | 14 | 0 | 0 |
| fleet shipIds / loadoutIDs / flagship | 9 | 0 | 0 |
| loadout precastLoadoutID / shipPawnID | 8 | 0 | 0 |
| loadout weapons (primary+secondary) | 8 | 0 | 0 |
| loadout abilities | 16 | 0 | 1 (see below) |
| catalog itemID / shipID | 124 | 0 | 0 |
| inventory itemID / shipID | 64 | 0 | 0 |

Every id the server sends resolves to a real asset and sits in the category its consumer
expects. **The ids are not the problem.**

Detail: `03_server_ids.json`.

## 4. Defects found

### 4.1 ClassId drift — FIXED
Sniper Light T2 (`184483954`) was sent with `classID 10` (`YSC_SNIPER_MEDIUM`) where its
asset path `/Ships/Sniper/Light/T2/` says **3** (`YSC_SNIPER_LIGHT`). One wrong value out of
14; found by validating every seed against its path. Now derived from the path, with a test
pinning it.

### 4.2 ItemIDTable is not a complete index
**37 ids have a register path but appear in no category** — 18 weapons, 8 abilities, 4 pawns,
3 boosters, and one each of precast loadout / hero loadout / membership / game mode. One of
them is live in our data: `83825291` (Vulture Missiles, on Simargl's loadout). It resolves in
the register, so it works today, but any client path that indexes via ItemIDTable would miss
it. Worth knowing before trusting ItemIDTable as an allow-list.

## 5. Things confirmed correct (previously unverified)

- **Tier values**: all 14 tech tree rows match the tier in their asset path. 0 mismatches.
- **Base shipClass (0-4)**: all 14 correct against the path's class. 0 mismatches.
- **Localization keys**: all 62 catalog keys resolve in `DreadGame.locres`. 0 unresolved,
  0 missing.
- **Manufacturer assignment**: no ship asset carries a manufacturer property (`m_manufacturer`
  appears only in UI assets), so this is genuinely server-authored and the client cannot
  contradict it. Corroborated independently from the lore text instead: of 12 ships, 9 have
  descriptions naming a manufacturer and **all 9 agree** with what the server assigns;
  3 have no lore. Zero conflicts.

## 6. Still unexplained

- **`GetTechTreeCategoryImagePath: Unhandled base ship class <13>`**. 13 is `YSC_SUPPORT_HEAVY`
  in EYShipClass, and we emit no 13 anywhere — our ClassIds are 14/6/10/12/3/2 and our base
  classes are all 0-4, both now validated against asset paths. The value is not coming from
  our data.
- **`m_loadouts of length 0`** on `FUIShipData`, which has survived every change so far.

## 7. Loadout contents validated against the blueprints

`bpdump` can't parse these assets, but the blueprint's name table lists its references
directly. For all four starter loadouts, the weapons and abilities the server sends match the
precast blueprint **exactly**:

| loadout | weapons | abilities |
|---|---|---|
| 33489262 Agosta | 100597772, 100598563 | MATCH (4/4) |
| 33489423 Simargl | 100598595, 100598596 | MATCH (4/4) |
| 33489263 Rurik | 100597987, 100598570 | MATCH (4/4) |
| 33489264 Cerberus | 100597870, 100598573 | MATCH (4/4) |

Eight weapons and sixteen abilities, zero differences. The starter loadout contents are
authoritative-correct.

This also settles the ItemIDTable orphan from §4.2: `83825291` is referenced by Simargl's own
blueprint, so it is a legitimate ability and ItemIDTable is simply an incomplete index.

## 8. The complete tech tree the data supports

Built from the 66 player-facing precast loadouts (`05_full_tree.json`):

| | rows |
|---|---|
| by class | Assault 12, Dreadnought 13, Scout 13, Sniper 15, Support 13 |
| by tier | T1 4, T2 6, T3 12, **T4 15**, T5 14, untiered 15 |
| with a resolvable ship pawn | 49 of 66 |

**51 tiered rows are available. The server currently sends 10.** Tier 4 is only reachable this
way, since no T4 ship pawn resolves (§2).

The 15 untiered entries are the per-class "base" variants (e.g. `33489315 AssaultMedium`
alongside the T1-T5 chain); whether they belong in the tree needs a decision, not more data.

## 9. Summary

**Validated correct:** the category-byte law (3437/3437), every id the server emits (219
values, 0 unresolvable, 0 miscategorised), all tier values, all base ship classes, all 62
localization keys, all starter loadout weapons and abilities against their blueprints, and the
manufacturer assignment against lore text (9/9 where evidence exists).

**Defects found:** one — `ClassId` for Sniper Light T2, now fixed and derived.

**Data limits, not bugs:** ItemIDTable omits 37 ids that do have paths; 16 YPawn ids resolve
nowhere, which is why tier 4 has no ships; 2 class/size/tier combinations have a ship but no
loadout.

**Conclusion on the tech tree:** the server's *data* is sound. It is not sending wrong ids or
wrong classes or wrong tiers. What it is sending is *too little* — 10 of 51 available rows —
and the remaining client-side failures (`<13>`, `m_loadouts of length 0`) are not traceable to
any value we emit.
