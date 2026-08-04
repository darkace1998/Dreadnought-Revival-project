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

**A fix exists, and it is not here.** The client-side team hooks
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

## 1b. The same absence, three more times: orbit, thrusters and an empty match

The empty loadout manager is not one bug. It is the first of at least four
symptoms of one fact -- **the host has no player record** -- and each was found
separately before the shape became obvious. Recording them together so the next
one is recognised on sight.

| symptom | what actually reads the missing data |
| --- | --- |
| `FindLoadoutByID` misses every id | loadout manager, filled only from player data (above) |
| the teleport out of orbit never fires | `PlayerController+0x948` = `EYOrbitReadyState`, computed by `FUN_140346C20` from the fleet **slot count**; count 0 returns 0 |
| thrusters and muzzle effects never switch on | suspected: the orbit transition that the forced flag skips (client side, unconfirmed) |
| **the match has no opponents or bots** | the NPC set is chosen by FLEET TYPE (below) |

The bot chain, verified by disassembly:

```text
FUN_1403A0A80  LoadNPCSet(fleetType)
  switch (fleetType) { case 1,2,3: load the set; default: "LoadNPCSet | Invalid fleet type!" }
     ^
FUN_140396C50  the fleet-type getter
  ...
  call FUN_14039A2C0        ; the local player controller
  mov  rcx, [rax + 0x958]   ; m_fleetManager -- the SAME pointer the orbit gate checks
  test rcx, rcx / je -> default   ; null fleet manager -> default type -> no NPC set
```

So a host with no fleet manager gets the default fleet type, `LoadNPCSet` refuses
it, no NPC set is ever loaded, and `AYGameMode_Multiplayer::SpawnNPC` then has
nothing to draw on ("not enough npcs in m_npcPlayers"). The empty match is not a
missing feature on the backend: **the game populates matches itself, off the
fleet.** `AYGameState_MP::LoadNPCSetOnFleetInitialized` says as much in its name.

That makes populating the host's fleet slots the single lever for all of it --
which is what the client side reached independently in AGENT-CHAT C27 from the
orbit side alone.

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
