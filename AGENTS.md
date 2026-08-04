# AGENTS.md — Dreadnought Private Server

Go workspace emulating the Greybox backend for Dreadnought (UE4 4.13.1, Steam 835860).
Read `CLAUDE.md` for the working rules and `CONTRIBUTING.md` before touching anything
protocol-related. This file is the operational stuff: what breaks, what has moved,
and what you can now do locally that you could not before.

## Critical Gotchas

**Tests & lint must run per-module, not from workspace root:**
```bash
# WRONG (fails with "directory prefix . does not contain modules")
cd dreadnought-private-server && go test ./...

# CORRECT
cd dreadnought-private-server/mmogbrain && go test ./...
cd dreadnought-private-server/legacy-api && go test ./...
cd dreadnought-private-server/shared    && go test ./...
```

**`dn-dedicated` is a separate module and is deliberately NOT in `go.work`.**
Build and test it from its own directory with the workspace off, or you get the
same "directory prefix" error:
```bash
cd dn-dedicated && GOWORK=off go build ./... && GOWORK=off go test ./...
```

**`DATA_DIR` is no longer required for `shared` / `legacy-api` tests.** This used
to be a hard requirement — a package-level `init()` loaded game data through a
cwd-relative default that only resolved from a module root directly under the
repo root, so `cd shared && go test ./...` panicked before any `TestMain` could
intervene. `DataDir()` (`shared/dreadgameconfig/loader.go:34`) now walks upward
looking for a `data/` directory holding the extracted assets, so both suites pass
with `DATA_DIR` unset. Verified 2026-08-02 with `env -u DATA_DIR go test -count=1
./...` in both modules. `DATA_DIR` still works and still wins when set — use it
to point at a different extraction.

**On Windows, `stop-services.sh` silently stops nothing.** `pgrep` and `pkill` do
not exist in Git Bash / MSYS, so `if pgrep -x "$name"` is `command not found`,
which takes the `else` branch: the script reports every service as "not running",
never reaches `pkill`, and then `rm -f`s the pidfile — discarding the last handle
on a process it did not stop. The old `mmogbrain` keeps its ports, the next
`start-services.sh` silently no-ops, and you debug against the previous build.
*(Reported and verified by the client side in `AGENT-CHAT.md` C2, with the full
stack live and `tasklist` showing all six. Not reproduced here — this box is
Linux.)* `ps`, `tasklist` and `taskkill` are all present there and could carry a
Windows branch.

**Never `pkill -f <pattern>` on this box.** The `bash -c` command line running
your own script contains the pattern, so you kill your own shell. `pgrep -x` does
not work for the game either — the process name is truncated to 15 characters.
Use `ps -eo pid,comm | awk '$2 ~ /^DreadGame/ {print $1}'` and kill by PID.

**Lint from workspace root reports 0 issues but shows a typecheck error.** Run
per-module for real results: `cd mmogbrain && golangci-lint run ./...`. There is
a known pre-existing backlog in `mmogbrain`/`shared`; don't add to it, and don't
expect a clean run.

**Test status (2026-08-02):** all modules pass — `mmogbrain` (21 test files in
the root package, 3 more in `matchmaker`), `shared/dreadgameconfig`, `legacy-api/handlers`,
`gateway`, `game-manager/spawner`, and `dn-dedicated/internal/server` (with
`GOWORK=off`). `go vet` is clean everywhere and must stay that way.

## Architecture

```
[Windows Client] → HTTPS :443 → gateway (TLS termination + Host-header routing)
  ├─ profile-api.*       → auth-server :8081   (JWT, login)
  ├─ legacyapi.*         → legacy-api  :8082   (profiles, inventory)
  └─ masterserver.*      → master-server :8084 (server registry)

[Client] → HTTPS :65443 → mmogbrain  (Greybox web-services: catalog, inventory, session)
[Client] → TLS   :48843 → mmogbrain  (Firmament JSON-RPC + YMmogbrain binary protocol)

[mmogbrain matchmaker] → :8085 control plane → wine + DreadGame-Win64-Shipping.exe
[Client] → UDP :7777-7877 → that battle server
```

Naming trap: the **`gateway` service** is the reverse proxy on :443, while
**`GATEWAY_ADDR` (:65443)** is a socket *inside mmogbrain*. Two different things.

**The control plane on :8085 is now `dn-dedicated`, not `game-manager`.** Same
routes, same argv, plus per-instance readiness (`GET /instances/{id}` reports
`ready`) and a full per-instance battle server log under `run/battle-logs/`.
`DN_CONTROL_PLANE=game-manager` switches back; game-manager is still built and
still works, it just makes every match wait out `DN_CONNECT_PUSH_DELAY` instead
of travelling when the server reports it is hosting.

## Build & Run

