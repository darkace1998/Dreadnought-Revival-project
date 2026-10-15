# How a battle server gets its data (and why a player cannot spawn)

Everything here is from the shipping binary, the client log and the battle
server's own captured output, on 2026-08-02. Addresses are Ghidra `FUN_*` names
from the full decompile.

The short version: a battle server is the client executable run as a **listen
server with no backend**. Two data paths were meant to fill that gap. One is
unreachable without a backend, and the other is failing on a hard engine limit
and disconnecting the player.

## 1. Loadouts: the manager on the server is empty

A joining client asks the server to spawn it:

```
ServerSpawnNearActor | Could not Set the active Loadout.
                       Given loadout ID does not exist in loadout manager!
AYGameMode::SpawnDefaultPawn: Active Loadout not found. Can't spawn
```

- `AYPlayerController::ServerSpawnNearActor_Implementation` = `FUN_1405930d0`.
  It fetches the player controller's loadout manager (`FUN_1405b7a60`, PC+0x958
  → +0x28) and looks the id up with `UYLoadoutManager::FindLoadoutByID`
  (`FUN_140340340`), which matches an **FName at loadout+0xb0**.
- The id is the loadout blueprint's CDO name. Observed verbatim from a client
  with no player data:
  `Dind't find any loadouts matching id Default__VH_AssaultMedium_T1_PrecastLoadout_BP_C`.
- The manager fills itself from the **local process's** mmogbrain data:
  `InitializeFromPlayerData` (`FUN_14034bd00`) → `FUN_1405b9360`, which reads the
  YMmogbrain module singleton at **+0x3898**.
- Both ways of adding anything sit behind that. `FUN_14034ff90` either walks the
  player data or, when there is none of it, falls back to
  `LoadInstallingLadouts` (`FUN_14034f1d0`, the four T1 medium precast
  blueprints from `m_installerLoadoutList` in DefaultFleet.ini) — and
  `FUN_14034ff90` is only ever called from `InitializeFromPlayerData` and
  `FUN_140355440`, both of which require valid player data first. Checked
  against the call graph: there is no third caller.

So on a server with no backend the manager stays empty, and the ids the client
sends can never match. The one loadout source that still works is
`AYGameMode::GetGameModeLoadout` (vtable +0x880), overridden **only** by
`AYGameMode_TrainingMatch` (`m_trainingMatchLoadout`). The resolver is
`GetLoadoutForPlayer` = `FUN_140370970`.

**That is no longer why the matchmaker picks a mode.** This line used to end
"which is why the matchmaker forces TM", and that was true of `6960e5f` only.
Forcing TM broke orbit and ship selection, `6e8478f` reverted it, and `S10.1`
replaced it with a single substitution -- TM (Proving Grounds) is routed to TDM
because TM is the one mode that fails `no orbit spawn locations set!`. Matches
run as TDM, and the client side confirmed it from the host's own
`LoadMap: ...?listen?game=TDM` (AGENT-CHAT C25.3). Nothing forces a game mode
for the loadout's sake any more.

**A fix exists, and it is being brought here.** The project owner accepted it on
2026-08-04; it lands in `battle-server-mod/`, which carries the contract it has
to meet. What follows describes the version that ran on the client side's own
host, superseded in shape by `C25.1`: the accepted fix hooks
`UYLoadoutManagerComponent::FindLoadoutByID` (`0x340340-0x3404D3`) and, on a
**miss only**, registers the four precasts with `0x3382F0(manager, loadout, 2)`
and re-runs the engine's own lookup — rather than choosing a loadout itself,
which is what made every hull spawn a Cerberus.

The earlier description, for the record: the client-side team hooks
`GetLoadoutForPlayer` (`FUN_140370970`) in their DLL: call the original, and only
if it produced nothing, load the cooked precast asset whose CDO name the engine
just asked for and hand back the CDO — which is what `LoadInstallingLadouts`
would have done if anything could reach it. It is opt-in on the host via a marker
file (`dn_server_loadout.txt` beside the executable). Kept in their tree, not
this one, so that "the game client is unmodified" stays true of this repo
(AGENT-CHAT C16, S10.6).

