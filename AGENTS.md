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

**Current test status (2026-07-02):**
- `mmogbrain`: all tests pass (7 test files — payload sizes, ribbons, seasons, gateway bootstrap, fleet dumps, quickcheck, main)
- `legacy-api/handlers`: 9 tests pass
- `shared/dreadgameconfig`: 7 tests pass
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

- `progress.md` — Task tracker (READ FIRST every session)
- `issues.md` — Known bugs and blockers
- `go.work` — Workspace file (8 modules)
- `run/start.sh` — Service launcher script
- `run/secrets.env` — Runtime secrets (gitignored)
- `docs/PROTOCOL.md` — UE4 wire protocol reference
- `docs/API.md` — REST API documentation
- `.github/copilot-instructions.md` — Architectural context

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

## Current State (2026-07-02)

- All 8 services build; 0 golangci-lint issues per-module
- All tests pass across 4 modules (mmogbrain, legacy-api, shared, gateway)
- 24/24 CRITICAL+HIGH issues resolved; 29 MEDIUM tracked; 15 LOW resolved
- mmogbrain refactored: 4,720-line `main.go` → 218-line entry point + 11 files
- ~114 YA_* handlers dispatched; ~45 with dedicated payload builders
- Client can log in, enter hangar, modify fleets/loadouts, queue for matches, earn XP/ranks
- Phases 1-6 complete: hangar, matchmaking, progression, market/economy, PvE/AI
- Feature coverage: ~30%
