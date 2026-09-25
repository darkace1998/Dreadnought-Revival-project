# battle-server-mod

> ## Deployable as of #65 (2026-08-04)
>
> The first merged build froze the battle server for ~90 seconds on the first
> hull click. The cause was not `StaticLoadClass`, as first reported: it was a
> `GObjects` scan issuing a `VirtualQuery` syscall twice per object, once per
> precast -- roughly 8 million syscalls. `StaticLoadClass` costs 1.66s for all
> four assets. CDO resolution is now 4ms in a single pass and the engine's
> `TickDispatch: Took too long to receive packets` warning is gone
> (AGENT-CHAT C29, retracted and corrected in C30).
>
> Every log line now carries a wall-clock stamp, which is what made the original
> misdiagnosis possible: four resolution lines with an 88-second hole between
> them and no times to show it.

The landing place for the **host-side loadout fix** from the client-side half of
this project (`AHouseOfBards/DreadnoughtTestBench`), accepted by the project
owner on 2026-08-04 (AGENT-CHAT C25.1, S14).

`src/dn_host_loadout.cpp` is the fix. It is one source file, one hook, and no
other behaviour; `build.bat` produces `build/dn_host_loadout.dll`. See
[Building](#building) and [Deploying](#deploying) below.

## Why a DLL is in a repository whose first rule is "no client modification"

It is not a client modification. It runs on the **battle server** — the same
executable, launched headless by `dn-dedicated` with `"<map>?listen" -server
-nullrhi -unattended` — and it is not loaded by, required by, or visible to a
player's game client.

The rule it does not break is the one that matters: a player still runs an
unmodified client, and `dn-launcher/` is still the only thing we ask them to
install. That rule is about what we require of players, not about what a server
operator runs on their own host.

## What belongs here

Exactly one thing, and the reason it qualifies:

**Registering the four precast loadouts with the host's loadout manager.**
`UYLoadoutManager` is filled only by `InitializeFromPlayerData`, which reads the
YMmogbrain module at `+0x3898`, which requires a login the host never performs
(`docs/battle-server-data-path.md` §1). `LoadInstallingLadouts` would install
exactly the four T1 mediums the client is offered, and it sits behind the same
gate; there is no third caller. So the manager is empty, every
`FindLoadoutByID` misses, and `SpawnDefaultPawn` refuses.

The accepted fix hooks `UYLoadoutManagerComponent::FindLoadoutByID`
(`0x340340-0x3404D3`) and, **on a miss only**, registers the four cooked precast
assets via `0x3382F0(manager, loadout, 2)` and re-runs the engine's own lookup.
A lookup that succeeds is never touched.

That is what makes it acceptable rather than a workaround: it supplies data the
engine was designed to have and then gets out of the way. It stays correct if a
real backend ever populates the manager, because it only runs when the manager
could not answer.

## Dedicated net mode (`dn_host_dedicated.txt`) — this is what gets players out of orbit

**Opt-in** like every host mod: an empty `dn_host_dedicated.txt` beside the
executable, or `DN_HOST_DEDICATED=1`. Added 2026-09-24; not yet verified in a
live match.

**Why players stay in orbit** (verified by disassembly and the full host log of
a match, `run/battle-logs/battle-20260923-204528-port7777.log`):

- The teleport into the arena needs the pawn's in-orbit state, which the orbit
  sequence sets only once the GameState readiness mask is complete
  (`AYGameState_MP+0x1D60 == 0xF`, AGENT-CHAT S39).
- On a **dedicated** server the game completes that mask itself. `0x3ACA40`,
  called from `ServerReadyForJoining` and `ClientLoadingCompleted`:

  ```text
  if Role == Authority:
      if GetNetMode() == NM_DedicatedServer (1): set bits 0x1|0x2|0x4|0x8
  set bit 0x10
  if Role == Authority: StartOrbitTransition
  ```

- The host is the shipping *game* exe, which is never dedicated: engine PreInit
  sets `GIsClient = 1` on every non-commandlet launch (`0x228F71`) whatever the
  command line says. That is why `-server`, and dropping it, changed nothing.
  `UNetDriver::GetNetMode` (`0x1A5CF60`) returns `1 + (GIsClient != 0)` for a
  server, so the host is a LISTEN server (2), the mask stops at `0x2`, and every
  player gets "not in orbit".

The hook calls the original `UNetDriver::GetNetMode` and turns a LISTEN answer
into DEDICATED. Nothing is forced: the game then takes the path its shipping
servers took. A client's net driver is not a server, so clients are unaffected,
and `GIsClient` itself is left alone.

What to look for in the host log:

```text
[dn-host-loadout] installed: UNetDriver::GetNetMode hooked at RVA 0x1A5CF60 (...)
[dn-host-loadout] net mode: net driver ... answered LISTEN (2); reporting DEDICATED (1) ...
... StartOrbitTransition | Start Orbit Transition for player 257
... TeleportPlayersFromOrbit ...        and NO "that is not in orbit!" after it
```

## The player's own fit (`dn_host_player_loadouts.txt`)

**Opt-in.** The host used to spawn every ship with its DEFAULT precast fit,
because a client only tells the server which loadout it picked
(`ServerPlayerClickedShipLoadout(FName)`) and this exe's server side cannot look
the fit up: its mmog `AutoLogin` (`0x2AABCB0`) is a stub and the fleet manager
(`0x35FDF0`) only reads the host's own account. Now:

1. mmogbrain adds `?DNPID=<pid>` to the `YA_Connect` travel address; the host
   keeps the login URL at `UNetConnection+0x198` (verified live).
2. On a loadout miss the mod asks mmogbrain
   `GET http://127.0.0.1:8083/battle/loadout?pid=&id=` (loopback only;
   `DN_MMOG_HTTP=host:port` overrides) for the record the client itself built
   its loadout from.
3. It builds the loadout the way the client does (`HandleMmogbrainLoadoutAdded`,
   `0x348830`): `StaticConstructObject(UYShipLoadout)` -> `0x34D690(obj, owner+0x970, &info)`
   -> cached init data -> `AddLoadout(mgr, obj, 2)`.

It runs before any precast fallback (the client picks by the precast's name, so
a registered default would shadow the fit). If mmogbrain has no record, the
default precast is used as before.

```text
[dn-host-loadout] player loadout: Default__VH_..._PrecastLoadout_BP_C for <pid> -> precast N, weapons a/b, abilities ... -> registered
```

## Stack overflow at match end (always on)

Verified 2026-09-24: the host died with `EXCEPTION_STACK_OVERFLOW` (c00000fd) the
moment a proving-ground match was won, so no win screen. The overflowed stack
(scraped from `/proc/<pid>/task/<tid>/mem`) held one 12-frame loop ~3,884 times:
`ClientSetPlayerRestrictions` exec (`0x746B40`) -> `_Implementation` (`0x5743B0`)
-> `SetPlayerRestrictions` (`0x5954F0`) -> the client RPC (`0x5E5B80`) -> back.
On any server `0x5954F0` sends the RPC; for the host's own LOCAL player (256)
a client RPC runs in-process, so it re-enters itself forever. The shipping
servers had no local player. The mod hooks `0x5743B0` and drops a re-entrant
call on the same thread; remote players are unaffected.

```text
[dn-host-loadout] restrictions: dropped a re-entrant ClientSetPlayerRestrictions on controller ...
```

## End-of-match screen (on; `dn_host_no_eom_stats.txt` turns it off)

Symptom: the match ends, the screen fades to black and stays black. The client
log shows the end-of-match stage graph stopping at `SetupUIWidgets`.

That stage waits for `AYPlayerReplicationInfo+0x7E0`, which only the client RPC
`ClientSetTopPlayerMatchStats` sets (body `0x5B0240`). Nothing in this exe sends
that RPC: its FName global (`0x3E102F8`) is referenced only by its initializer.
The ranking code lived in the separate server build. The mod hooks the one
sender of `ClientStartEndOfMatchTransition` (`0x5E5D70`) and, right after it,
sends `ClientSetTopPlayerMatchStats` with an empty array to that controller's PRI,
the same way the engine's own RPC stubs do (`FindFunctionChecked` `0xD57C90`,
`ProcessEvent` at vtable `0x1A8`). The MVP page gets no entries; the host has no
ranking data to fill it with.

