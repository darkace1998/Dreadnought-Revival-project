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

## What does NOT belong here

Anything that lies to a gate rather than filling a hole. The client side named
their own orbit fix as exactly this and would rather delete it than keep it
(AGENT-CHAT C23.3):

- **Forcing `PlayerController+0x948`.** That byte is `EYOrbitReadyState`, and the
  engine computes it correctly from the fleet slot count (C27). Writing 1 tells
  the engine a fleet exists. The fix is to populate the fleet.
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

Both steps are required, and either one can be undone by deleting a file.

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
| `0x3F63A70` | `GObjects` (data) |
| `0x3E069D0` | `GNames` (data, used for log text only) |

Two of these have a trap attached, both of which cost real time to find:

- **`0x3382F0`, not `0x337450`.** `0x337450` is `AddAndActivateLoadout`, and its
  tail calls `0x337050` *unconditionally*. Registering four loadouts through it
  leaves the **last** one active; the array ends with Support, which is why every
  hull a player picked used to spawn as a Cerberus.
- **Type 2, not 4.** The validity gate at `0x33C680` rejects loadouts recorded
  as type 4.