### The battle server cannot log in

Verified twice: nothing reaches mmogbrain during a battle server's whole life,
and still nothing when the process is handed
`-GatewayAddress/-GatewayPort/-YFirmamentAddress/-YFirmamentPort` exactly as
dn-launcher passes them to the client. The backend connection is driven by the
frontend login flow, and a process booted straight into a map never runs it.

## 1b. The same absence, three more times: orbit and thrusters

The empty loadout manager is not one bug. It is one symptom of one fact -- **the
host has no player record** -- and the client side has since found two more.
Recording them together so the next one is recognised on sight.

| symptom | what actually reads the missing data |
| --- | --- |
| `FindLoadoutByID` misses every id | loadout manager, filled only from player data (above) |
| the teleport out of orbit never fires | `PlayerController+0x948` = `EYOrbitReadyState`, computed by `FUN_140346C20` from the fleet **slot count**; count 0 returns 0 |
| thrusters and muzzle effects never switch on | suspected: the orbit transition that the forced flag skips (client side, unconfirmed) |

That makes populating the host's fleet slots the single lever for the last two,
which is what the client side reached independently in AGENT-CHAT C27.

### Empty matches are NOT on that list, and here is the disproof

`S12.1` claimed they were: that `LoadNPCSet` (`FUN_1403A0A80`) switches on a
fleet type, that the type comes from `PlayerController+0x958` (`m_fleetManager`,
the same pointer the orbit gate checks), and that a host without one therefore
loads no NPCs. **That is wrong, and this repo's own battle-server logs say so:**

```text
LogYAICombatSceneManager: AYAICombatSceneManager::LoadNPCSet | Request sync. load for NPCSet!
LogYGameState_MP: LoadNPCSet | Sync loading NPC set.
```

The NPC set loads. No `Invalid fleet type!`, no `No NPCSet data loaded!`, no
`FStringAssetReference is not valid!` in any host log we have.

The reading error is worth keeping, because it is an easy one to repeat. The
fleet-type getter `FUN_140396C50` has two branches:

```asm
call 0x1726F50          ; GetNetMode(this)
cmp  eax, 3             ; NM_Client
jne  0x396CDE           ; NOT a client -> read the GAME STATE
...                     ; client path: local PC -> [+0x958] m_fleetManager
0x396CDE: movzx eax, byte [rbx+0x1D48]   ; server path: GameState+0x1D48
```

The `m_fleetManager` path is the **client** branch. A server reads
`GameState+0x1D48`, which `FUN_1403A55E0` sets to 1, 2 or 3 -- and `LoadNPCSet`
accepts all three. So the host's fleet type is valid, and it was never the
reason a match is empty.

What is still true and unexplained: matches have no opponents. The set loads,
`AYGameMode_Multiplayer::SpawnNPC` exists, and
`AYAICombatSceneManager::StartCombat - starting combat` appears in some of our
host logs but not others. That is the next thread, and it starts at `StartCombat`
rather than at the fleet.

## 2. OTS: the client uploads its tune data, and gets kicked for it

There IS a client→server data channel. It is called **OTS**, not "loadout",
which is why searching the binary for `*Loadout*` RPCs missed it.
`UYLocalServerDataManager`: the server sends `ClientRequestOTSBunch`, the client
answers `ServerReceiveLocalServerOTSData`. `ReplicateDataToLocalServer`
(`FUN_140590bd0`) runs whenever NetMode < 3, so every battle server does it.

It fails:

```
LogNetPartialBunch:Error: Final partial bunch too large
UChannel::ReceivedRawBunch: Bunch.IsError() after ReceivedNextBunch 1
Received corrupted packet data from client 10.0.0.26.  Disconnecting.
```

- UE 4.13 caps a reassembled partial bunch at exactly 64 KB. The check is in
  `FUN_1419091a0`: `(NumBits + 7) & ~7 < 0x80001`. There is no
  `NetMaxConstructedPartialBunchSizeBytes` cvar in this version, so it cannot be
  raised.
