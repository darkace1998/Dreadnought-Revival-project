# Copilot Instructions — Dreadnought Private Server

A Go workspace of seven microservices that emulate the Greybox/Yager backend for the discontinued game Dreadnought (UE4, Steam App 835860), allowing unmodified clients to connect via a hosts-file redirect.

## Build & Run

```bash
# Build all services (also generates TLS certs if missing)
bash scripts/setup.sh

# Build a single service
cd auth-server && go build -o ../run/auth-server .

# Run all tests (only mmogbrain currently has tests)
cd mmogbrain && go test ./...

# Run a single test
cd mmogbrain && go test -run TestExtractMmogPlayerPIDFromLoginTicket ./...

# Health-check all running services
curl http://127.0.0.1:8081/health   # auth-server
curl http://127.0.0.1:8082/health   # legacy-api
curl http://127.0.0.1:8083/health   # mmogbrain
curl http://127.0.0.1:8084/health   # master-server
curl http://127.0.0.1:8085/health   # game-manager
```

## Workspace Layout

This is a **Go workspace** (`go.work`). Each service is an independent Go module under `github.com/dreadnought-ps/<service>`. The `shared/` module (`github.com/dreadnought-ps/shared`) provides common packages consumed by all services.

```
auth-server/    :8081  Login, JWT issuance, bans (SQLite: auth.db)
legacy-api/     :8082  Profiles, inventory, match history (SQLite: legacy.db)
mmogbrain/      :8083  Matchmaking queue + Firmament TLS :48843 (SQLite: mmog.db)
master-server/  :8084  Server registry + heartbeat (SQLite: master.db)
game-manager/   :8085  Wine+DreadGame.exe spawner, port pool 7777–7877 (in-memory)
gateway/        :443   TLS termination + reverse proxy by Host header
admin-cli/            CLI that calls service REST APIs via X-Admin-Key
shared/               db, logging, middleware packages
```

## Architecture: Request Flow

```
[Unmodified Windows client]
  → HTTPS → Gateway :443   (routes by Host header)
    ├─ profile-api.*       → auth-server :8081
    ├─ legacyapi.*         → legacy-api  :8082
    ├─ mmog.*              → mmogbrain   :8083  (HTTP REST)
    └─ masterserver.*      → master-server :8084
  → TLS → Firmament :48843 → mmogbrain (binary YMmogbrain protocol over TCP)
  → UDP  → DreadGame.exe   :7777–7877   (UE4 direct game traffic)
```

After a match is found, mmogbrain calls game-manager `POST /instances`, which spawns `wine DreadGame-Win64-Shipping.exe -dedicatedserver` and registers it with master-server.

## Key Conventions

### Handler structure
Every service follows the same pattern: a `handlers.Handler` struct holding `*sql.DB`, `*logrus.Logger`, and service-specific fields. Handlers are wired in `main.go` using `gorilla/mux`.

```go
type Handler struct {
    DB     *sql.DB
    Log    *logrus.Logger
    Secret []byte  // service-specific extras
}
```

### Shared helpers
- `writeJSON(w, status, v)` — sets Content-Type and encodes JSON
- `writeGreyboxError(w, status, code, msg)` — **Greybox-compatible error format**; `code` must be `<= -32000` for the launcher to show a real error message (not "Greybox service unavailable")

### Database
Use `shared/db`:
```go
db, err := db.Open(path)       // WAL mode, foreign keys on, busy_timeout=5s, MaxOpenConns=1
db.Migrate(db, []string{ddl})  // sequential migrations tracked in schema_versions
```
SQLite is single-writer (`MaxOpenConns(1)`). All queries use parameterized statements.

### JWT
- Algorithm: **HS256** (HMAC-SHA256)
- Claims: `sub`=user UUID, `username`, `realm`=`"dreadnought.pc-us"`, `aud`=`"dreadnought"` (game) or `"launcher"` (launcher Steam auth)
- Auth validated by middleware → sets `X-User-ID` and `X-Username` headers for downstream handlers
- The launcher sends auth as **JSON-RPC 2.0** (`method: "jwt.get.by_steam_ticket"`); responses must be wrapped in `{"jsonrpc":"2.0","id":<id>,"result":[true, userObject]}`

### Auth middleware pattern
Admin endpoints use `X-Admin-Key` header checked by `adminKeyMiddleware`. Authenticated user endpoints use `jwtMiddleware` which injects `X-User-ID`/`X-Username`.

### Environment variables
All services use `getenv(key, fallback)` — no `.env` file parsing. All defaults are intentionally insecure; always set `JWT_SECRET` and `ADMIN_KEY` in production.

### Logging
All services use `logrus` with `JSONFormatter`. Log at `Info` for normal requests, `Warn` for admin/ban events, `Error`/`Fatal` for unexpected failures. The `loggingMiddleware` wraps `http.ResponseWriter` to capture status codes.

### Prometheus metrics
Every service exposes `GET /metrics` via `promhttp.Handler()` — no custom collectors needed unless adding new metrics.

## mmogbrain: Binary Protocol (YMmogbrain)

mmogbrain is the most complex service. Beyond HTTP REST endpoints, it runs a **Firmament TLS server** on `:48843` that speaks a proprietary binary protocol used by the game's `OnlineSubsystemMmogbrain` UE4 plugin.

- Binary encoding helpers: `appendMmogStringField`, `appendMmogInt32Field`, `appendMmogFieldNameAndType`, etc. — use these instead of raw byte manipulation
- Player PIDs in the binary protocol are **UUID hex strings with hyphens stripped** (32 hex chars, not 36-char UUID format)
- Race condition: Firmament auth success must be **delayed until MMOG `YA_PlayerGet` confirms player data is ready** — coordinated via `firmamentPendingAuth` and `mmogPlayerDataReady` maps with a 30-second timeout
- `PLAYERS_PER_MATCH=2` for testing, `10` for production

## TLS Certificates

Two certificate identities are in play:

| File | Used by | Notes |
|---|---|---|
| `certs/server.crt` | Gateway HTTPS | Self-signed by our CA; clients must trust `certs/ca.crt` |
| `certs/firmament.crt` | Firmament :48843 | Issuer spoofed as `Amazon RSA 2048 M01` + matching AKID to bypass the game's hardcoded Amazon CA pinning; peer verification is disabled in-game so the signature doesn't matter |

## game-manager: Mock Mode

Set `WINE_EXE=none` (or omit the game binary) to run without Wine. game-manager will record a mock instance — useful for testing the matchmaking flow end-to-end without a real game binary.

## docs/ Reference

- `docs/PROTOCOL.md` — UE4 wire protocol (17-byte packet header), YMmogbrain endpoint table, JWT format, known unknowns
- `docs/ARCHITECTURE.md` — full data-flow diagram (Login → Match → Stats), security model, port reference
- `docs/API.md` — REST API documentation for all services
