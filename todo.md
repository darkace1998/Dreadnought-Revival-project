# TODO — Dreadnought Private Server

> Last updated: 2026-07-02

---

## Phase 1: DataTable Loading Infrastructure (A1-A6)
> Build the foundation for loading 176 extracted JSON DataTables.

- [x] **A1**: Create `shared/dreadgameconfig/loader.go` — generic JSON DataTable parser (`{"rows": {"RowName": {...}}, "row_count": N}`)
- [x] **A2**: Add `DATA_DIR` env var support (default: `../data/`) with fallback to embedded hardcoded data
- [x] **A3**: Copy/symlink the 176 extracted DataTable JSONs into `data/datatables/`
- [x] **A4**: Copy the 4 asset management JSONs from `test/` into `data/assets/`
- [x] **A5**: Copy `LoadoutDevelopmentTable.json` from `DreadGame/Config/` into `data/loadouts/`
- [ ] **A6**: Add loader unit tests — parse each of the 176 files without error, validate row counts match

---

## Phase 2: Weapons Data (B1-B5)
> Load 226 weapon definitions with 50+ stat fields each.

- [ ] **B1**: Define Go struct `WeaponStats` matching `DN_Weapons_OTS_DT.json` fields (damageHigh/Med/Low, cooldown, spread, ammo, speed, energyCost, hitzone multipliers)
- [ ] **B2**: Load `DN_Weapons_OTS_DT.json` into `map[int32]WeaponStats` keyed by ItemID
- [ ] **B3**: Expose `WeaponByID(id) WeaponStats` and `AllWeapons() []WeaponStats` accessors
- [ ] **B4**: Wire weapon stats into `YA_GetTechTree` and store catalog payloads
- [ ] **B5**: Add tests — verify all 226 weapons load, spot-check damage/cooldown values against `lookup_tables.md`

---

## Phase 3: Projectiles Data (C1-C5)
> Load 393 projectile definitions + 175 offline missile variants.

- [ ] **C1**: Define Go struct `ProjectileStats` matching `DN_Projectile_OTS_DT.json` fields
- [ ] **C2**: Load `Projectiles/DN_Projectile_OTS_DT.json` (393 rows)
- [ ] **C3**: Load `Projectiles/YProjectileMissile_Offline_DT.json` (175 rows)
- [ ] **C4**: Link projectiles to weapons via weapon-to-projectile reference fields
- [ ] **C5**: Add tests — verify projectile count, validate weapon→projectile linkage

---

## Phase 4: Ship Feats (D1-D6)
> Load 75 ship feat tables with modifier DSL parsing.

- [ ] **D1**: Define Go struct `ShipFeat` matching `ShipFeats/*.json` fields (m_enabling, m_triggers, m_effects DSL)
- [ ] **D2**: Build feat DSL parser — parse modifier strings like `AM(PawnDamageModifier +75%) :Stacks(1): D(10.0) : Buff(FirepowerIncrease)`
- [ ] **D3**: Load all 75 `ShipFeats/*.json` files into `map[shipID][]ShipFeat`
- [ ] **D4**: Expose `FeatsForShip(shipID) []ShipFeat` accessor
- [ ] **D5**: Wire ship feats into tech tree rows and fleet data payloads
- [ ] **D6**: Add tests — verify all 75 feat tables load, validate modifier parsing

---

## Phase 5: Abilities Data (E1-E6)
> Load 103+ abilities from 24 DataTable files.

- [ ] **E1**: Define Go struct `AbilityStats` matching ability DataTable fields (cooldown, activeTime, duration, damage, tier scaling)
- [ ] **E2**: Load all 24 `Abilities/*.json` files into unified ability map
- [ ] **E3**: Cross-reference abilities with ItemIDRegister to resolve ItemID→asset path
- [ ] **E4**: Expose `AbilityByID(id) AbilityStats` and `AllAbilities() []AbilityStats` accessors
- [ ] **E5**: Wire ability stats into tech tree, store catalog, and loadout payloads
- [ ] **E6**: Add tests — verify ability count, validate cooldown/damage values

---

## Phase 6: Officers & Perks (F1-F7)
> Load 21 officer cards and perk system data.

- [ ] **F1**: Define Go struct `OfficerCard` matching `DN_Officers_OTS_DT.json` fields (trigger type, effect DSL, conditions)
- [ ] **F2**: Load officers table (21 rows) with trigger/effect parsing
- [ ] **F3**: Wire officer data into `YA_PlayerGet` Officers array (replace current empty synthetic data)
- [ ] **F4**: Define Go struct `Perk` for perk DataTable fields
- [ ] **F5**: Load perk data from ItemIDTable category `YPerk` entries
- [ ] **F6**: Wire perks into tech tree and store catalog
- [ ] **F7**: Add tests — verify 21 officers load, validate trigger types

