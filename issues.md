# Known Issues — Full Project

> Last updated: 2026-05-23

## CRITICAL

| # | Service | Issue | Location |
|---|---------|-------|----------|
| C1 | **shared/middleware** | RateLimiter map has no mutex — **guaranteed data race** under concurrent requests | `middleware.go:51-88` |
| C2 | **shared/logging** | `log.WithField("service", ...)` return value discarded — **service field never appears in logs** | `logging.go:16` |
| C3 | **auth-server** | Logout broken — `jwtMiddleware` never consults `sessions` table; revoked tokens still work | `main.go:107-123` |
| C4 | **auth-server** | Expired JWTs accepted — `WithoutClaimsValidation()` disables `exp` check | `jwt.go:52-53` |
| C5 | **auth-server** | Passwords logged in cleartext — raw request body dumped to log | `handlers.go:60-66` |
| C6 | **game-manager** | Ports permanently leaked — `pool.Release()` never called after launch | `spawner.go:138-164` |
| C7 | **game-manager** | Orphaned processes on shutdown — no kill loop for spawned game servers | `main.go` |
| C8 | **gateway** | IPv6 address parsing broken — `strings.LastIndex(ip, ":")` corrupts IPv6 | `main.go:116-118` |

## HIGH

| # | Service | Issue | Location |
|---|---------|-------|----------|
| H1 | **shared/db** | `db.Close()` not called on Ping failure — resource leak | `db.go:19-21` |
| H2 | **shared/db** | `schema_versions` scan error silently discarded — all migrations re-run on failure | `db.go:36` |
| H3 | **shared/middleware** | Unbounded rate limiter map growth — memory leak over time | `middleware.go:52` |
| H4 | **auth-server** | No body size limits on login/register — OOM DoS risk | `handlers.go:60, 282` |
| H5 | **auth-server** | Admin ban/unban not transactional — inconsistent DB state on partial failure | `handlers.go:370-413` |
| H6 | **auth-server** | JWT audience (`aud`) never validated — launcher tokens usable on game endpoints | `main.go:107-123` |
| H7 | **legacy-api** | **Panic on short userID** — `userID[:8]` causes index-out-of-range crash | `handlers.go:42` |
| H8 | **legacy-api** | `GetProfile` returns zero-value stats on first call (reads before default insert) | `handlers.go:135-149` |
| H9 | **game-manager** | `registerWithMaster` ignores HTTP status codes — silent failure | `spawner.go:207-221` |
| H10 | **game-manager** | `http.DefaultClient` has no timeout — hangs forever on unreachable master | `spawner.go:152-158` |
| H11 | **gateway** | Missing `ReadHeaderTimeout` on HTTPS server — slowloris vulnerable | `main.go:205-214` |
| H12 | **gateway** | Rate limiter map grows unbounded — memory leak | `main.go:39-69` |
| H13 | **dn-launcher** | `CryptProtectData` error wraps syscall error, not `GetLastError()` | `main.go:62-63` |
| H14 | **dn-launcher** | `InsecureSkipVerify: true` — JWT sent over MITM-vulnerable connection | `main.go:173` |
| H15 | **admin-cli** | Type assertion bug in `status()` — HTTP status (int) asserted as string, always fails | `main.go:187` |
| H16 | **master-server** | `log.Fatal` in goroutine kills process, bypasses all defers | `main.go:58` |

## MEDIUM