Log: `eom stats: sent ClientSetTopPlayerMatchStats (empty) to controller …`.

## Researched ships: any precast on demand

Part of the loadout fix, no switch of its own. The four T1 mediums are only the
starter fleet; a player who fields a researched ship makes the host look up that
ship's precast (`Default__VH_SniperLight_T2_PrecastLoadout_BP_C`, ...), which the
T1 set cannot answer -- verified live: "STILL MISSING", then "Active Loadout not
found. Can't spawn". On such a miss the mod resolves the requested precast by
name through `src/precast_paths.h` (all 102 precasts, generated from the cooked
asset list by `scripts/gen-precast-paths.py`; 48 of them live outside a
`Precast/T<n>/` folder, so the path cannot be derived), loads it, and registers
it with the asking manager.

```text
[dn-host-loadout] precast VH_SniperLight_T2_PrecastLoadout_BP (on demand): class=... cdo=... (N ms)
[dn-host-loadout] register VH_SniperLight_T2_PrecastLoadout_BP with manager ... -> ok
[dn-host-loadout] FindLoadoutByID miss for ... -> after registering: FOUND
```

## AI in the proving ground (`dn_host_bc_ai.txt`)

**Opt-in.** Empty `dn_host_bc_ai.txt` beside the executable (or
`DN_HOST_BC_AI=1`). Verified live 2026-09-24.