---

## Phase 7: Energy Shields & Global Tuning (G1-G6)
> Load shield mechanics and global game balance values.

- [ ] **G1**: Define Go struct `EnergyShieldStats` matching `DN_EnergyShields_DT.json` fields (per-class shield damage modifiers, pass-through factors)
- [ ] **G2**: Load energy shields table
- [ ] **G3**: Define Go struct `GlobalTuning` matching `DN_GlobalTuningValues_DT.json` fields (AFK timer, projectile speed modifier, reveal range)
- [ ] **G4**: Load global tuning table
- [ ] **G5**: Expose tuning values for use by matchmaking and game balance calculations
- [ ] **G6**: Add tests — verify shield modifiers sum correctly, validate tuning constants

---

## Phase 8: Asset Management & Item Registry (H1-H7)
> Load 4 asset lookup tables (~8,800 entries) and replace hardcoded catalog.

- [ ] **H1**: Load `test/ItemIDTable.json` (10,661 lines, 27 categories, ~4,000+ item IDs) into category→itemID map
- [ ] **H2**: Load `test/ItemIDRegister.json` (12,349 lines, 3,086 entries) into itemID→assetPath map
- [ ] **H3**: Load `test/CatalogIDTable.json` (6,692 lines, 12 catalog buckets) into catalog bucket data
- [ ] **H4**: Load `test/ItemIDConversionTable.json` (36,482 lines, 1,616 entries) into oldItemID→newItemID map
- [ ] **H5**: Replace hardcoded `itemCatalog` in dreadgameconfig with data from loaded asset tables
- [ ] **H6**: Verify all 66 currently hardcoded items resolve correctly via the new loader
- [ ] **H7**: Add tests — verify item counts per category, validate ID→path mappings

---

## Phase 9: Loadout Development Table (I1-I5)
> Load ~100+ hero ship loadout definitions.

- [ ] **I1**: Define Go struct `DevLoadout` matching `LoadoutDevelopmentTable.json` fields (ShipID, weapon/ability/perk slot ItemIDs)
- [ ] **I2**: Load loadout development table (~100+ hero ship loadouts)
- [ ] **I3**: Cross-reference loadout slot ItemIDs with weapon/ability/perk data from phases B/E
- [ ] **I4**: Wire into `YA_PlayerFleets` and `YA_RequestStaticFleetData` as precast loadout references
- [ ] **I5**: Add tests — verify loadout count, validate slot ItemIDs resolve to known items

---

## Phase 10: Progression Tables (J1-J5)
> Load rank data, game modifiers, and match statistics definitions.

- [ ] **J1**: Load `Progression/Ranks/DN_Ranks_Player.json` — replace hardcoded 51-rank ladder with extracted data
- [ ] **J2**: Load `Progression/GameModifiers/DN_GameModifiers_DT.json` — game mode tuning values
- [ ] **J3**: Load `Progression/DN_PlayerMatchStatistics.json` — match stat category definitions
- [ ] **J4**: Verify rank names/thresholds match current hardcoded values
- [ ] **J5**: Add tests — verify rank count, validate game modifier fields

---

## Phase 11: PvE Tables (K1-K5)
> Load Havoc boosts/modifiers/rewards and PvE scoring tables.

- [ ] **K1**: Load `Progression/Havoc/` — 7 files (boosts:38, modifiers:26, bossWaves:4, rewards:7, loadouts, enemyModifiers, unlockables)
- [ ] **K2**: Load `PVE/` — 14 files (kill scoring, wave scoring, defend scoring, medal scoring, objectives, seasons, events, tutorial)
- [ ] **K3**: Replace hardcoded Havoc modifier/boost/reward data in mmogbrain with loaded table data
- [ ] **K4**: Wire PvE scoring tables into match result processing
- [ ] **K5**: Add tests — verify boost/modifier counts match (38 boosts, 26 modifiers)

---

## Phase 12: UI & Miscellaneous Tables (L1-L7)
> Load UI configs, level streaming, and misc gameplay tables.

