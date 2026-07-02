# TODO — Dreadnought Private Server

> Last updated: 2026-07-02

---

## Critical Priority (Gameplay-Breaking)

### DataTable Loading & Game Data
- [ ] **Load 379+ DataTables** from extracted JSON into server config (biggest single unlock)
- [ ] **Ship data**: 75+ ship configs with full stats, 50+ hero ships, 3 manufacturers (Jupiter Arms, House Oberon, Akula Vektor)
- [ ] **Weapon data**: 50+ weapon types, 226 DataTable rows, 60+ stat fields (damage, range, spread, projectile physics)
- [ ] **Ability data**: 103+ abilities with cooldowns, tiers, 402+ modifiers (damage, defensive, mobility, deployable, special)
- [ ] **Officer cards**: 21 officers with triggers (OnEnable, DoKill, BeHitPerHitPoint, OnAbilityFinishedCooldown) and AM/RM modifiers
- [ ] **Perk system**: YPerkManager, PerksUnlock_DT, PerksUnlockingChallenges_DT (offensive, defensive, utility, class-specific)

### Damage & Defense System
- [ ] **Energy wheel**: 4-way (Off, Thrusters, Shields, Weapons) with per-class effects and exact modifiers
- [ ] **Energy shields**: DN_EnergyShields_DT, separate from hull, regenerating
- [ ] **Armor system**: Standard, Kinetic Amplifier, Armorbooster, Armored Lockdown with damage resistance values
- [ ] **Damage types**: 15+ types with tier variants (Kinetic, Energy, Explosive, Armorbreaker, Disruptor, Drain, Stasis)
- [ ] **Damage formula**: Final = Base x DamageMod x ArmorMod x ShieldMod
- [ ] **Hull damage states**: Hull_Damaged, Hull_Severely_Damaged, Hull_Critical_Damaged

### Scoring & Rating System
- [ ] **TrueSkill-like rating**: Starting skill 220, max 500, deviation 40, base change 10, conservative constant 3
- [ ] **Team rating update**: Every 240 seconds
- [ ] **15 stat categories**: Kills, Assists, Double Kills, Weapon Damage by class, Energy Used, Damage with Modules, Healing Done, Control Points, etc.
- [ ] **XP pools**: 10 types (Scoring, ScoringBase, ScoringPerformance, BoosterWin, FirstWinOfTheDay, GoldMembership, TeammatesGoldMembership, BattleReadyRecruit/Veteran/Legendary)
- [ ] **Credits pools**: 13 types (RankUp + same as XP pools)
- [ ] **Scoring formula**: YScoringFormulaParameters with veteranMultiplier, premiumMultiplier, premiumShipXPPercentage, etc.
- [ ] **End-of-match stat comparison**: Top match, top team, above average, above career average, pure stats (max 5 stats shown)

---

## High Priority (Major Feature Gaps)

### Additional Game Modes
- [ ] **Pod TDM**: GameMode_PodTDM_BP
- [ ] **Turbo TDM**: GameMode_Turbo_TDM_BP
- [ ] **Training mode**: YMSS_PLAY_TRAINING
- [ ] **Tutorial mode**: YMSS_PLAY_TUTORIAL
- [ ] **Coop Havoc**: Cooperative variant of Havoc mode
- [ ] **Coop PVE**: Standard cooperative PvE
- [ ] **Bootcamp**: GameInfo_BC_BP (training scenario)
- [ ] **Benchmark mode**: GameMode_Benchmark_BP (performance test)

### Chat & Social Systems
- [ ] **Real chat channels**: Team, squad, global, language-specific channels with actual message routing
- [ ] **Presence system**: Online/offline/away status, friend list, pending friends
- [ ] **Squad system**: Real squad state management (invite, accept, leave, kick, promote)
- [ ] **Squad XP/credit bonuses**: Elite status bonuses for squad members

### Vanity & Cosmetics
- [ ] **Paints**: Ship color customization
- [ ] **Decals**: Ship surface graphics
- [ ] **Emblems**: Faction/personal emblems
- [ ] **Patterns**: Repeating surface patterns
- [ ] **Coatings**: Material finish customization
- [ ] **Character customization**: Material, mesh, gender (YCharacterCustomization*)
- [ ] **Founders packs**: 8+ special cosmetic bundles