```bash
bash scripts/setup.sh            # build everything into run/, plus certs + secrets
bash scripts/start-services.sh   # start the stack; logs in run/<service>.log
bash scripts/stop-services.sh

cd auth-server && go build -o ../run/auth-server .          # single service
cd dn-dedicated && GOWORK=off go build -o ../run/dn-dedicated .

for p in 8081 8082 8083 8084 8085; do curl -s "http://127.0.0.1:$p/health"; echo; done
```

`run/` is gitignored, so anything in it (including the stale `run/start.sh` with
hardcoded paths) does not ship. `scripts/start-services.sh` is the one to use.

**mmogbrain writes two logs.** `run/mmogbrain.log` is only stdout/stderr; the
detailed JSON request log goes to `mmogbrain.log` **at the repo root**, because
mmogbrain opens it relative to its working directory. Grep the latter when
tracing client requests.

## Running the game client yourself

`bash scripts/wine-client.sh [seconds] [output-log]` runs the real client against
this stack on this Linux box: mints a token with dn-launcher (`DN_PLAYER_ID`, so
it auto-registers a stable test account), launches the client, dismisses the
title screen with xdotool, sits in the hangar, exits. About four minutes per run.
This is the A/B loop — change a response, restart mmogbrain, run it, read the log
— and it removes the "wait for the user to test on Windows" round trip that
shaped most of this project's history.

Requires Xvfb on `:99` and xdotool. **The CPU pinning in that script is not
decoration:** rendering is llvmpipe on the CPU and an unconstrained run has taken
this box down before, killing four services. `taskset -c 4,5,6 nice -n 19 timeout
-s KILL <secs>` on a 12-core host kept load under 2 across four runs with the
whole stack up. Keep it.

`SHOT_DIR=/some/dir` additionally captures PNGs of the running UI at three
points (after the keypress, and twice in the hangar). Use it for anything the log
cannot answer — whether a counter shows a number, whether a preview viewport is
empty. It grabs the X root window, so the frame includes the Wine console window
overlapping the top-left of the game; the game itself is the 1024x576 area at
+128+115. Needs `imagemagick`.

Two things to know before reading its output:

- The client writes **no log file of its own** under Wine, even with `-LOG`.
  Captured stdout is the only record.
- stdout is capped at Display verbosity; `-AllowStdOutLogVerbosity` raises it to
  Log. `-LogCmds` raises *category* verbosity but stdout still filters, so
  Verbose lines never appear. `-FullStdOutLogOutput` does not exist in 4.13.
- Never raise `LogYComVOComponent` past Verbose — the client crashes in
  `PlayVoiceLineInternal`.
- `-stdout` is required for any of that to matter: without it the engine attaches
  no stdout log device at all, and `-AllowStdOutLogVerbosity` raises a stream
  nobody writes to (219 lines vs 570 on the same run).
- The launcher phase must not be piped into `grep`. The launcher starts the game
  itself and the child inherits its stdout, so the pipe outlives `timeout` and
  the harness hangs before the real run ever starts.

The same limits apply to battle servers, which is why the control plane passes
`-AllowStdOutLogVerbosity` and keeps the whole stream per instance.

## Reading the client binary

`.claude/skills/dreadnought-rva/` answers "what does the client actually expect?"
straight from the executable instead of by inference from wire behaviour. Set
`DREADGAME_EXE` to your copy; the scripts are pure stdlib Python and run fine on
Linux against the Windows binary. Smoke-tested here 2026-08-02: 224,934
`RUNTIME_FUNCTION` records parsed, `strxref.py` resolved a live log message to
its function.

- `strxref.py "<log message>"` → the function that prints it. UE4 literals name
  their own class and method, so one hit often hands you the symbol.
- `pdata.py <rva>` → is this a real function entry? Run it on any RVA inherited
  from a comment, a memory file or another agent.
- `callers.py <rva>` → direct callers, for walking outward to a choke point.

**RVA convention:** those scripts use image-relative addresses (`0x370970`); the
Ghidra decompile at `/root/projects/src/Documents/ghidra_decompile` uses virtual
addresses (`FUN_140370970`). Same address, base `0x140000000`. That decompile has
150,731 functions plus `callgraph.db`, which answers "who calls this" with a SQL
query — faster than `callers.py` when you already have the address.

`dreadnought-verify` and `dreadnought-hooks` are also present; `hooks` is
client-mod specific and you will probably never need it.

## Talking to the client side

`AGENT-CHAT.md` is an append-only channel between this repo and the client-mod
half of the project (`AHouseOfBards/DreadnoughtTestBench`). Read the whole file
before writing an entry; the protocol is in it. The parts that matter: append at
the bottom, one commit per entry (`chat: <ID> <summary>`), never edit the other
side's entries, and mark every claim `verified` or `suspected`.

## Key Files