- [ ] **L1**: Load `UI/` — 16 files (HUD colors, energy wheel config, onboarding, quest markers, short commands, module data:1,237 rows)
- [ ] **L2**: Load `CachedData/DN_ShipMeshCache_DT.json` — ship mesh references
- [ ] **L3**: Load `FeatEffectFeedback/` — feat effect feedback definitions
- [ ] **L4**: Load `BuffPod/` — buff pod feat definitions
- [ ] **L5**: Load `LevelStreaming/` — 13 per-map level streaming configs
- [ ] **L6**: Validate loaded data doesn't break any existing payloads
- [ ] **L7**: Add tests — verify file counts per directory

---

## Phase 13: Integration & Migration (M1-M8)
> Wire all loaded data into payloads and remove hardcoded stubs.

- [ ] **M1**: Update `YA_GetTechTree` to use loaded weapon/ability/ship feat data instead of hardcoded stubs
- [ ] **M2**: Update `YA_PlayerFleets` to use loaded loadout development data
- [ ] **M3**: Update `YA_RequestStaticFleetData` to use loaded ship feat data
- [ ] **M4**: Update store catalog (`gateway_catalog.go`) to use loaded item registry for full item list
- [ ] **M5**: Update `YA_GetShipBonuses` to use loaded feat modifier data
- [ ] **M6**: Remove hardcoded `itemCatalog` from `dreadgameconfig/data.go` after all loaders verified
- [ ] **M7**: Update payload size snapshots in `sizecheck_test.go` after data changes
- [ ] **M8**: Full regression — all mmogbrain tests pass with loaded data

---

## Phase 14: Damage & Defense System
> Implement energy wheel, shields, armor, and damage types.

- [ ] **Energy wheel**: 4-way (Off, Thrusters, Shields, Weapons) with per-class effects and exact modifiers
- [ ] **Energy shields**: DN_EnergyShields_DT, separate from hull, regenerating
- [ ] **Armor system**: Standard, Kinetic Amplifier, Armorbooster, Armored Lockdown with damage resistance values
- [ ] **Damage types**: 15+ types with tier variants (Kinetic, Energy, Explosive, Armorbreaker, Disruptor, Drain, Stasis)
- [ ] **Damage formula**: Final = Base x DamageMod x ArmorMod x ShieldMod
- [ ] **Hull damage states**: Hull_Damaged, Hull_Severely_Damaged, Hull_Critical_Damaged

---

## Phase 15: Scoring & Rating System
> Implement TrueSkill-like rating and comprehensive stat tracking.

- [ ] **TrueSkill-like rating**: Starting skill 220, max 500, deviation 40, base change 10, conservative constant 3
- [ ] **Team rating update**: Every 240 seconds
- [ ] **15 stat categories**: Kills, Assists, Double Kills, Weapon Damage by class, Energy Used, Damage with Modules, Healing Done, Control Points, etc.
- [ ] **XP pools**: 10 types (Scoring, ScoringBase, ScoringPerformance, BoosterWin, FirstWinOfTheDay, GoldMembership, TeammatesGoldMembership, BattleReadyRecruit/Veteran/Legendary)
- [ ] **Credits pools**: 13 types (RankUp + same as XP pools)
- [ ] **Scoring formula**: YScoringFormulaParameters with veteranMultiplier, premiumMultiplier, premiumShipXPPercentage, etc.
- [ ] **End-of-match stat comparison**: Top match, top team, above average, above career average, pure stats (max 5 stats shown)

---

## Phase 16: Additional Game Modes
> Add 8 missing game modes beyond the current 3 PvP + 3 PvE.

- [ ] **Pod TDM**: GameMode_PodTDM_BP
- [ ] **Turbo TDM**: GameMode_Turbo_TDM_BP
- [ ] **Training mode**: YMSS_PLAY_TRAINING
- [ ] **Tutorial mode**: YMSS_PLAY_TUTORIAL
- [ ] **Coop Havoc**: Cooperative variant of Havoc mode
- [ ] **Coop PVE**: Standard cooperative PvE
- [ ] **Bootcamp**: GameInfo_BC_BP (training scenario)
- [ ] **Benchmark mode**: GameMode_Benchmark_BP (performance test)

---

## Phase 17: Chat & Social Systems
> Implement real chat channels, presence, and squad management.

- [ ] **Real chat channels**: Team, squad, global, language-specific channels with actual message routing
- [ ] **Presence system**: Online/offline/away status, friend list, pending friends
- [ ] **Squad system**: Real squad state management (invite, accept, leave, kick, promote)
- [ ] **Squad XP/credit bonuses**: Elite status bonuses for squad members

---

## Phase 18: Vanity & Cosmetics
> Add ship and character customization systems.