- The client sends in fixed slices of **900 rows** (`FUN_14057b230`:
  `cursor + 900`, stride 0x40) across 8 arrays, stage 0..7, done at 8
  (`FUN_140591860`). Most slices are ~33 KB. The last one measured 139 partial
  bunches × 501 B ≈ **69.6 KB** — 6% over the cap.
- Measured from one join: 78 slices, ~2.6 MB, over 4 seconds, then the kick
  about 13 s after the client connected.

**It does not reproduce on Windows.** The client side ran a full match against a
Windows host with zero `Final partial bunch too large`, zero `Bunch.IsError`,
zero `corrupted packet data` and no disconnect (AGENT-CHAT C25.2). Every
measurement above was taken under Wine. Before anyone spends more on this, note
that the bug may be platform-dependent -- and that the client side's 900->600
slice change (C11) is in the build that produced the clean run, so the two are
not independent.
- **The host survives it.** The instance that logged the overflow at 22:29:20
  went on running until 22:44:41 and exited only to a SIGKILL from the harness --
  15m42s later. So the oversized bunch disconnects the player; it does not crash
  the server, at least under Wine. Worth stating because the client side reported
  battle servers dying ~56 s in whenever a client is attached (AGENT-CHAT C9) and
  wondered whether this was the cause. On this box it is not.

The rows are the tune data, flattened to JSON nodes
(`RebuildTuneData_Internal`, `FUN_14058d390`). The 8 arrays line up exactly with
mmogbrain's `YA_Tune` fields: WeaponsTune, BattleReadyTune, ProjectilesTune,
AbilitiesTune, OfficersTune, FeatsTune, HavocTune, GameModifiersTune.

### Why the client is uploading its own shipped tables

`YTuneManager::Set()` (`FUN_1403d5160`) is **never called**. Confirmed in the
user's client log and reproduced locally: the only LogYTuneManager lines are
`LoadBackupDataTablesFromAssets`, `YTuneManager created` and
`RequestUpdateFromServer (version: 0.0.0)`. `Set()` logs
`Received data, setting tune values (version: %s)` at Display before it does
anything, and that line never appears — even though mmogbrain logs the YA_Tune
request arriving and a 299-byte response going back out.

So the client keeps the tables `LoadBackupDataTablesFromAssets` loaded from its
own cooked assets, and those are what it uploads.

Notes on that path, for whoever picks it up:

- `Set()` first bails entirely if the command line has **`-noonlinetuning`**.
  The client is not launched with it.
- It reads `Returning`, then `Returning.<MetaData>` → the object it keeps at
  +0x80, then `Version` from that object, and applies the 8 tables only when the
  version differs from the cached one. Our payload matches that shape.
- `HasData()` (`FUN_1403c2520`) returns true once `LoadBackupDataTablesFromAssets`
  has run (it sets +0x531), so the OTS transfer is **not** gated on the server
  lacking tune data.
- A YA_Tune response is a single mmog frame, and mmog frames are delimited by a
  16-bit size field — 64 KB maximum. The full tune tables are on the order of a
  megabyte. There is no known chunking mechanism for this request, so "serve the
  real tune data" is not simply a matter of splitting it up.

### What has been ruled out for YA_Tune

Three shapes/orderings were tested against a real client running locally
(`scripts/wine-client.sh`), each a full login to the hangar. `Set()` did not
log once:

1. `Returning` as a top-level sibling of `result` (the original shape).
2. `Returning` nested inside `result`, which is what every other data response
   in response_builders.go does.
3. Both at once.
4. The response held back until after YA_PlayerGet, on the theory that the
   client ignores an early tune response the way it ignores an early
   YA_PlayerFleets. It was deferred and delivered correctly; nothing changed.

So it is not the payload shape and not the ordering.

**Settled 2026-08-03 by instrumenting the running client** (gdb attached to the
Wine process, exe mapped at its preferred base 0x140000000, breakpoints on
function entries only):

| probe | RVA | hit |
|---|---|---|
| `YTuneManager::RequestUpdateFromServer` (control) | `0x3D3AB0` | yes |
| `YTuneManager::LoadBackupDataTablesFromAssets` (control) | `0x3C83D0` | yes |
| mmog tune request **sender** | `0x2A41A10` | yes |
| tune response **callback** | `0x2A16040` | **no** |
| `YTuneManager::Set` | `0x3D5160` | **no** |
| callback-object constructor | `0x2A0DC90` | **no** |