The proving ground is game mode BC (`YGameMode_Bootcamp`, `EYGameModeType` 18).
Its blueprint carries 45 NPC entries, but every spawn path (game-mode virtual
`0x9D0` = `0x3678F0` -> fill `0x381FA0` -> `StartCombat`) is gated on
`GameMode+0x961 m_enableSpawnAI`, which the native constructors set only for
TrainingMatch (`0x362840`) and nothing in BC content overrides. On our host it
read 0, so no bot ever spawned.

The mod hooks the multiplayer game mode's once-a-second timer (`0x36A080`, the
caller that fills the teams at `m_remainingTime <= 50` before the match starts)
and, when the GameState's `m_gameModeType` is Bootcamp, sets the flag once. The
game does the rest: at the 50 s mark both teams fill (T1 7 + the player, T2 8).

```text
[dn-host-loadout] bc ai: game mode ... is Bootcamp; m_enableSpawnAI 0 -> 1 ...
... AYAICombatSceneManager::StartCombat - starting combat
```

Known noise: "No mmog tier data available for spawning AI ships, using default
hardcoded data" (the host never logs in) and content warnings from the AI data.

## Player ship physics (`dn_host_ship_physics.txt`)

**Opt-in.** Empty `dn_host_ship_physics.txt` beside the executable (or
`DN_HOST_SHIP_PHYSICS=1`). Verified live 2026-09-24: without it a player's ship
moved on their screen but snapped back to a stale spot on every server
correction (e.g. after firing or an ability).

Ship movement is input-replicated: the client sends throttle/steering/vertical
(`ServerUpdate*State`) and the server simulates. The force builder
(`UYVehicleMovementComp`, `0x5C8C00`) skips all forces for a ship that is not
locally controlled when `+0x489` is set, unless the ship is near and in front of
the **local player's camera** (`+0x498`/`+0x49C`) -- a client optimisation.
`+0x489` is set only by `0x5C4EB0`, and only when the world has a local player
controller. A real dedicated server has none; our host (the game exe) has one,
parked at the orbit camera, so player ships far from it were never simulated.

The mod hooks `0x5C4EB0` and clears `+0x489` after it runs, on ships that are
not locally controlled. AI ships are locally controlled on the host and are
untouched.

```text
[dn-host-loadout] ship physics: movement component ... is a remote player's ship; cleared ...
```

## DISABLED: fleet tier (`dn_host_fleet_tier.txt`)

**Disproved 2026-09-24 and switched off in code**; the marker is now ignored and
the log says so. The gate below reads the **pawn**, not the PlayerState: the
caller (`0x3838D1`) passes an IsA-checked element of an array of pawns, and the
null branch logs "Trying to teleport into level a null YPawn!". `+0x948` on
`AYPawn` is an unreflected native field -- the pawn's own orbit state -- and the
SDK's `m_highestFleetUnlocked` merely shares the offset; the real PlayerState
constructor (`0x5A8820`) already sets that to Recruit. So writing `+0x948` at
`TeleportPlayerIntoLevel` forced the pawn's orbit flag, which is faking the
gate. The analysis below is kept as a record of how that was reached.

