---
name: dreadnought-verify
description: How to prove a change to Dreadnought actually worked. Use before claiming any fix, crash-rate result, or "this hook is the problem" conclusion - the game is non-deterministic, so a single passing run proves nothing, and the logs routinely report success while the screen stays black. Also covers isolating whether the mod or the server caused a bug, and the headless harness for fast repeatable runs.
---

# Proving a change worked

This is a dead game brought back by injection. It is non-deterministic, badly
observable, and has already absorbed weeks of work that was reported as done and
wasn't. The techniques here exist because each one caught a wrong conclusion.

**Note for the server side.** This was written for the client mod, so the `DN_*`
switches and `bisect.ps1` (PowerShell) are client-specific — and `DN_INERT=1` is
how you get a *clean* client to test your server against, which is the most
useful part here. The first two rules are not client-specific at all and are the
reason this skill is worth having: they are about what counts as evidence. The
headless harness may also be directly reusable, since it launches a listen
server the same way `game-manager/spawner` does.

## The three rules

### 1. A log line is not a working feature

The single most expensive failure mode on this project: a metric in the log says
success, the screen is as broken as ever. `Start() returned 1` on the HUD movie,
with nothing drawn. `AddAndActivateLoadout` returning a valid pointer, with the
player still in spectator.

**Anything the user can see must be confirmed by the user seeing it.** Ask for a
screenshot or a description. Do not report a visual fix as done on log evidence.

Corollary: when you add diagnostics, log the *state you care about* — the pawn
pointer, the input flags, the actual camera POV — not the fact that your code
ran.

### 2. One run is one data point

Startup races here are real. The same build hosted a map 6/6 times without the
mod and 1/6 with it — and a single-run bisect earlier had confidently blamed one
specific hook, which turned out to be innocent.

**Run at least 6 times per configuration before believing a crash-rate claim.**
Report it as a fraction: "3/6 with the hook, 6/6 without". A single failure
tells you almost nothing; a single success tells you less.

### 3. Establish whether it's even yours

`DN_INERT=1` disables the mod completely. With a real backend present, that is
the reference control for *"does the mod cause this?"*

This settled a whole class of argument once: `DN_INERT=1` against the revival
server gives a fully working game — login, player data, tutorial match, HUD,
weapons, VFX, persistence, matchmaking. So any bug that survives `DN_INERT=1` is
the server's, and any bug that disappears is ours. Check this **before**
investigating, not after.

## The headless harness

`scripts/bisect.ps1` hosts a map with no renderer and reports whether the
process survived. About 7 seconds per run, versus minutes through the launcher.

```console
PS> .\bisect.ps1 -Inert 1
=== RUN: rvaMax=(all) rvaOff=(none) inert=1  port=7790 ===
VERDICT: SURVIVED after 6.8s
```

`SURVIVED` means the process bound UDP on the listen port — the map finished
loading and it is accepting players. `CRASHED` means it died first.

Six runs on separate ports, which is what a real answer looks like:

```powershell
1..6 | ForEach-Object { .\bisect.ps1 -RvaMax 40 -Port (7790 + $_) }
```

Set `DREADGAME_EXE` if the binary isn't at the default path.

### Bisecting which hook breaks something

The mod reads these at startup, so you can bisect without rebuilding:

| Switch | Effect |
| --- | --- |
| `DN_INERT=1` | install nothing at all — the control |
| `DN_RVA_MAX=<n>` | install only the first n RVA hooks |
| `DN_RVA_OFF=<list>` | skip specific RVA hooks |
| `DN_HOOKS_MAX=<n>` / `DN_HOOKS_OFF` | same for the named hooks |
| `DN_NO_VEH=1` | disable the vectored exception handler |
| `DN_NO_PATCHES=1` / `DN_NO_GCPATCH=1` | skip byte patches |
| `DN_NO_SETTIMER=1` | skip the SetTimer hook |

**Turn the VEH off when investigating a crash.** It suppresses access violations
— fifty of them in one observed run — and then executes stack memory before
`ExitThread`, so the crash you finally see has nothing to do with the first
fault. `DN_NO_VEH=1` shows you the real one.

Remember rule 2 while bisecting. A race will happily give you a clean bisect
onto an innocent function if you run each configuration once.

## Reporting

Say what you actually observed, in these terms:

- **verified** — you saw it, or the user saw it. Include the evidence.
- **suspected** — it follows from something else. Say what.
- **fractions, not adjectives** — "4/6 runs", never "usually works".

If a theory was wrong, say so plainly and move on; three wrong theories preceded
the real cause of the black screen, and naming each one as dead is what got to
the fourth. Do not report a fix as complete because the code path executed.