### Achievements
- [ ] **34 Steam achievements**: BixsRightHand, SinleyBayRecruit/Veteran/Legend, JackOfAllTrades, MVP, HungryForMore, HelpingHand, DeathmatchVet, EliminationVet, Unbreakable, CryHavoc, OnslaughtVeteran, VeteranCaptain, LegendaryCaptain, TopOfTheLine, PuttinOnTheRitz, PimpYourRide, etc.

### Fleet Progression
- [ ] **Fleet XP sharing**: All ships in fleet earn XP together
- [ ] **Battle bonus**: Fleet-wide bonus, resettable with Credits
- [ ] **Elite bonuses**: Extra contract slot, XP/Credits bonus (self + allies), applied before Battle Bonus

### Item Drop System
- [ ] **YPlayerItemDropCycleMP**: 1 drop per cycle, max 20 checks, max 1 simultaneous unclaimed item
- [ ] **DN_MPItemDrops**: Loot table integration

---

## Medium Priority (Polish & Completeness)

### Season & Event System
- [ ] **6 seasons**: S1-S6 with full episode data (20+ episodes)
- [ ] **Reward tiers**: BRONZE, SILVER, GOLD per season
- [ ] **Seasonal cosmetics**: Body sets (Autumn, Spring, Summer, Winter, Explorer, Jovian, NanoDoc), decals, paints
- [ ] **PvE events**: DN_Events_DT integration
- [ ] **Boss battles**: BunBunBoss (S3), DreadnoughtHeavyBoss (S4), Multiple bosses (S6)
- [ ] **Medal scoring**: PvEMedalScoring DataTable

### AI System Enhancements
- [ ] **AI behavior trees**: 22 BT nodes, blackboard system
- [ ] **AI ability activation**: DN_AIAbilityActivation_DT (21 rows) with target/damage/distance/threat/probability gates
- [ ] **AI state machine**: YCSB_NONE, YCSB_ATTACK, etc.
- [ ] **PvE spawn logic**: EnemyGroupSpawn_Component, CreepFactory, triggers, managers
- [ ] **AI combat scene manager**: YAICombatSceneManager
- [ ] **Bot configurations**: DN_ClientBots_DT
- [ ] **Battle ready**: DN_BattleReadyUpdate_DT

### Custom Match Enhancements
- [ ] **Real lobby state**: Actual room state management (not just success responses)
- [ ] **Custom settings**: Game mode, map, player limits, team sizes
- [ ] **Fleet select**: Real fleet selection flow in custom matches

### Market & Economy Enhancements
- [ ] **Featured items**: Rotating daily/weekly store offers
- [ ] **Spotlight items**: Highlighted/promoted items
- [ ] **Bundle system**: GetMarketBundlesRequestDefinition
- [ ] **Live tiles**: Dynamic UI elements in store

### Remaining YA_* Handlers
- [ ] **~15 less common request types**: Complete remaining YA_* handlers that currently return generic success

### Server→Client Notifications
- [ ] **Achievements updated**: YA_AchievementsUpdated with actual achievement data
- [ ] **User status**: YA_UserStatus with real presence data
- [ ] **Fleet charged**: YA_OnFleetCharged with actual fleet state

---

## Low Priority (Infrastructure & Polish)

### Test Coverage
- [ ] **auth-server**: Add tests for login, register, logout, ban/unban, JWT validation
- [ ] **admin-cli**: Add tests for CLI commands
- [ ] **dn-launcher**: Add tests for identity generation, auth flow, config parsing
- [ ] **game-manager**: Add tests for instance lifecycle, port pool, spawner
- [ ] **master-server**: Add tests for server registry, heartbeat, stale cleanup
- [ ] **shared/db**: Add tests for Open, Migrate, schema versioning
- [ ] **shared/logging**: Add tests for logger creation, service hook
- [ ] **shared/middleware**: Add tests for JWT middleware, rate limiter

### Medium Issues (29 outstanding)
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

### Phase 8 Polish Items
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
1. DataTable loading (biggest single unlock)
2. Ship/weapon/ability data
3. Damage/defense system
4. Scoring/rating system
5. Additional game modes