Server side in the same window: the YA_Tune request arrived and a 299-byte
response went out, and the client went on to complete login ten seconds later —
so the dispatcher was alive and handling later responses the whole time.

`Set` is therefore **not called**, which is what the missing log line suggested
all along but could not prove: the client side's C7 was right that the log line
lives in a moved cold chunk (`0x3D52B3-0x3D5363`, chained to `0x3D5160`) and
proves nothing on its own. This is a **dispatch** problem, not a payload-shape
rejection.

The constructor result is the one to be careful with: `0x2A0DC90` may simply
have run before the debugger attached, about seven seconds into the process. But
the request sender does NOT construct it — `0x2A41A10` calls `0x2A83D40`,
`0x2A5A3D0` and `0x2A7C8E0` and never references it — so the tune response
handler is not attached per-request. It is registered somewhere else, or not at
all, and that is where the next look should go.

Also checked and excluded: `-noonlinetuning` is absent from the client's command
line (and `RequestUpdateFromServer` shares that same guard, so a request going
out at all proves it); `YTuneManager::Set` is the only consumer besides the OTS
path; and the log line is at the same verbosity as the three LogYTuneManager
lines that DO print, so it is not a logging threshold.

## 3. TM is not a way out, and forcing it made things worse

The loadout finding above led to forcing every match into TM, since TM is the
only mode whose game mode supplies a loadout. Measured afterwards on a host with
no client attached — same map, same binary, only the URL's `game` option
differing:

