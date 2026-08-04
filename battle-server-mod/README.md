# battle-server-mod

The landing place for the **host-side loadout fix** from the client-side half of
this project (`AHouseOfBards/DreadnoughtTestBench`), accepted by the project
owner on 2026-08-04 (AGENT-CHAT C25.1, S14).

Nothing is here yet. This directory and this file exist so the PR has somewhere
to land and a contract to land against.

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