### Original section (superseded)

**Off by default.** Enable with an empty `dn_host_fleet_tier.txt` beside the
executable, or `DN_HOST_FLEET_TIER=1`.

The orbit teleport is gated on one byte:

```
FUN_3D92A0:  cmp byte ptr [rdx+0x948], 0 ; jne proceed
             -> "Trying to teleport into level player %s that is not in orbit!"
```

`+0x948` is `AYPlayerReplicationInfo::m_highestFleetUnlocked`, an `EYFleetType`
declared in `YMmogbrain_Structs.h` (`None=0 Recruit=1 Veteran=2 Legendary=3`).
It is `EYFT_None` on a host that never logged in, so **nobody is ever teleported
and the client sits in the orbit screen** — measured live, and the reason the
spawn bypass below was not sufficient on its own: the host spawned four pawns
for the player and the client stayed in orbit regardless, because the only thing
that takes a client out of orbit is the teleport.

This sets the tier to `EYFT_Recruit` **when, and only when, the host has none**.
It never overwrites a value the engine already has — the same rule the
`FindLoadoutByID` hook follows, and what keeps it correct if a real tier ever
arrives.

**Where it is written, corrected 2026-08-15.** It used to be written from
UFunction trigger points (`PostLogin`, `ServerReadyForJoining`, …) reached
through a `ProcessEvent` hook. Measured on a live host, **none of them ever
ran**: `ProcessEvent` hooked successfully and `EnsureFleetTier` then logged
nothing at all — not even one of its failure paths, which all log. `K2_PostLogin`
resolves on the base `GameMode` and is never dispatched through `ProcessEvent`
here. The fix had never once executed.

It is now written in a hook on **`AYOrbitTransitionManager::TeleportPlayerIntoLevel`
(RVA `0x3D92A0`)** — the function that reads the gate, which the same host log
proves runs once per player. No reflection, no GObjects scan, no guess about
when a `PlayerState` exists: the engine hands over the exact object it is about
to test, at the moment it tests it. The old trigger points are retained and
still log, so if they ever start firing it will be visible.

The gate was re-verified by disassembly rather than inherited from notes:

```
0x1403d92b6  test rdx, rdx
0x1403d92b9  jne  0x1403d9303        ; a null PRI logs a different message
0x1403d9303  cmp  byte ptr [rdx+0x948], 0
0x1403d930a  jne  0x1403d9393        ; -> teleport proceeds
```

The format string has two identical `.rdata` copies (`0x142edaf90`,
`0x142edb0a0`) and exactly one xref, to the second — the wrong-copy trap in
`CONTRIBUTING.md`.

Why this is supplying data rather than faking a check: the engine computes this
tier from the YMmogbrain module (`FUN_3A5831`, which logs
`EYFleetType::EYFT_Recruit: no FleetType override - FleetTier=%d`) and cannot
here, because the host holds no mmogbrain data at all. Recruit is the floor —
what a player who owns any fleet has unlocked, and every player who reaches a
battle server owns one.

**Honest limit:** Veteran and Legendary players are under-reported as Recruit.
If a real tier ever reaches the host, this defers to it.

**Prefer this over the spawn bypass below.** It lets the *normal* orbit flow
finish, so players keep ship selection.

## Optional: post-login spawn (`dn_host_postlogin.txt`)

**Off by default, and a bigger change than the loadout fix.** Enable with an
empty `dn_host_postlogin.txt` beside the executable, or `DN_HOST_POSTLOGIN_SPAWN=1`.

Registering the loadouts gets a player a **pawn** — the host spawns them and
`SetYPawn` assigns it — but they still never reach the map, because the orbit
teleport is gated on `AYPlayerReplicationInfo::m_highestFleetUnlocked`
(`+0x948`, an `EYFleetType` from `YMmogbrain_Structs.h`). It is `EYFT_None` on a
host that never logged in, and no backend payload can change that, because the
host holds no mmogbrain data at all (AGENT-CHAT S39, S40).

This switch does not satisfy that gate. It skips the orbit flow: it hooks
`PostLogin`, sets the controller's active loadout, and calls the engine's own
`ServerRestartPlayer()`, which asks the GameMode for a PlayerStart and spawns
there. That path never enters `UYPlayerOrbitComponent` and never reads the fleet
tier. The approach is taken from `dread-sdk`'s server mod, which the operator has
played real matches with (S42).