- `go.work` — 9 modules; `dn-dedicated` is not one of them
- `scripts/setup.sh` — build + certs + secrets, one shot
- `scripts/start-services.sh` / `stop-services.sh` — the shipped launcher/stopper
- `scripts/wine-client.sh` — run the real client against this stack, locally
- `scripts/secrets.env.example` — annotated configuration template
- `run/secrets.env` — runtime secrets (gitignored)
- `AGENT-CHAT.md` — the client↔server channel
- `docs/battle-server-data-path.md` — how a battle server gets its data, and why
  a player cannot spawn (the current hard blocker)
- `docs/client-data-reference.md` — extracted item/ship id maps and naming rules
- `docs/client-data-validation.md` — audit of every id the server emits

## Conventions

- **Handler pattern:** `type Handler struct { DB *sql.DB; Log *logrus.Logger; ... }` wired in `main.go` with `gorilla/mux`
- **JWT:** HS256, claims `sub`, `username`, `realm="dreadnought.pc-us"`, `aud="dreadnought"|"launcher"`; 24h lifetime
- **Auth middleware:** injects `X-User-ID`/`X-Username`; admin endpoints use `X-Admin-Key`
- **Database:** SQLite via `shared/db` (WAL, `MaxOpenConns=1`, sequential migrations in `schema_versions`)
- **Logging:** `logrus` JSON; `Info` for requests, `Warn` for admin events
- **Metrics:** every service exposes `GET /metrics`
- **Env vars:** `getenv(key, fallback)` only — no `.env` parsing

## mmogbrain (most complex service)

Beyond HTTP, speaks the proprietary binary YMmogbrain protocol on `:48843`:

- Use the encoding helpers (`appendMmogStringField`, …), never raw bytes
- Player PIDs on the binary protocol are UUIDs **with hyphens stripped** (32 hex chars)
- Firmament auth success is deliberately delayed until MMOG `YA_PlayerGet` confirms
  player data is ready — a real client race, coordinated via pending-auth maps
- Several responses are deferred until after `YA_PlayerGet` (`YA_PlayerFleets`,
  `YA_RequestStaticFleetData`) because the client rejects them when they arrive
  early. `YA_GetTechTree` is deferred until after `YA_Tune` for a different
  reason — both write the same shared document slot
- `PLAYERS_PER_MATCH` defaults to **1** so one tester can reach a battle server
  without a second client. The cost: every player gets a **private** match, so
  two people queueing together never meet, and PvP modes have no bots to fill the
  gap — `AYAICombatSceneManager::StartCombat` fires only under Training Match
  across every host log we have. Set it to 2+ for PvP.

Debug switches worth knowing: `DN_CONTROL_PLANE`, `DN_FORCE_GAME_MODE`,
`DN_CONNECT_PUSH_DELAY`, `DN_GAME_MGR_TIMEOUT`, `DN_ENGINE_LOG_CMDS`,
`DN_MAP_URL_OPTION`, `DN_TECHTREE_LIMIT`, `DN_NO_DEFER_PLAYER_FLEETS`,
`DN_ALLOW_MOCK_INSTANCES`. The README table documents each.

## TLS Certificates

| File | Used by | Notes |
|---|---|---|
| `certs/server.crt` | gateway HTTPS | self-signed; clients trust `certs/ca.crt` |
| `certs/firmament.crt` | Firmament :48843 | issuer spoofed as `Amazon RSA 2048 M01` to satisfy the client's CA pinning |

## Current State (2026-08-02)

Working end to end: registration and login, the hangar, the starter fleet with
weapons/modules/officer slots, inventory, the market catalog, ship and captain
customisation, career progression, matchmaking, battle-server spawning, and the
travel handoff — the client now travels seconds after a match forms rather than
waiting out a fixed delay, because the control plane reports when the engine is
hosting.

**The hard blocker is in-match.** A player reaches the battle server and stays a
spectator, then gets disconnected about 15 seconds in. Two separate causes, both
written up in `docs/battle-server-data-path.md`:

1. The server-side loadout manager is empty and cannot be filled — it populates
   from the local process's mmogbrain data, and a battle server makes zero
   contact with mmogbrain even when handed the gateway/firmament addresses. Only
   a game mode that supplies its own loadout (TM) can spawn a pawn, which is why
   `DN_FORCE_GAME_MODE=TM` exists.
2. The client uploads its tune data to the host over the OTS channel
   (`UYLocalServerDataManager`) in fixed 900-row slices; one slice reassembles to
   ~69.6 KB against UE4.13's hard 64 KB partial-bunch cap, so the host calls the
   packet corrupt and drops the connection.

The first thing worth fixing is why the client ignores our `YA_Tune` response —
`YTuneManager::Set()` is never called, so today the server cannot influence the
client's tune data at all. Four shapes/orderings have been ruled out against a
live client; see the doc.
