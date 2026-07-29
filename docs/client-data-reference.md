# Dreadnought client data: complete validated reference

Purpose: a ground-truth reference for what the client actually contains, so server data that
was invented rather than extracted can be identified. Everything here was derived from client
files and checked; where something could not be verified, it says so explicitly.

Sources: `data/assets/{ItemIDRegister,ItemIDTable,ItemIDConversionTable,CatalogIDTable}.json`,
`Content/Localization/DreadGame/en/*.locres`, the cooked `.uasset` tree, and
`DreadGame-Win64-Shipping-pristine.exe`.

Companion data in this directory:
`01_tables.json`, `02_ships_loadouts.json`, `03_server_ids.json`, `04_master.json`,
`05_full_tree.json`, `06_hero.json`, `07_fields.json`, `08_absent_fields.json`,
`09_absent_verified.json`.

---

## 1. The category law

**The top byte of an item id IS its `ItemIDTable` CategoryID.** 3437 ids checked, **0
disagreements**.

    categoryByte(id) = (id >> 24) & 0xff

Any synthetic id the server invents therefore *claims* a category. This is not cosmetic: the
client's tech-tree admission gate tests exactly this byte.

| CategoryID | Category | ids | with path | named¹ |
|---|---|---|---|---|
| 1 | YShipLoadoutPrecast | 283 | 259 | — |
| 3 | YShipLoadoutHero | 64 | 57 | 13 |
| 4 | YAbility | 553 | 521 | 413 |
| 5 | YWeapon | 521 | 453 | 98 |
| 6 | YPerk | 41 | 41 | 21 |
| 10 | YPawn | 219 | 203 | 0 |
| 20 | YShipVanityMeshPart | 532 | 466 | 0 |
| 21 | YShipVanityEmblem | 102 | 91 | 39 |
| 22 | YShipVanityPaint | 250 | 228 | 128 |
| 23 | YShipVanityPattern | 197 | 189 | 0 |
| 24 | YShipVanityDecal | 288 | 267 | 135 |
| 30 | YBoosterAssetBase | 5 | 3 | — |
| 31 | YGoldMembership | 8 | 8 | — |
| 35-37 | YProgressionUnlockContainer{Blank,Currency,Function} | 4 | 4 | — |
| 50 | YCharacterCustomizationMaterial | 68 | 63 | 33 |
| 51 | YCharacterCustomizationMesh | 262 | 217 | 58 |
| 52 | YCharacterCustomizationGender | 2 | 2 | — |
| 53-55 | TextureSet / ColorPalette / MaterialPalette | 37 | 0 | — |
| 80-82 | YMenuNavigation{Item,Section,SlotBase} | 29 | 19 | — |
| 99 | YGameMode | 32 | 18 | — |

¹ has an authoritative display name in `ItemIDConversionTable`.

**`ItemIDTable` is NOT a complete index.** 37 ids have register paths but no category entry
(18 weapons, 8 abilities, 4 pawns, 3 boosters, 1 each precast/hero/membership/gamemode). One is
live in our data: `83825291` (Vulture Missiles), which Simargl's own blueprint references.
Never use ItemIDTable as an allow-list.

## 2. Asset paths are the schema

Paths encode class, size and tier, so mappings should be **derived, never hand-written**. Every
hand-written table audited here had drifted.

    ship          /Game/Generic/Ships/<Class>/<Size>/T<n>/VH_..._Pawn_T<n>_BP
    precast       /Game/Generic/Loadouts/Precast/[T<n>/]VH_<Class><Size>_[T<n>_]PrecastLoadout_BP
    hero          /Game/Generic/Loadouts/Hero/VH_<Class><Size>_<Name>_HeroLoadout_BP
    ability       /Game/Generic/Abilities/<Class>/...
    ship vanity   /Game/Generic/VanityItems/{_Shared,Patterns,Heroships,ThemedShips}/...
    character     /Game/Generic/VanityItems/_Shared/...
    perk          /Game/Generic/Officer/Perk/...