| mode on Highlands | sublevels loaded | `no orbit spawn locations set!` |
| --- | --- | --- |
| TM | 12 | **yes** |
| TDM | 13 | no |
| BC | 13 | no |
| Onslaught (the map's own default) | 13 | no |

TM loads `MP_Highlands_TM` and does not load `MP_Highlands_Light` or
`MP_Highlands_Onslaught`, and it is the one configuration where
`AYOrbitTransitionManager::ActivateBattlePlayerStarts` finds nothing to activate.
Downstream the player has no player start, falls to world origin — under the
terrain on Highlands — and never reaches ship selection at all.

So the two failures are mutually exclusive:

- **TM**: a loadout exists, but the player never gets to choose a ship.
- **anything else**: ship selection works, and the spawn is refused because the
  host's loadout manager is empty.

The second is what this server did before the redirect, and what the operator
reported having. The redirect is off by default now; `DN_FORCE_GAME_MODE` still
turns it on. Both failures share one root — a host with no player data — and
that is where the fix belongs.

### Reading a host's log at all

`-AllowStdOutLogVerbosity` alone captures nothing useful: without **`-stdout`**
the engine attaches no stdout log device, so raising that device's verbosity
raises a stream nobody writes to. The same run captures 219 lines without it and
570 with, including every `ActivateLevel`. Both spawners pass both switches now.
Every "the host does not do X" conclusion drawn before this was reading a log
that could not have shown X.

## What follows from this

- No amount of server-side data fixes the loadout manager. It needs the battle
  server to have player data, which needs it to reach mmogbrain.
- The OTS overflow is client data hitting a client-side engine limit. The only
  server-side handle on its size is what the client's tune manager holds, and
  today the server cannot change that at all, because `Set()` never runs.
- The first thing worth fixing is therefore **why the YA_Tune response is
  ignored**. Until it applies, nothing the server sends can affect the upload.

## 4. The ship-appearance popup: what the client actually reads

Offline RE only (no live client). Goal: find the function
that builds the hangar customize popup's option list and the client structure it
reads. Verified vs suspected is marked inline. All ranges are `.pdata` ranges.

### Ordinals: the EYUILoadoutItemType enum, verified two independent ways

1. Enum-registration walkthrough at `0x69C8DE-0x69D23E`: a `TArray<FName>`
   append loop referencing each `::name` utf-16 literal in order, with a
   running index counter -- the canonical UE4 native-enum registration.
2. The tech-tree classifier tags items by the same ordinals (1 = primary
   weapon, 2 secondary, 3-6 abilities, 7-10 officers, 11-18 appearance,
   19 ship class, 20 elite status).

| # | value | # | value |
|---|---|---|---|
| 0 | NONE | 11 | APPEARANCE_COATING |
| 1 | WEAPON_FIRST | 12 | APPEARANCE_PATTERN |
| 2 | WEAPON_SECOND | 13 | APPEARANCE_EMBLEM |
| 3 | ABILITY_FIRST | 14 | APPEARANCE_DECAL |
| 4 | ABILITY_SECOND | 15 | APPEARANCE_FORECASTLE |
| 5 | ABILITY_THIRD | 16 | APPEARANCE_HULL |
| 6 | ABILITY_FOURTH | 17 | APPEARANCE_BRIDGE |
| 7 | OFFICER_FIRST | 18 | APPEARANCE_STERN |
| 8 | OFFICER_SECOND | 19 | SHIP_CLASS |
| 9 | OFFICER_THIRD | 20 | ELITE_STATUS |
| 10 | OFFICER_FOURTH | 21 | MAX |

The enum full-name strings are in the binary's `.rdata` utf-16 pool; they are
NOT in any extracted asset name table.

### The popup chain, verified end to end

Cooked `UI_EditShip_Panel_Appearance` calls BP function
`GetPossibleAppearanceDataForShipSlot` → native thunk `0x140BB2020` (no unwind;
bumps a UFunction refcount and jumps) → `0x140AC00E0` (guard: the ship-edit data
object at `[singleton+0x4F0]` NULL ⇒ **popup shows nothing at all** -- one of the
possible empty states) → `0x140AB2690` (ENTRY `0xAB272B`-adjacent chunks; caches
its result at `[dataObj+0x108]`; PC via `0x140AAAED0`; **fleet manager =
`[PC+0x958]`**; bail if the frontend component `[PC-cast+0x550]` is null).

Inside, the slot search is `0x140358500` (ENTRY `0x358500-0x3585BE`, 173 B,
self-contained UYFleetManager member -- it owns the only LEA of its log
literal):

- rows at `[fm+0x30]` (ptr), `[fm+0x38]` (Num, int32 movsxd);
- row stride **0x50**; slot test `BYTE [row+0x40] == LoadoutType` (11-18 for
  appearance slots);
- on hit: returns **the result of the nativized helper
  `0x142A16490` (ENTRY `0x2A16490-0x2A16557`, 199 B)** -- NOT a found-bool.
  It moves the row's FString ptr at `[row+0x20]` into a local, walks to
  `[YMmogModule+0x3518]`, queries by FName + FString ('transactionId',
  'settings', 'slot', 'fleet', 'flag' all in its string pool), and its bool is
  the search's result;
- on miss: LogYFleetManager line 1385, verbosity-gated
  ('FleetManager.cp' path, fmt `0x142E6D030`).

Found-path assembly: three delegate inits via `0x140BFBF10`, then
`0x140A9B750` / `0x140A9B1F0` / `0x140A95780` (rcx = frontend component,
rdx = result local) fill three arrays whose per-element release loops release
0x18-stride objects (`0x140BFC9C0`). Hit result is cached to
`[dataObj+0x108]`.

### The row struct (copy ctor `0x140332CC0`, dtor `0x140236EF0`)

Stride-0x50 **recursive tree-node** ("array of arrays" shape, the nativized
tech/fleet graph node):

- +0x00 DWORD
- +0x08/+0x0C/+0x10 TArray, elements stride 0x10
- +0x18/+0x1C/+0x20 TArray, elements stride **0x50 -- recursive**
- +0x28/+0x2C/+0x30 TArray, elements stride 0x10
- +0x38/+0x3C/+0x40 TArray (the Num at +0x40 shares address space with the
  slot byte the popup test reads -- the slot byte is the low byte of that Num
  count, i.e. rows whose type byte is 11-18 are rows of that type)
- +0x48 raw QWORD

Wait, correction while writing this: the test is `BYTE [row+0x40]` against the
LoadoutType, and the +0x38 TArray's Num field would naturally sit at +0x40
(int32). A TArray's Num being 11..18 as a *count* and matching the slot id is
implausible, so the +0x40 byte is a dedicated byte field that follows the
TArray at +0x38 (ptr@+0x38, Max@+0x3C, byte@+0x40). Suspected, not
disassembled twice -- verify before building on it.

### Where the rows come from -- the fill chain

Callers of the copy ctor enumerate the record writers:
`0xAB2C50` (chunks, iterating `[r15+0x3830]` on the MMOG module),
`0x3C8480-0x3C972A` (TuneManager context -- logs
'YTuneManager::LoadBackupDataTablesFromAssets(): Sync-loading data table
assets…'; actual inline Add at `0x1403C8EE3`, rows built by nativized helper
`0x142A3BA60` (ENTRY `0x2A3BA60-0x2A3BBDF`, 383 B) and copied to `[obj+0xD0]`),
`0x3D5363-0x3D5528`, `0x3D6340`, `0x3FFE28-0x4018DA`, `0x347E1C-0x347FD4`,
`0x335AB5`, and nativized `0x2A0DC30`/`0x2A0DD30`/`0x2A0EAD0`/`0x2A14270`/
`0x2A18000`.

The blob-fill side: `InitializeFromPlayerData` (0x14034BD00) → fill
`0x14034FF90` → blob fetch `0x1405B9360` → `[module+0x3898]` fleet manager →
field move-ctor `0x1405A9770` (ENTRY `0x5A9770-0x5A9D2D`, 1469 B), then an
element loop of stride **0x68** temp rows with ctor `0x140332FB0`: each row's
dword id (+0x00) resolves through a 14-bit-chunked id registry
(`[(id>>14)][(id&0x3FFF)*8]`, entry must be non-null, else the row is SKIPPED),
dedupes against already-created runtime item objects (their id at
`[obj+0xB0]`), then creates the object via `0x14034D690` (ENTRY
`0x34D690-0x34D801` -- copies FName→`[obj+0xB0]`, vtable+0x238/+0x240/+0x260,
word→`[obj+0xC0]`, byte→`[obj+0xD8]`, int32x4 blocks→`[obj+0x158]`/`[obj+0x168]`).

An on-demand dispatcher `0x1403585C0-0x358660` resolves 'YMmogbrain' (ASCII
literal `0x142E31278`), reads `[module+0xE40]`, and if empty broadcasts a
delegate that targets `0x14034BD00` -- meaning the rows may simply never be
built on our server if that chain is never triggered. This is the current
leading hypothesis for the empty popup.

### Tune tables are NOT the missing piece

strxref on every tune-table name: exactly 1 LEA each for
WeaponsTune/ProjectilesTune/AbilitiesTune/OfficersTune/FeatsTune/
BattleReadyTune/HavocTune/GameModifiersTune; **zero** for
AppearanceTune/LoadoutTune/ShipLoadoutTune/ItemsTune/CatalogTune/VehiclesTune/
ShipclassTune -- those strings do not exist in the binary. mmogbrain's
`tuneTablesInPriorityOrder()` (response_builders.go) already sends exactly the
8 real ones. So ship-appearance options never travel in a tune table.

### Captain vanity is a different subsystem

The LogYUIUtil flood 'Unhandled character vanity slot type <0>' (17,782 hits in
the client log) is captain-level vanity handled by GetCategoryImagePath
(`0x4E67C0-0x4E75E4`) + switch `0x140324C40` mapping slot values 9..14; it is
not the ship-appearance popup.

### Open (the remaining question of this cycle)

Which of the record writers feeds `[PC+0x958 fm]+0x30` specifically, and which
blob field it reads -- decided between: (a) built from the fleet/loadout blob
the server already sends (then the one-change is adding appearance entries
there), or (b) built lazily by the on-demand dispatcher `0x1403585C0` chain
(then the one-change is triggering/emptying `[module+0xE40]` so the dispatcher
runs). Both are server-side only; the client stays stock.

### Addendum (same round, offline): the fill gate inside UYLoadoutManager

- UYLoadoutManager ctor entry `0x333860-0x3339A7` (+chunk): vtables
  `0x142E81488`/`0x142E817D8`; flag DWORD `[this+0x238..0x23B]` set to
  `0x10000` at construction -- i.e. byte +0x23A starts as **1**. Chunk
  0x3339A7-0x333A06 then overwrites byte **+0x238** with
  `(GetPlatformFeature([0x144091dd8]+0x1190, featureIdx=1) == 3)`
  (checked via leaf 0x140309640 -> 0x140CB6D30). A live-flag legend:
  +0x238 = appearance/loadout feature enabled, +0x23A = set by ctor,
  +0x23B = "InitializeFromPlayerData ran" (set at 0x14034be42).
- The fill wrapper `0x14034FF90` requires byte +0x23A != 0 AND byte
  +0x238 != 0; with +0x238 == 0 it calls **UYLoadoutManager::
  LoadInstallingLoadouts** (`0x34F1D0-0x34F1FB`, iterates
  `[this+0x128]/[+0x130]` stride 0x10; its log literal
  'LoadoutManager::LoadInstallingLadouts | Unable to Load Installer
  Loadout [%s]') and fills nothing.
- `FindLoadoutByID` lives at ~0x14034AB80 (log literals
  'UYLoadoutManager::FindLoadoutByID | Dind't find any loadouts matching id
  %s.' / 'Received NAME_None.'); it lazily calls InitializeFromPlayerData
  (0x14034bd00) and the on-demand dispatcher 0x1403585c0 when the id misses.
  Live log: 1,794 FindLoadoutByID calls (hero-loadout misses only) -- so the
  manager's lazy-init chain DOES run on our server.
- The fill body (0x14034FFD7-0x1403503F2): copies blob loadout rows (stride
  0x68 from blob fields `[rbp+0x138]/[+0x140]`), validates each row's dword
  id against the chunked ItemIDTable ([tableObj+0x400] bounds, 14-bit
  registry), dedupes, then `NewObject`-creates a runtime item object and
  calls `UYLoadoutManager::AddLoadoutInternal`
  (`0x1403382F0-0x140338542`, log 'AddLoadoutInternal | Tried adding loadout
  howerver null.' / 'AddLoadout | %s | %i' with kind byte=2) which appends a
  stride-0x30 bucket to `[this+0x108]/[+0x110]`.
- 'DreadGamePlatformFeatures' turned out to be a Yager UI module (alongside
  Voice/WebBrowser/CEF3Utils in the same stub family), NOT server-fed data --
  the +0x238 feature check runs against engine-local state.
- Pop-search class confirmed UYFleetManager by its literals
  ('IsFleetBattleReady', 'PurchaseBattleReadyState'); the remaining
  unknown is still what writes `[fm+0x30]` (the stride-0x50 node array the
  popup scans). Module+0x3830 candidates live nativized-side; not traced
  to a blob field yet. Run client.log has zero 'player custom loadouts'
  messages (the InitializeFromPlayerData failure branch never logged).

### Addendum 2 (evening, same day, offline): the type byte that slots appearances

The popup's per-slot option list is NOT the owned-items list. It is an
enumeration in two layers, now traced end to end (all verified in-binary):

1. **Collector `0x140A9E350`** (ENTRY `0xA9E350-0xA9E34D`): gathers the
   tech-tree manager's item-id list (`[mgr/fm+0x968]` → `0x1403F9FC0`, the
   same gather gate `ComposeShipManufacturerDataForLoadout` uses), then per
   id: lookup `0x140480F70` → record, **`cmp BYTE PTR [rec+0x10D], r13b`
   (r8d arg = the wanted LoadoutType, 11-18 for appearance slots)** -- only
   records whose type byte matches the slot enter the option list. A second
   byte filter `cmp BYTE PTR [rdx], bpl` walks the record's children
   (TArray at `rec+0x0`, stride 8) and dedupes `[rdx+4]` ids into the out
   array.
2. **Provenance of `rec+0x10D`** (the type byte): the tech-tree cached
   record builder `0x14047EAB0-0x14047F2E6` writes `[rec+0x10D] =
   [loadoutItem+0xD8]` (0x14047EBF7) or `= [rbx+0xED]`, or constant 0x5.
   The loadout runtime item's `+0xD8` is set by the item ctor **0x14034D690**
   (`ENTRY 0x34D690-0x34D801`) as `movzx eax,BYTE PTR [rdi+0x28];
   mov BYTE PTR [rbx+0xD8],al` (0x14034D72E) -- i.e. **BYTE 0x28 of the
   0x68-stride precast/loadout blob row**.
3. **Blob row +0x28 is a DWORD, copied by converter `0x14034E550`**
   (ENTRY `0x34E550-0x34E68E`): `mov eax,[rbx+0x28]; mov [rdi+0x28],eax` from
   the loaded tech/loadout row row into the ctor's source struct. Only the
   low byte of that DWORD matters anywhere (all three consumers are byte
   reads).

So: the client classifies every blob row into an appearance slot **by the
row's own LoadoutType byte at blob-row +0x28**. A cosmetic row whose byte is
not 11-18 can be owned, listed, and still never reach any appearance slot --
exactly the "visible but not usable" symptom. The remaining offline question
(never confirmed, do not build on guesses): **which wire field parses into
blob-row +0x28.** 0x1405A9770 and 0x140332FB0 are move/copy ctors only
(verified -- 0x1405A9770 copies [rdx+0x28]→[rcx+0x28] verbatim); the writer
is in the wire parse, unmapped. Candidates to test next cycle: a `LoadoutType`
or `Type` field on module rows (grep'd `.rdata` for `LoadoutType` utf16:
only the enum-name hits `EYLoadoutType` at 0x5FD608/0x8C22C3 -- the UProperty
name, which is what a wire parser would look up by FString→FName).

### Addendum 3: what the screenshots actually showed

Verified from cooked data, closes cycle B's observations:

- Every popup tab's option list = the ship precast's own `m_appereance`
  struct (`data/loadouts/PrecastLoadouts_cooked.jsonl`): exactly one item per
  slot -- m_heroShipParts (4× `VAN_H_AssaultM_{Bridge,Forecastle,Hull,Stern}_
  Default_DA`), m_emblem `VAN_EMB_Default_DA`, m_paint
  `VAN_PN_Manufacturer_Jupiter_DA`, m_pattern `VAN_PTN_AssaultM_Default_DA`,
  m_decal `VAN_DCL_Default_DA`. That is why paint showed exactly "Jupiter
  Arms", pattern exactly "Default", emblem/decal exactly "None": the option
  list is per-loadout appearance data, not the whole owned catalog.
- "Jupiter Arms" = `VAN_PN_Manufacturer_Jupiter_DA`, id 385810573 =
  **0x16FF000D**, icon `UI_Icon_VanityItems_Coating_JupiterArms` -- the
  precast's own manufacturer paint (17 of the AssaultMedium-family precasts
  carry it; Akula-brand precasts carry Manufacturer_Akula_Green ×19, Ober
  ×16). NOT one of the four vanity_probe ids; the probe ids never produced
  tiles and were never what surfaced.
- Cycle B (cosmetic ids into PurchasesData → `+0x3F90` →
  GetTechTreeItemState 0x543890 → state 4) made the precast-referenced
  cosmetic selectable -- the one visible change reported in play
  (default→JA coating preview worked). Ownership is the availability layer;
  the type byte above is the slotting layer.
- Display-string cosmetics (Agosta emblem 0x15FF00EA, coating
  0x16FF0041 VAN_PN_Winter_Vanity_01) have NO client item records -- tiles
  for them could not resolve names even if slotted.
- Ship-part slots' 5 visible-but-padlocked entries are the hull line's
  precast tier-variant loadouts; suspected genuine tier gating, not a bug.

**Proposed ONE change for the next test cycle:** make the cosmetic blob rows
carry the LoadoutType byte that maps to blob-row +0x28 (coating 11, pattern
12, emblem 13, decal 14, ship parts 15-18) -- after confirming offline which
wire field parses into +0x28. Fallback if it cannot be set: extend the
compose gather surface instead (0x1403F4F20's item-id list).
