# dn-dedicated

A local, headless dedicated server for **Dreadnought**, driven from the command
line and compatible with the
[Dreadnought-Revival-project](../Dreadnought-Revival-project-master).

Dreadnought never shipped a dedicated-server build. The battle server is the
**ordinary client executable** run as a headless listen server. That method was
established by the Revival project's `game-manager`; this tool uses the identical
argv and the same HTTP contract, so it can stand in for `game-manager` or run
entirely on its own.

## Build

Needs Go 1.24+ (developed against 1.26.5). No third-party dependencies, so it
builds offline with no module cache:

```bash
go build -o dn-dedicated.exe .
```

## Quick start

```bash
dn-dedicated run --map Gorge --mode TDM --port 7777
```

That launches one headless battle server in the foreground and blocks until the
engine exits or you press Ctrl+C. No window, no GPU, no Steam client. It prints
`READY` once the engine reports it is hosting.

**On "no window".** `-nullrhi` stops the engine *rendering*; it does not stop it
creating a game window, there is no `-RenderOffScreen` in UE 4.13, and a truly
windowless mode would need a Server target binary this game never shipped. So the
window is suppressed at the OS level instead: the process is started with
`STARTUPINFO.nCmdShow = SW_HIDE`, backed by a poll that hides any visible
top-level window the child creates later. Measured — without this, one visible
`DreadGame` window appears; with it, the process owns six top-level windows, none
visible, and `MainWindowHandle` is 0. Pass `--show-window` to get the window back
for debugging.

The game executable is found automatically if the `Dreadnought/` install sits
beside this tool; otherwise pass `--game-binary` or set `GAME_BINARY`.

```bash
dn-dedicated maps      # playable maps and their package paths
dn-dedicated modes     # game modes and aliases
dn-dedicated args      # print the exact argv "run" would use, without launching
```

## Running the control plane

`serve` exposes the same HTTP API as `game-manager`, so mmogbrain's matchmaker
can drive it unchanged:

```bash
INTERNAL_API_KEY=<secret> dn-dedicated serve --addr :8085 --port-start 7777 --port-end 7877
```

| Route | Auth | Purpose |
|---|---|---|
| `POST /instances` | `X-Internal-Key` | launch a match |
| `GET /instances` | — | list running matches |
| `DELETE /instances/{id}` | `X-Internal-Key` | stop a match |
| `GET /health` | — | health and port usage |
| `GET /metrics` | — | Prometheus text format |

Add `--allow-mock` to record instances without a game binary, which is how the
matchmaking pipeline is tested end to end. It also relaxes the startup check, so
`serve --allow-mock` runs on a machine with no game installed at all.

Request and response shapes, status codes, and validation limits match
`game-manager/main.go`. To use it in place of `game-manager`, point mmogbrain at
it:

```bash
GAME_MGR_URL=http://127.0.0.1:8085
```

`INTERNAL_API_KEY` must match the one mmogbrain uses, or every match request
returns 403 and the matchmaker silently rolls its queue back to `waiting`.

Client commands talk to a running `serve`:

```bash
dn-dedicated start --map Highlands --mode TDM --player alice --player bob
dn-dedicated list
dn-dedicated stop <instance-id>
dn-dedicated status
```

## Configuration

Flags take precedence over environment variables. Env names match the Revival
stack so an existing `run/secrets.env` works as-is.

| Variable | Flag | Default | Meaning |
|---|---|---|---|
| `GAME_BINARY` | `--game-binary` | auto-detected | path to `DreadGame-Win64-Shipping.exe` |
| `WINE_EXE` | `--wine` | `none` on Windows, else `wine` | Wine executable |
| `SERVER_IP` | `--server-ip` | `127.0.0.1` | address handed to clients |
| `ADDR` | `--addr` | `:8085` | control plane listen address |
| `PORT_RANGE_START` / `PORT_RANGE_END` | `--port-start` / `--port-end` | `7777` / `7877` | UDP pool |
| `INTERNAL_API_KEY`, else `ADMIN_KEY` | `--internal-key` | — | required by `serve` |
| `MASTER_URL` | `--master-url` | `http://127.0.0.1:8084` | master-server, with `--register` |
| `DN_LOG_DIR` | `--log-dir` | `logs/` beside the binary | per-instance logs |

`run` needs no key at all — it talks to nothing. Only `serve` requires one,
because its write routes can spawn and kill game processes.

## Logs

Each battle server gets its own file under `--log-dir`, e.g.
`logs/battle-20260801-233216-port7803.log`, containing the argv it was launched
with and every line the engine emitted, timestamped.

**This is deliberate and matters.** Every process using a given install writes to
the same `%LOCALAPPDATA%\DreadGame\Saved\Logs\DreadGame.log` — including your own
game client. Running a battle server while the client is open produces one
interleaved file where the frame counters jump backwards, and each process
rotates the other's log away on startup. That was observed directly here: a
client at frame 506 and a battle server at frame 323 writing alternating lines
into one file.

`-ABSLOG=<path>` looks like the fix for this and **is not one on this build.**
Tested against the shipping executable: with `-ABSLOG` the engine stops writing
`DreadGame.log`, so the switch is definitely parsed, but it never creates the
target file either — leaving no engine log at all. That reproduced both with a
path containing spaces and with a plain `C:\dnlogs`, and the raw command line was
confirmed to reach the process correctly quoted, so it is not a quoting problem.
Passing it would make diagnosis strictly worse than passing nothing. The engine
therefore keeps its default log, and this tool captures the child's
stdout/stderr instead.

## Compatibility with game-manager's spawner

**Drop-in for everything mmogbrain does.** Verified by reading
`game-manager/spawner/spawner.go`, `game-manager/main.go`,
`mmogbrain/matchmaker/matchmaker.go` and `master-server/handlers/handlers.go`,
and by exercising the API.