**What it costs:** the player no longer picks a ship in orbit — everyone spawns
in one configured hull (`g_postLoginLoadoutIndex`, default Assault Medium T1).
That is a real regression in behaviour and the reason this is opt-in and
separate. If the orbit path can ever be made to work, prefer it.

**How it hooks:** `UObject::ProcessEvent`, vtable index `0x35`. The three
UFunctions it needs (`K2_PostLogin`, `GetLoadoutManager`, `ServerRestartPlayer`)
are resolved once at install by walking GObjects, so the hot path is a single
pointer comparison per reflected call — no string work.

**Not yet verified against a running host.** It is written from the SDK dump's
offsets (`m_activeLoadout` at `0x0208`) and dread-sdk's proven sequence, but it
has not been built or run. Expect to read the `[dn-host-loadout] post-login:`
lines on the first attempt; every failure path logs why and stands down rather
than continuing.

## What does NOT belong here

Anything that lies to a gate rather than filling a hole. The client side named
their own orbit fix as exactly this and would rather delete it than keep it
(AGENT-CHAT C23.3):

- **Forcing `+0x948` to an arbitrary value.** Corrected 2026-08-14: that byte is
  NOT `EYOrbitReadyState`. The SDK dump names it
  `AYPlayerReplicationInfo::m_highestFleetUnlocked`, an `EYFleetType`
  (`EYFT_None/Recruit/Veteran/Legendary`) declared in `YMmogbrain_Structs.h` --
  so the "not in orbit" error really means "this player's highest unlocked fleet
  tier is None", and the value is backend data rather than an engine-computed
  flag. Writing 1 to get past the comparison is still out: it produced a pawn
  that could fire but not move (C32.4). Writing a player's REAL tier, if the host
  can be given it, is the other kind of change and is fine -- see `CLAUDE.md`,
  which no longer treats this offset as forbidden on principle.
- Anything that invents player data, a fleet, or an inventory.
- Anything a player's client would load.

If a change here would stop being correct the moment the host obtains a real
player record, it belongs in the other repository.

## Constraints on the PR

- **Optional at runtime.** The stack must build, start and run a match with this
  directory absent or the DLL not deployed. `dn-dedicated` must not require it.
- **Opt-in on the host.** The existing marker-file switch
  (`dn_server_loadout.txt` beside the executable) is fine and is the right shape
  — our spawner does inherit its environment (`buildEnv`, S10.5), but a file
  survives however the operator starts the service.
- **Its own build.** Windows/MSVC, not wired into `go.work`, not built by
  `scripts/setup.sh`.
- **Evidence in the commit body**, per `CONTRIBUTING.md`: the RVAs, what was
  verified against a running host, and what was not.

## Building

Windows, MSVC, x64. Nothing else — no Go, no `go.work` entry, no
`scripts/setup.sh` step. Deleting this whole directory leaves the stack building
and running exactly as before.

```console
> cd battle-server-mod
> build.bat
Built ...\battle-server-mod\build\dn_host_loadout.dll
```

