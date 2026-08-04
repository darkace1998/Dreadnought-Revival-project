# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A community private server for the discontinued game **Dreadnought** (UE4 4.13.1, Steam App 835860). The goal is to run the **unmodified game client** against an emulated backend; the only thing a player installs is the replacement launcher (`dn-launcher/`). Patching the game executable is out of scope and not accepted.

One narrow exception, added 2026-08-04: `battle-server-mod/` holds an optional DLL for the **battle server** (the same executable run headless by an operator, never by a player). The host's loadout manager can only be filled from a login it never performs, so without it nobody can spawn. It must remain optional at runtime — see `battle-server-mod/README.md` for what qualifies.

A Go workspace (`go.work`, Go 1.24+ required) of independent modules under `github.com/darkace1998/Dreadnought-Revival-project/<service>`.

## The one rule: never invent data

Everything the server sends must be traceable to the client's own data tables (`data/`) or to the client binary. Invented-but-plausible values have repeatedly cost days here, because they look right and get built on. If a value cannot be determined, mark it `// GUESS:` with what evidence is missing. Debugging order: read the client's log → find the string in the binary → read the parser → only then hypothesize about the payload. Full working rules are in `CONTRIBUTING.md` — read it before touching anything protocol-related.

## Commands

```bash
bash scripts/setup.sh                         # build all services into run/, plus certs + secrets on first run
cd auth-server && go build -o ../run/auth-server .   # build a single service

bash scripts/start-services.sh                # start the stack (logs in run/<service>.log)
bash scripts/stop-services.sh                 # stop it
curl http://127.0.0.1:8081/health             # health checks: 8081-8085 per service
```

### Tests and lint — must run per-module, not from the workspace root

`go test ./...` from the repo root fails ("directory prefix . does not contain modules"). Run inside each module:

```bash
cd mmogbrain && go test ./...
cd mmogbrain && go test -run TestPayloadSizesVerify ./...   # single test
cd mmogbrain && go vet ./...
cd mmogbrain && golangci-lint run ./...       # per-module; root run gives misleading results
```

`shared/dreadgameconfig` and `legacy-api/handlers` tests used to **panic without `DATA_DIR`**: a package-level `init()` loaded game data through a cwd-relative default that only resolved from a module root directly under the repo root, and it ran before any `TestMain` could fix it. `DataDir()` (`shared/dreadgameconfig/loader.go:34`) now walks upward looking for a `data/` directory holding the extracted assets, so both suites pass with `DATA_DIR` unset — checked with `env -u DATA_DIR go test -count=1 ./...` in both modules. Set it to point at a different extraction; it still wins when present.

`dn-dedicated` is a separate module and deliberately not in `go.work`, so it needs the workspace switched off:

```bash
cd dn-dedicated && GOWORK=off go build ./... && GOWORK=off go test ./...
```

Modules with tests: `mmogbrain`, `shared`, `legacy-api`, `gateway`, `game-manager`, `dn-dedicated`. `go vet` is clean everywhere and must stay that way. `golangci-lint` has a known pre-existing backlog in `mmogbrain`/`shared` — don't add to it, but a clean run is not expected.

## Architecture

```
[Unmodified Windows client]  (hosts-file redirect + our CA trusted)
  → HTTPS :443  → gateway (TLS termination, routes by Host header)
      ├─ profile-api.*   → auth-server   :8081  (registration, login, JWT — auth.db)
      ├─ legacyapi.*     → legacy-api    :8082  (profiles, inventory, match history — legacy.db)
      └─ masterserver.*  → master-server :8084  (server registry, browser — master.db)
  → HTTPS :65443 → mmogbrain "gateway" socket  (Greybox web-services: catalog, inventory, session)
  → TLS   :48843 → mmogbrain Firmament socket  (social/presence + binary YMmogbrain protocol)
  → UDP :7777-7877 → DreadGame-Win64-Shipping.exe  (one Wine process per match)

mmogbrain matchmaker ──match formed──► control plane :8085 ──spawns──► wine + game exe
                                      (dn-dedicated; DN_CONTROL_PLANE=game-manager
                                       switches back to the older service)
```

Naming trap: the **`gateway` service** is the reverse proxy on :443, while **`GATEWAY_ADDR` (:65443)** is a socket *inside mmogbrain*. Two different things.

There is no dedicated-server build of Dreadnought — the battle server is the ordinary client exe launched headless (`"<map>?listen" -server -nullrhi -unattended`). A `-dedicatedserver` switch does not exist in the binary.