Identical: the launch argv (one addition, below), the working directory rule, the
`/instances` routes with their request/response shapes and status codes, the
`X-Internal-Key` auth, the validation limits (20 players, 64-char ids, 100-char
mode/map, `^[A-Za-z0-9_]+$`), the port-pool range and its 503, and the
`master-server` register/deregister payloads.

### Deliberate differences

| # | Difference | Effect on the Revival stack |
|---|---|---|
| 1 | argv adds `-forcelogflush` | none; a dying server leaves its lines. Only argv delta |
| 2 | Heartbeats every 20s with `--register` | **fixes** a real bug — see below |
| 3 | Empty map defaults to `Highlands`, not `Charon` | **fixes** a real bug — see below |
| 4 | Mock instances need `--allow-mock` | none unless you relied on it happening silently |
| 5 | `/metrics` is a reduced set | no `go_*` / `process_*` series |
| 6 | Responses add `map_path`, `pid`; request accepts `max_players` | additive, ignored by existing callers |
| 7 | Unknown game mode / unknown map name → 400 | never triggered by mmogbrain (verified) |
| 8 | `map_path` must start with `/Game/` | never triggered by mmogbrain (verified) |
| 9 | `/health` reports `"service":"dn-dedicated"` | only matters if something string-matches it |
| 10 | No per-instance Wine prefix fallback, no temp config dir | Linux only; see below |

**(2) Heartbeats.** `master-server` marks a server offline once its
`last_heartbeat` is over 60 seconds old (`handlers.go` `StartCleanup`). The
spawner registers instances and then never heartbeats, so every match it starts
is flagged offline about a minute in while still running.

**(3) Default map.** `game-manager/main.go:126` defaults an empty map to
`"Charon"` — one of the invented names `CONTRIBUTING.md:20` documents as a costly
past mistake. It has no package path, so that default loads nothing.

**(7)(8) Stricter validation.** These reject inputs `game-manager` would pass
through. They cannot fire on the real path: both queue-join handlers
(`mmogbrain/handlers/handlers.go` and `response_builders.go`) call
`ValidGameMode` before `NormalizeGameMode`, so `queue_entries.game_mode` only
ever holds a canonical `clientGameModeConfigs` name, and the matchmaker always
sends a real `map_path` from the client's own table. The mode and alias tables
here mirror the matchmaker's exactly.

**(10) Wine environment.** When launching through Wine, `WINEPREFIX` is
inherited and `GAME_WINEPREFIX` overrides it, as in the spawner. Dropped: the
spawner's fallback of manufacturing a fresh per-instance prefix under a temp
config dir when neither is set. Its own comment records that this "never
worked" — the engine needs a configured prefix and exits with status 3 against
an empty one — and keeps it only "for a caller that has genuinely configured
nothing". Inheriting the default prefix is what that caller actually wants. The
temp config dir existed only to hold that prefix, so it is not created either.
On Windows none of this applies; the Wine variables are not set at all.

## Caveats

**Windows cannot detect a busy UDP port.** Windows permits a second UDP bind to
the same address and port unless the first socket set `SO_EXCLUSIVEADDRUSE`,
which the engine does not. So the pre-launch port check finds nothing on Windows
and the pool's own bookkeeping is the only protection against a collision. Don't
read a successful launch as proof the port was free.

This also broke readiness detection in an earlier version of this tool, which
probed the port and treated "cannot bind" as "engine is up" — it never fired on
Windows, and `run` blocked until the process exited even though the server was
live and bound. Readiness now comes from the engine's own
`Match State Changed ... WaitingToStart` line, which is verified to arrive.

**Unreal parses the raw command line.** On Windows, `-Key=Value` switches with
spaces in the value must be quoted *after* the `=`, which Go's argv escaping does
not produce. `internal/server/rawcmdline_windows.go` takes over command-line
construction for this. It is inert on Linux.

**The mod DLL is injected if present.** If the install has the
`DreadnoughtTestBench` mod deployed (`wer.dll` → `Dreadnought.dll` next to the
exe), it loads into battle servers launched from that directory too, since the
engine resolves it from its own working directory. That is usually not what you
want for a Revival-stack setup.

**Match state with no players.** The engine goes `EnteringMap` →
`WaitingToStart` → `InProgress` immediately, without waiting for anyone to
connect, so `InProgress` does not imply players are present.

## Layout

```
main.go                        CLI entry, config resolution, binary discovery
commands.go                    run / serve / start / stop / list / status / maps / modes / args
internal/server/instance.go    the verified argv, launch, supervision, readiness
internal/server/manager.go     port pool, instance registry, heartbeat lifecycle
internal/server/rawcmdline_*   Windows raw command-line construction
internal/api/api.go            game-manager-compatible HTTP control plane
internal/master/client.go      master-server register / heartbeat / deregister
internal/gamedata/gamedata.go  map and game-mode tables
internal/ids/ids.go            UUIDv4, so google/uuid is not needed
```

## Adding it to the Revival workspace

It is a standalone module and does not need to be. If you want it in the
workspace, add `./dn-dedicated` to `go.work`'s `use` block. Note that the Revival
repo's `go.work` declares `go 1.25.0`, and this module declares `go 1.24`.

## Data provenance

The map and game-mode tables in `internal/gamedata` are copied from the Revival
project's `mmogbrain/matchmaker/matchmaker.go`, which took them from the client's
own `GlobalUI.uasset` (`UYUIData::m_multiplayerMaps`). Nothing there is invented.
If you add a map, cite where in the client's data you found it — names like
Charon, Medusa, Procyon, Iapetus and Kalyke are not maps this game has, and
believing otherwise has cost this project real time before.