- [ ] **Paints**: Ship color customization
- [ ] **Decals**: Ship surface graphics
- [ ] **Emblems**: Faction/personal emblems
- [ ] **Patterns**: Repeating surface patterns
- [ ] **Coatings**: Material finish customization
- [ ] **Character customization**: Material, mesh, gender (YCharacterCustomization*)
- [ ] **Founders packs**: 8+ special cosmetic bundles

---

## Phase 19: Achievements
> Implement 34 Steam achievements.

- [ ] **34 Steam achievements**: BixsRightHand, SinleyBayRecruit/Veteran/Legend, JackOfAllTrades, MVP, HungryForMore, HelpingHand, DeathmatchVet, EliminationVet, Unbreakable, CryHavoc, OnslaughtVeteran, VeteranCaptain, LegendaryCaptain, TopOfTheLine, PuttinOnTheRitz, PimpYourRide, etc.

---

## Phase 20: Fleet Progression
> Add fleet XP sharing and battle bonuses.

- [ ] **Fleet XP sharing**: All ships in fleet earn XP together
- [ ] **Battle bonus**: Fleet-wide bonus, resettable with Credits
- [ ] **Elite bonuses**: Extra contract slot, XP/Credits bonus (self + allies), applied before Battle Bonus

---

## Phase 21: Item Drop System
> Implement loot drops from matches.

- [ ] **YPlayerItemDropCycleMP**: 1 drop per cycle, max 20 checks, max 1 simultaneous unclaimed item
- [ ] **DN_MPItemDrops**: Loot table integration

---

## Phase 22: Season & Event System Enhancements
> Expand season/event support with full episode data.

- [ ] **6 seasons**: S1-S6 with full episode data (20+ episodes)
- [ ] **Reward tiers**: BRONZE, SILVER, GOLD per season
- [ ] **Seasonal cosmetics**: Body sets (Autumn, Spring, Summer, Winter, Explorer, Jovian, NanoDoc), decals, paints
- [ ] **PvE events**: DN_Events_DT integration
- [ ] **Boss battles**: BunBunBoss (S3), DreadnoughtHeavyBoss (S4), Multiple bosses (S6)
- [ ] **Medal scoring**: PvEMedalScoring DataTable

---

## Phase 23: AI System Enhancements
> Improve AI with behavior trees and advanced mechanics.

- [ ] **AI behavior trees**: 22 BT nodes, blackboard system
- [ ] **AI ability activation**: DN_AIAbilityActivation_DT (21 rows) with target/damage/distance/threat/probability gates
- [ ] **AI state machine**: YCSB_NONE, YCSB_ATTACK, etc.
- [ ] **PvE spawn logic**: EnemyGroupSpawn_Component, CreepFactory, triggers, managers
- [ ] **AI combat scene manager**: YAICombatSceneManager
- [ ] **Bot configurations**: DN_ClientBots_DT
- [ ] **Battle ready**: DN_BattleReadyUpdate_DT

---

## Phase 24: Custom Match Enhancements
> Add real lobby state management for custom matches.

- [ ] **Real lobby state**: Actual room state management (not just success responses)
- [ ] **Custom settings**: Game mode, map, player limits, team sizes
- [ ] **Fleet select**: Real fleet selection flow in custom matches

---

## Phase 25: Market & Economy Enhancements
> Add featured items, bundles, and live store features.

- [ ] **Featured items**: Rotating daily/weekly store offers
- [ ] **Spotlight items**: Highlighted/promoted items
- [ ] **Bundle system**: GetMarketBundlesRequestDefinition
- [ ] **Live tiles**: Dynamic UI elements in store

---

## Phase 26: Remaining YA_* Handlers
> Complete ~15 less common request types.

- [ ] **~15 less common request types**: Complete remaining YA_* handlers that currently return generic success

---

## Phase 27: Server→Client Notifications
> Implement real notification payloads.

- [ ] **Achievements updated**: YA_AchievementsUpdated with actual achievement data
- [ ] **User status**: YA_UserStatus with real presence data
- [ ] **Fleet charged**: YA_OnFleetCharged with actual fleet state

---

## Phase 28: Test Coverage
> Add tests for 5 services with 0 coverage.

- [ ] **auth-server**: Add tests for login, register, logout, ban/unban, JWT validation
- [ ] **admin-cli**: Add tests for CLI commands
- [ ] **dn-launcher**: Add tests for identity generation, auth flow, config parsing
- [ ] **game-manager**: Add tests for instance lifecycle, port pool, spawner
- [ ] **master-server**: Add tests for server registry, heartbeat, stale cleanup
- [ ] **shared/db**: Add tests for Open, Migrate, schema versioning
- [ ] **shared/logging**: Add tests for logger creation, service hook
- [ ] **shared/middleware**: Add tests for JWT middleware, rate limiter