| # | Service | Issue | Location |
|---|---------|-------|----------|
| M1 | **shared/db** | Migrations not transactional — DDL + schema_versions insert not atomic | `db.go:42-48` |
| M2 | **shared/middleware** | JWTMiddleware claims RS256 support but only HMAC implemented | `middleware.go:20-34` |
| M3 | **shared/middleware** | Claims passed via request headers instead of `context.WithValue` | `middleware.go:43-44` |
| M4 | **auth-server** | Brittle `authHeader[7:]` stripping — doesn't verify "Bearer " prefix | `main.go:109-114` |
| M5 | **auth-server** | Duplicate username detection via error-string matching (brittle) | `handlers.go:303` |
| M6 | **auth-server** | prometheus incorrectly marked `// indirect` in go.mod | `go.mod:15` |
| M7 | **auth-server** | Zero test files — no test coverage | — |
| M8 | **legacy-api** | `PostMatchResult` no transaction — partial data committed on mid-loop failure | `handlers.go:317-364` |
| M9 | **legacy-api** | `GetInventory` defers `rows.Close()` then explicitly calls it again | `handlers.go:186, 240` |
| M10 | **legacy-api** | No request body size limit on `PostMatchResult` | `handlers.go:312` |
| M11 | **legacy-api** | No DB health check in `/health` endpoint | `handlers.go:370-372` |
| M12 | **game-manager** | DELETE returns 404 when process already dead (should be 409 or 200) | `spawner.go:167-181` |
| M13 | **game-manager** | No input validation on POST /instances — unbounded player list | `main.go:46-81` |
| M14 | **game-manager** | `List()` returns pointers to internal state — data race risk | `spawner.go:184-192` |
| M15 | **game-manager** | Zero test files | — |
| M16 | **gateway** | `RateLimiter.allow` uses `strings.Contains` instead of `strings.HasPrefix` | `main.go:114` |
| M17 | **gateway** | `promhttp` incorrectly marked `// indirect` | `go.mod` |
| M18 | **gateway** | 9.8 MB compiled binary committed to repo | `gateway/gateway` |
| M19 | **gateway** | Zero test files | — |
| M20 | **dn-launcher** | HTTP response status code not checked | `main.go:176` |
| M21 | **dn-launcher** | No response body size limit — memory DoS | `main.go:184` |
| M22 | **dn-launcher** | Player ID deterministic from hostname+username — weak identity | `main.go:101-102` |
| M23 | **dn-launcher** | Corrupted `player.json` silently regenerates identity | `main.go:95` |
| M24 | **dn-launcher** | Zero tests | — |
| M25 | **admin-cli** | URL path injection in `stopInstance()` — unsanitized ID | `main.go:249` |
| M26 | **admin-cli** | Channel name not URL-encoded in `chat()` | `main.go:274` |
| M27 | **admin-cli** | `io.ReadAll` errors silently swallowed (×3) | `main.go:127, 150, 168` |
| M28 | **admin-cli** | Zero tests | — |
| M29 | **master-server** | `go.sum` corrupted — hashes for `x/sys v0.13.0` but `go.mod` declares `v0.35.0` | `go.sum:17-19` |
| M30 | **master-server** | Stale-server marking in hot read path (GET /servers) — should be background goroutine | `handlers.go:145-182` |
| M31 | **master-server** | `RowsAffected()` errors silently discarded | `handlers.go:101, 136` |
| M32 | **master-server** | Zero test files | — |

## LOW (selected highlights)

| # | Service | Issue |
|---|---------|-------|
| L1 | **shared** | `go.mod` declares `go 1.25.0` — not a real Go release |
| L2 | **auth-server** | `models.Session` and `models.Ban` never used |
| L3 | **auth-server** | Sessions table has no cleanup — expired rows accumulate |
| L4 | **legacy-api** | `ensurePlayerProfile` has confusing return bool semantics |
| L5 | **legacy-api** | `PostMatchResult` integer division truncates XP (Score/10 when Score < 10) |
| L6 | **legacy-api** | `INSERT OR IGNORE INTO player_stats` duplicated 3 times |
| L7 | **game-manager** | Orphaned temp config directories (`dn-instance-*`) never cleaned up |
| L8 | **game-manager** | Port range (7777-7877) hardcoded |
| L9 | **gateway** | Empty `proxy/` and `tls/` directories |
| L10 | **master-server** | Empty `models/` directory |
| L11 | **master-server** | `interface{}` used instead of `any` |
| L12 | **admin-cli** | Default admin key `"changeme-admin-key"` |
| L13 | **admin-cli** | URL construction via string concatenation instead of `url.JoinPath` |
| L14 | **dn-launcher** | `waitExit()` blocks stdin — hangs in non-interactive contexts |
| L15 | **dn-launcher** | Hardcoded `10.0.0.73` default IP |

---

## Test Coverage Summary

| Service | Tests | Status |
|---------|-------|--------|
| **mmogbrain** | 3,200+ lines | Extensive |
| **shared/dreadgameconfig** | 179 lines (7 tests) | Minimal |
| **legacy-api** | 682 lines (9 tests) | Partial — ~30% handler coverage |
| **auth-server** | 0 | None |
| **admin-cli** | 0 | None |
| **dn-launcher** | 0 | None |
| **game-manager** | 0 | None |
| **gateway** | 0 | None |
| **master-server** | 0 | None |
| **shared/db** | 0 | None |
| **shared/logging** | 0 | None |
| **shared/middleware** | 0 | None |