`admin-cli/` is the operator CLI; `dn-launcher/` is the Windows launcher replacement (cross-compiled). `shared/` holds common packages: `db`, logging, middleware, and `dreadgameconfig` (the extracted-game-data loader).

### Conventions shared by all services

- Handlers: `type Handler struct { DB *sql.DB; Log *logrus.Logger; ... }` wired in `main.go` with `gorilla/mux`.
- SQLite via `shared/db`: WAL mode, `MaxOpenConns=1` (single writer), sequential migrations in `schema_versions`.
- JWT: HS256; claims `sub`, `username`, `realm="dreadnought.pc-us"`, `aud="dreadnought"|"launcher"`; `JWT_SECRET` must be identical across services. Middleware injects `X-User-ID`/`X-Username`; admin endpoints check `X-Admin-Key`.
- Config via `getenv(key, fallback)` only — no `.env` parsing. Runtime config lives in `run/secrets.env` (gitignored; annotated template at `scripts/secrets.env.example`; full variable table in README).
- `logrus` JSON logging; `promhttp` metrics on `GET /metrics` everywhere.
- Greybox-compatible errors: `writeGreyboxError(w, status, code, msg)` with `code <= -32000`, or the launcher shows a generic "service unavailable".

### mmogbrain (the complex one)

Beyond HTTP, it speaks the proprietary binary YMmogbrain protocol on the Firmament TLS socket. Key facts:

- Use the encoding helpers (`appendMmogStringField`, `appendMmogInt32Field`, …), never raw bytes.
- Player PIDs on the binary protocol are UUIDs **with hyphens stripped** (32 hex chars).
- Firmament auth success is deliberately delayed until MMOG `YA_PlayerGet` confirms player data is ready — a real client race, coordinated via pending-auth maps.
- `PLAYERS_PER_MATCH` defaults to 1 for solo testing.
- Debug env switches (`DN_TECHTREE_LIMIT`, `DN_ANSWER_DAILY_CONTRACTS`, `DN_NO_DEFER_PLAYER_FLEETS`, `DATA_DIR`) are documented in README.

### Protocol invariants (verified; easy to break by accident)

Details and evidence in `CONTRIBUTING.md`; headlines:

- **Category law:** the top byte of an item id is its `ItemIDTable` CategoryID (`(id >> 24) & 0xff`); several client gates admit ids only by this byte.
- **Container length excludes its own length field** but includes the 6-byte terminator; off-by-4 makes a container swallow its next sibling, so only the last array element survives.
- **Arrays (tag 0x0d) lose child names**; lists the client also looks up by name must be sent as objects with children named `"0"`, `"1"`, … (`protocol.AppendIndexedStringListField`).
- The binary mmog parser compares field names **case-insensitively**; the JSON catalog lookups do **not** — `Name` and `name` are both required, carrying different values.
- `<DNT>[[NotFound]]` in-game means a **missing field**, not a failed localization.
- Three distinct enums are all called "ship class" — see `docs/client-data-reference.md` before mixing them.

### TLS

`certs/server.crt` serves the gateway; the Firmament socket uses `certs/firmament.crt`, whose issuer is spoofed as "Amazon RSA 2048 M01" to satisfy the client's CA pinning. `scripts/gen-certs.sh` builds both.

## Working on tests

- `TestPayloadSizesVerify` pins the exact byte size of every mmogbrain response as a deliberate tripwire. When a change moves a size, don't just update the number — add a line to the comment block above it explaining what grew/shrank and why.
- Tests encode client behaviour, so a failing test may itself be wrong (it has twice faithfully defended a disproved theory). Decide which before "fixing" either side, and write down why if you change the assertion.
- Comments in this codebase carry reverse-engineering findings that exist nowhere else — keep them dense, cite binary function addresses, and when you disprove a comment, correct it in place rather than deleting it.

## Commits

Lowercase, scoped subjects: `techtree: ClassId must be an item id, not the EYShipClass ordinal`. For protocol changes the body must record the evidence (function, offset, client log before/after) and state plainly what was **not** verified against a running client.

## Reference docs

- `CONTRIBUTING.md` — working rules, protocol invariants, debugging method (read first)
- `AGENTS.md` — agent-oriented gotchas and current test status
- `docs/client-data-reference.md` — extracted item/ship id maps and naming rules
- `docs/client-data-validation.md` — audit of every id the server emits
- README — setup, configuration variables, troubleshooting