- 57 ship pawns under `/Ships/`; **50** map to a precast loadout.
- 66 player-facing precast loadouts (of 283; rest are Havoc / AI-boss / Development).
- **No tier-4 ship pawn resolves.** Ship tiers are {1:4, 2:7, 3:12, 5:15}; 16 YPawn ids have
  no path and no conversion entry. All 15 tier-4 *loadouts* do exist, so a tree keyed on
  loadout ids reaches T4 and one keyed on pawns cannot.

## 3. The real tech tree, with authoritative names

66 rows (`05_full_tree.json`), 65 named. T- is the untiered base variant.

| class/size | T1 | T2 | T3 | T4 | T5 |
|---|---|---|---|---|---|
| AssaultMedium | **Agosta** | **Trafalgar** | Otranto | Vigo | Athos |
| AssaultHeavy | — | — | Kama | Blud | Gora |
| AssaultLight | — | — | — | Vindicta | — |
| DreadnoughtMedium | *(unnamed)* | **Nav** | Chernobog | Voronezh | Zmey |
| DreadnoughtHeavy | — | — | — | Jutland | Monarch |
| DreadnoughtLight | — | — | Gravis | Lorica | Invictus |
| SniperMedium | **Rurik** | **Tugarin** | Vucari | Murometz | Svarog |
| SniperHeavy | — | — | Ballista | Onager | Grenada |
| SniperLight | — | **Furia** | Virtus | Nox | Stabia |
| SupportMedium | **Cerberus** | **Orcus** | Ceres | Aion | Feronia |
| SupportHeavy | — | — | — | Koschei | Ohkta |
| SupportLight | — | — | Palos | Harwich | Cattaro |
| ScoutMedium | — | — | Fulgora | Medusa | Mithras |
| ScoutHeavy | — | — | Kreshnik | Perun | Netron |
| ScoutLight | — | **Dover** | Lerwick | Valcour | Bakar |

**51 tiered rows exist. The server currently sends 10.**

## 4. Hero ships

47 hero ids in the server, **all 47 real** and all names match their asset filename exactly.
But asset filename != display name. Where an authoritative name exists (13 of them), three
differ:

| id | asset filename says | actual display name |
|---|---|---|
| 67043329 | Skagerrak | **Huscarl** |
| 67043330 | FallofTroy | **Fall of Troy** |
| 67043338 | JunkyardPrince | **Junkyard Prince** |

The other 34 have no conversion-table entry, so their display names are unknown — the
filename-derived names are placeholders, not verified.

---

# SERVER AUDIT

## 5. Id literals: 28 with no client counterpart

183 distinct id-shaped literals in the Go source; 155 exist in client data. Of the 28 that
don't, most are benign (test sentinels like `9999999`/`123456789`, conversion-table OldItemIDs
`1000001-5`, CatalogIDTable bundle ids `99930001-3`). Two clusters are real problems:

### 5.1 `realCatalogBucketIDBase` — synthetic ids that claim a real category
`mmogbrain/gateway_catalog.go:414-425` assigns bucket bases `19000000` … `31000000`. Every one
has **top byte 1**, so by §1 the client reads them as `YShipLoadoutPrecast`. A synthetic
"Heroships" id at `29000000` is announced to the client as a precast loadout. Synthetic ids
must be chosen so their top byte is an unused category, or not used at all.

### 5.2 `legacyStarterShipItemAliases` — invented ids and a wrong name
`legacy-api/handlers/inventory_bootstrap.go:118-127`:

    "assault medium t1"     -> "Athos_T1",  "16777223"
    "dreadnought medium t1" -> "Akula_T1",  "16777225"
    "support medium t1"     -> "Lorica_T1", "16777231"

- `16777223`/`16777225`/`16777231` are `0x01000007/09/0F` — **not in the client data at all**.
- The T1 assault ship is **Agosta**, not Athos (Athos is AssaultMedium T5/untiered).
- **"Akula" is the manufacturer, not a ship.** DreadnoughtMedium T1 is Simargl.
- Lorica is real but is **DreadnoughtLight T4**, not support medium T1 (that is Cerberus).
- Sniper medium T1 (Rurik) is missing from the switch entirely.

## 6. Names: the T2 roster is wrong

`t1t2TechTreeShips` / `lockedT1Ships` name the T2 ships Leipzig, Trieste, Valcour and Ceres.
Neither "Leipzig" nor "Trieste" is a localized display string anywhere in the game. The real
names (§3):