`build.bat` finds the toolset with `vswhere` if you are not already in a
Developer Command Prompt. The only dependency is
[MinHook](https://github.com/TsudaKageyu/minhook) (BSD 2-clause), vendored under
`third_party/minhook/` so the build needs no network access.

## Deploying

The DLL is loaded by **side-loading**, not by an injector. The game imports four
functions from `wer.dll` — Windows Error Reporting — and Windows resolves that
from the executable's own directory first:

1. Copy `build/dn_host_loadout.dll` next to `DreadGame-Win64-Shipping.exe`,
   renamed to `wer.dll`.
2. Create an empty `dn_server_loadout.txt` in the same directory.
3. Create an empty `dn_host_dedicated.txt` in the same directory, so players
   can leave orbit (see "Dedicated net mode" above).
4. Create an empty `dn_host_bc_ai.txt` for bots in the proving ground, and an
   empty `dn_host_ship_physics.txt` so player ships are simulated on the host
   (see the two sections above).

Steps 1 and 2 are required for the loadout fix; step 3 for the orbit; step 4
for bots and for player movement. Each can
be undone by deleting a file.

The four `WerReport*` exports are no-op stubs. The engine only calls them while
writing a crash report, and a host writing a crash report has already lost the
match. That is deployment plumbing, not part of the fix.

**The client loads this file too**, because a battle server and a player's
client are the same executable in the same directory. That is safe and it is
checked twice:

- `game-manager`'s spawner is the only thing that passes `-MatchID=`, so its
  absence identifies a client. On a client the DLL returns from `DllMain`
  without reading the game's memory, installing a hook, or writing a line.
- Without `dn_server_loadout.txt` it stands down on a host as well, and says so.

## What it logs

Every line is tagged `[dn-host-loadout]`, on stdout (which `dn-dedicated`
captures) and in `dn_host_loadout.log` beside the executable. A host log
therefore says whether the hook was present, not just whether it worked:

```text
[dn-host-loadout] battle server detected and enabled. module base 0x7FF6...
[dn-host-loadout] installed: FindLoadoutByID hooked at RVA 0x340340 (...)
[dn-host-loadout] resolved UClass class object at 0x... (Class)
[dn-host-loadout] precast VH_AssaultMedium_T1: class=0x... cdo=0x... (Default__VH_AssaultMedium_T1_PrecastLoadout_BP_C)
[dn-host-loadout] 4/4 precast loadouts resolved
[dn-host-loadout] register VH_AssaultMedium_T1 with manager 0x... -> ok
[dn-host-loadout] FindLoadoutByID miss for FName 0x21F0F -> after registering: FOUND
```

The last line is the one that matters. `FOUND` means the engine resolved the
player's own hull id by its own path, which is the whole point: the fix supplies
the data and the engine still makes the choice.

## Addresses

All verified against the PE exception directory as real `RUNTIME_FUNCTION`
entries — not chained cold chunks, not mid-instruction.

| RVA | What it is |
| --- | --- |
| `0x340340-0x3404D3` | `UYLoadoutManagerComponent::FindLoadoutByID(mgr, id, warn)`; `id` is an **FName** |
| `0x3382F0-0x338330` | `AddLoadout(mgr, loadout, uint8 type)` — **add only** |
| `0xD78110-0xD789B6` | `StaticLoadObject`/`StaticLoadClass`, 7 arguments |
| `0x1A5CF60-0x1A5CF8D` | `UNetDriver::GetNetMode(driver)` -> 1 dedicated / 2 listen / 3 client |
| `0x36A080-0x36A539` | `AYGameMode_Multiplayer` once-a-second timer (vtable `0x8E0`), `(this)` only |
| `0x5743B0-0x5744A0` | `ClientSetPlayerRestrictions_Implementation`, 18 args (this, 16 bytes, 1 pointer) |
| `0x5C4EB0-0x5C511E` | `UYVehicleMovementComp` local-camera cull setup, `(this)`; only writer of `+0x489` |
| `0x5E5D70-0x5E5DA9` | `ClientStartEndOfMatchTransition` RPC stub (`UYPlayerOrbitComponent`), `(this)`; the only sender |
| `0xD57C90-0xD57CB9` | `UObject::FindFunctionChecked(obj, FName)`; `ProcessEvent` is vtable slot `0x1A8` |
| `0x3E102F8` | FName global `ClientSetTopPlayerMatchStats` (data; referenced only by its initializer) |
| `0x614E30` | `UYShipLoadout::StaticClass()` |
| `0xD759E0` | `StaticConstructObject_Internal`, 8 arguments |
| `0x34D690` | loadout init from `FYShipImportLoadoutInfo` `(obj, *(owner+0x970), &info)` |
| `0x21F5E0` / `0x1CF3240` / `0xC9CF20` | `FString` assign / `TArray<int32>` assign / `FName(const wchar_t*, EFindName)` |
| `0x3F63A70` | `GObjects` (data) |
| `0x3E069D0` | `GNames` (data, used for log text only) |

Two of these have a trap attached, both of which cost real time to find:

- **`0x3382F0`, not `0x337450`.** `0x337450` is `AddAndActivateLoadout`, and its
  tail calls `0x337050` *unconditionally*. Registering four loadouts through it
  leaves the **last** one active; the array ends with Support, which is why every
  hull a player picked used to spawn as a Cerberus.
- **Type 2, not 4.** The validity gate at `0x33C680` rejects loadouts recorded
  as type 4.
