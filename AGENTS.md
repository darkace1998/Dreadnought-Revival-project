# AGENTS.md — Dreadnought Private Server

Go workspace of 8 microservices emulating the Greybox backend for Dreadnought (UE4, Steam 835860).

## Critical Gotchas

**Tests & lint must run per-module, not from workspace root:**
```bash
# WRONG (fails with "directory prefix . does not contain modules")
cd dreadnought-private-server && go test ./...

# CORRECT
cd dreadnought-private-server/mmogbrain && go test ./...
cd dreadnought-private-server/legacy-api && go test ./...
cd dreadnought-private-server/shared && go test ./...
```

**`shared/dreadgameconfig` and `legacy-api/handlers` tests require `DATA_DIR` set explicitly:**
```bash
# WRONG — panics: shared/dreadgameconfig's package-level init() eagerly loads
# game data via a cwd-relative default path ("../data/"), which only resolves
# correctly when cwd is a module root that sits directly under the repo root
# (e.g. mmogbrain/). legacy-api/handlers/ and shared/dreadgameconfig/ are one
# level deeper, so the relative default resolves to a nonexistent directory —
# and because the load happens in init() (before any test or TestMain can run),
# there is no way to fix this from within the test binary; DATA_DIR must be set
# in the environment before `go test` starts.
cd shared && go test ./...
cd legacy-api && go test ./...

# CORRECT
DATA_DIR=/root/projects/dreadnought-private-server/data bash -c 'cd shared && go test ./...'
DATA_DIR=/root/projects/dreadnought-private-server/data bash -c 'cd legacy-api && go test ./...'
```

**Current test status (2026-07-18):**
- `mmogbrain`: all tests pass (7 test files — payload sizes, ribbons, seasons, gateway bootstrap, fleet dumps, quickcheck, main)
- `legacy-api/handlers`: 11 tests pass (with `DATA_DIR` set — see gotcha above)
- `shared/dreadgameconfig`: 31 test files pass (with `DATA_DIR` set — see gotcha above)
- `gateway`: 2 tests pass (crash receiver)
- All other modules: no test files

**Lint from workspace root reports 0 issues but shows typecheck error:**
```bash
# Run per-module for real results
cd dreadnought-private-server/mmogbrain && golangci-lint run ./...
```

**Service launcher:** `run/start.sh` sources `run/secrets.env` and starts all services with correct env vars.

**Go version mismatch:** `go.work` declares `go 1.25.0` (installed), but `Dockerfile.service` uses `golang:1.24-alpine`.

## Architecture

```
[Windows Client] → HTTPS :443 → gateway (TLS termination + Host-header routing)
  ├─ profile-api.*       → auth-server :8081   (JWT, login)
  ├─ legacyapi.*         → legacy-api  :8082   (profiles, inventory)
  ├─ mmog.*              → mmogbrain   :8083   (matchmaking)
  └─ masterserver.*      → master-server :8084 (server registry)

[Client] → TLS :48843 → mmogbrain (Firmament JSON-RPC + YMmogbrain binary protocol)
[Client] → UDP :7777-7877 → DreadGame-Win64-Shipping.exe (dedicated servers via Wine)
```

## Build & Run

```bash
# Build all services
bash scripts/setup.sh

# Build single service
cd auth-server && go build -o ../run/auth-server .

# Start all services (sources run/secrets.env)
bash run/start.sh

# Health checks
curl http://127.0.0.1:8081/health  # auth-server
curl http://127.0.0.1:8082/health  # legacy-api
curl http://127.0.0.1:8083/health  # mmogbrain
curl http://127.0.0.1:8084/health  # master-server
curl http://127.0.0.1:8085/health  # game-manager
```

## Key Files

- `go.work` — Workspace file (9 modules, all under
  `github.com/darkace1998/Dreadnought-Revival-project/<service>`)
- `scripts/setup.sh` — Build + certs + secrets, one shot
- `scripts/start-services.sh` / `scripts/stop-services.sh` — Service launcher and stopper
- `scripts/secrets.env.example` — Annotated configuration template
- `run/secrets.env` — Runtime secrets (gitignored)
- `docs/client-data-reference.md` — Extracted item/ship id maps and naming rules
- `docs/client-data-validation.md` — Audit of every id the server emits
- `.github/copilot-instructions.md` — Architectural context

(`progress.md`, `issues.md`, `docs/PROTOCOL.md` and `docs/API.md` were listed here
for a long time but have never existed in this repository.)

## Conventions

- **Handler pattern:** `type Handler struct { DB *sql.DB; Log *logrus.Logger; ... }` wired in `main.go` with `gorilla/mux`
- **JWT:** HS256, claims: `sub`, `username`, `realm="dreadnought.pc-us"`, `aud="dreadnought"|"launcher"`
- **Auth middleware:** Injects `X-User-ID`/`X-Username` headers; admin endpoints use `X-Admin-Key`
- **Database:** SQLite via `shared/db` (WAL mode, `MaxOpenConns=1`, sequential migrations)
- **Logging:** `logrus` with `JSONFormatter`; `Info` for requests, `Warn` for admin events
- **Metrics:** All services expose `GET /metrics` via `promhttp`
- **Env vars:** All services use `getenv(key, fallback)` — no `.env` parsing

## mmogbrain (most complex service)

Beyond HTTP REST, runs Firmament TLS server on `:48843` speaking proprietary binary protocol:
- Binary encoding helpers: `appendMmogStringField`, `appendMmogInt32Field`, etc.
- Player PIDs: UUID hex strings **with hyphens stripped** (32 chars, not 36)
- Race condition: Firmament auth delayed until MMOG `YA_PlayerGet` confirms player data ready
- `PLAYERS_PER_MATCH=2` for testing, `10` for production

## TLS Certificates

| File | Used by | Notes |
|---|---|---|
| `certs/server.crt` | Gateway HTTPS | Self-signed; clients trust `certs/ca.crt` |
| `certs/firmament.crt` | Firmament :48843 | Issuer spoofed as `Amazon RSA 2048 M01` to bypass game's CA pinning |

## Current State (2026-07-18)

- All 8 services build; 0 new golangci-lint issues per-module (6 pre-existing unrelated issues remain in shared/dreadgameconfig, see todo.md)
- All tests pass across 4 modules (mmogbrain, legacy-api, shared, gateway)
- 24/24 CRITICAL+HIGH issues resolved; 22/32 MEDIUM resolved (see todo.md Phase 29); 15 LOW resolved
- mmogbrain refactored: 4,720-line `main.go` → 218-line entry point + 11 files
- ~114 YA_* handlers dispatched; ~45 with dedicated payload builders
- Client can log in, enter hangar, modify fleets/loadouts, queue for matches, earn XP/ranks
- Phases 1-6 complete: hangar, matchmaking, progression, market/economy, PvE/AI
- Feature coverage: ~30%