---

## Phase 29: Medium Issues (29 outstanding)
> Fix remaining medium-priority bugs and code quality issues.

- [ ] **M1**: shared/db — Migrations not transactional (DDL + schema_versions insert not atomic)
- [ ] **M2**: shared/middleware — JWTMiddleware claims RS256 support but only HMAC implemented
- [ ] **M3**: shared/middleware — Claims passed via request headers instead of context.WithValue
- [ ] **M4**: auth-server — Brittle authHeader[7:] stripping (mmogbrain, legacy-api)
- [ ] **M5**: auth-server — Duplicate username detection via error-string matching (brittle)
- [ ] **M6**: auth-server — prometheus incorrectly marked `// indirect` in go.mod
- [ ] **M7**: auth-server — Zero test files
- [ ] **M8**: legacy-api — PostMatchResult no transaction (partial data committed on mid-loop failure)
- [ ] **M9**: legacy-api — GetInventory defers rows.Close() then explicitly calls it again
- [ ] **M10**: legacy-api — No request body size limit on PostMatchResult
- [ ] **M11**: legacy-api — No DB health check in /health endpoint
- [ ] **M12**: game-manager — DELETE returns 404 when process already dead
- [ ] **M13**: game-manager — No input validation on POST /instances (unbounded player list)
- [ ] **M14**: game-manager — List() returns pointers to internal state (data race risk)
- [ ] **M15**: game-manager — Zero test files
- [ ] **M17**: gateway — promhttp incorrectly marked `// indirect`
- [ ] **M18**: gateway — 9.8 MB compiled binary committed to repo
- [ ] **M19**: gateway — Zero test files
- [ ] **M20**: dn-launcher — HTTP response status code not checked
- [ ] **M21**: dn-launcher — No response body size limit (memory DoS)
- [ ] **M22**: dn-launcher — Player ID deterministic from hostname+username (weak identity)
- [ ] **M23**: dn-launcher — Corrupted player.json silently regenerates identity
- [ ] **M24**: dn-launcher — Zero tests
- [ ] **M25**: admin-cli — URL path injection in stopInstance() (unsanitized ID)
- [ ] **M27**: admin-cli — io.ReadAll errors silently swallowed (x3)
- [ ] **M28**: admin-cli — Zero tests
- [ ] **M29**: master-server — go.sum corrupted (hashes for x/sys v0.13.0 but go.mod declares v0.35.0)
- [ ] **M31**: master-server — RowsAffected() errors silently discarded
- [ ] **M32**: master-server — Zero test files

---

## Phase 30: Polish Items
> Final cleanup and LOW issue resolution.

- [ ] **M4**: Context.WithValue migration for JWT claims
- [ ] **L2**: Remove unused auth-server models (Session, Ban)
- [ ] **L3**: Sessions table TTL cleanup already done — verify
- [ ] **L4-L15**: Various LOW issues from issues.md

---

## Blocked / Needs Investigation

- [ ] **Game binary certificate pinning**: Firmament (FUN_142aa3e00) — may need binary patching
- [ ] **RC4 stream cipher compatibility**: Patched vs unpatched binary
- [ ] **EAC bypass stability**: Easy Anti-Cheat bypass
- [ ] **WER proxy deployment**: Place `bin/wer-proxy/wer.dll` beside `DreadGame-Win64-Shipping.exe` for client diagnostics (requires approval to modify `/root/projects/src/Dreadnought`)

---

## Quick Reference

**Current state:**
- All 8 services build, 0 lint issues
- All tests pass (mmogbrain, legacy-api, shared, gateway)
- 24/24 CRITICAL+HIGH resolved, 3/32 MEDIUM resolved, 15/15 LOW resolved
- ~114 YA_* handlers dispatched, ~45 with dedicated payload builders
- Phases 1-6 complete: hangar, matchmaking, progression, market/economy, PvE/AI
- Feature coverage: ~30%

**Next priorities:**
1. Phase 1: DataTable loader infrastructure (A1-A6)
2. Phase 2: Weapons data (B1-B5)
3. Phase 8: Asset management & item registry (H1-H7)
4. Phase 5: Abilities data (E1-E6)
5. Phase 13: Integration & migration (M1-M8)
6. Phase 14: Damage/defense system
7. Phase 15: Scoring/rating system
8. Phase 16: Additional game modes