| our name | actual T2 name | where our name really belongs |
|---|---|---|
| Leipzig | **Trafalgar** | nowhere — not a game string |
| Trieste | **Nav** | nowhere — not a game string |
| Valcour | **Dover** | Valcour is ScoutLight T4/untiered |
| Ceres | **Orcus** | Ceres is SupportMedium **T3** |

T1 names (Agosta, Rurik, Cerberus) are correct. Ship-pawn rows carry placeholder names like
"Assault Medium T1"; real names belong to the loadout, not the pawn.

## 7. 56 emitted field names exist nowhere in the client

Of 355 field names the server sends via `protocol.Append*Field`, **58 are absent from the
exe**. Two of those (`CurrentXP`, `RewardItem`) do appear in `.uasset` files, so they may be
Blueprint-read and are **not** condemned. The remaining **56 appear in neither the executable
nor any Blueprint asset** — nothing can read them.

Grouped by payload:

- **Tech tree rows (old schema):** `NodeID`, `ParentID`, `PrereqID1`, `PrereqID2`,
  `UnlockCost`, `bIsUnlocked`, `bIsPurchased`, `bIsNew`, `techTreeRowCount`
- **Player progression:** `CurrentRank`, `RankXP`, `XPToNextRank`, `NumUnlockedShips`
- **Fleet:** `FlagShipLoadoutID`, `flagshipShipId`, `fleet id`, `bIsFlagship`,
  `selectedLoadoutIndex`
- **Matchmaking:** `matchmakingStatus`, `queueId`, `serverIP`, `entry_id`
- **Purchases / currency:** `pricePaid`, `premiumCurrency`, `premiumCreditsGained`,
  `xpConverted`, `eliteDays`
- **Contracts:** `contractID`, `rewardXP`, `rewardGP`, `rerollCost`
- **Stats:** `counterValue`, `CompositeName`, `IsPercent`
- **PvE / Havoc** (already known dead): `AIBehavior`, `BossFrequency`, `BossID`, `BossKills`,
  `BossKillsReq`, `CreditMult`, `DamageMult`, `DifficultyID`, `EffectType`, `EffectValue`,
  `EliteCount`, `EnemyCount`, `FirstKill`, `HealthMult`, `HighestWave`, `KillCount`, `MinWave`,
  `RewardGP`, `RewardXP`, `TotalWaves`, `WaveStart`, `XPMult`

Caveat: absence proves *that name* is never looked up. It does not prove the payload is
pointless — the client may read a differently-named field for the same purpose. Treat each as
"this name is fiction", then find the real one.

## 8. Validated correct

- Every id the server emits: 219 values across tech tree, fleets, loadouts, catalog and
  inventory. 0 unresolvable, 0 miscategorised.
- All four starter loadouts' weapons and abilities match their precast blueprint's own
  references exactly — 8 weapons, 16 abilities, 0 differences.
- All 14 tech tree Tier values match their asset path. All 14 base `shipClass` values match.
- All 62 catalog localization keys resolve in `DreadGame.locres`.
- `TechTrees` blob framing: bare zlib, **no** length prefix, confirmed against the
  decompressor `FUN_142a4c430` (sets `next_in` at offset 0, `inflateInit_` with "1.2.5").
- Ship→manufacturer *assignment*: 9 of 12 ships have lore text naming a maker; all 9 agree.

## 9. NOT validated / unknown

- **Manufacturer id numbering** (`0=JupiterArms, 1=AkulaVektor, 2=Oberon`). No
  `EYManufacturer` enum exists; `HandleManufacturerClicked` takes a bare int32; the
  manufacturers screen's name table is alphabetical so it encodes no order. This is still a
  guess.
- **Whether the tech tree loader runs at all.** `InitializeTechTreeMmogClient` is a UFunction
  and the document is read from `mmogInterface+0x40a0`; nothing confirms that offset is ever
  written. Until this is settled, field-level changes to the tech tree cannot be evaluated.
- Display names for 34 of 47 hero ships.
- `Position` and `Wires` semantics in the tech tree document.
- Vanity item names: 0 of 532 mesh parts and 0 of 197 patterns have authoritative names.
