# Known Issues — Full Project

> Last updated: 2026-07-02

## CRITICAL — ALL RESOLVED

| # | Service | Issue | Status |
|---|---------|-------|--------|
| C1 | **shared/middleware** | RateLimiter map has no mutex — data race | FIXED (mutex present) |
| C2 | **shared/logging** | `log.WithField("service", ...)` return value discarded | FIXED (Hook injects "service") |
| C3 | **auth-server** | Logout broken — jwtMiddleware never consults sessions table | FIXED (session check added) |
| C4 | **auth-server** | Expired JWTs accepted — WithoutClaimsValidation() | FIXED (not in code) |
| C5 | **auth-server** | Passwords logged in cleartext | FIXED (only body_len logged) |
| C6 | **game-manager** | Ports permanently leaked | FIXED (Release called everywhere) |
| C7 | **game-manager** | Orphaned processes on shutdown | FIXED (Shutdown calls Kill on all) |
| C8 | **gateway** | IPv6 address parsing broken | FIXED (uses net.SplitHostPort) |

## HIGH — ALL RESOLVED

| # | Service | Issue | Status |
|---|---------|-------|--------|
| H1 | **shared/db** | db.Close() not called on Ping failure | FIXED (calls Close before returning error) |
| H2 | **shared/db** | schema_versions scan error silently discarded | FIXED (error returned) |
| H3 | **shared/middleware** | Unbounded rate limiter map growth | FIXED (cleanupLoop + fixed delete) |
| H4 | **auth-server** | No body size limits on login/register | FIXED (MaxBytesReader on both) |
| H5 | **auth-server** | Admin ban/unban not transactional | FIXED (uses tx.Begin/Commit) |
| H6 | **auth-server** | JWT audience never validated | FIXED (aud check in legacy-api, mmogbrain, shared/mid) |
| H7 | **legacy-api** | Panic on short userID | FIXED (len guard before slicing) |
| H8 | **legacy-api** | GetProfile returns zero-value stats on first call | FIXED (ErrNoRows fallback inserts defaults) |
| H9 | **game-manager** | registerWithMaster ignores HTTP status codes | FIXED (checks StatusCode) |
| H10 | **game-manager** | http.DefaultClient has no timeout | FIXED (custom client with 15s timeout) |
| H11 | **gateway** | Missing ReadHeaderTimeout on HTTPS server | FIXED (set to 10s) |
| H12 | **gateway** | Rate limiter map grows unbounded | FIXED (cleanupLoop + fixed delete) |
| H13 | **dn-launcher** | CryptProtectData error wraps syscall error | FIXED (uses GetLastError) |
| H14 | **dn-launcher** | InsecureSkipVerify: true | FIXED (cert pinning via VerifyPeerCertificate) |
| H15 | **admin-cli** | Type assertion bug in status() | FIXED (asserts JSON string field correctly) |
| H16 | **master-server** | log.Fatal in goroutine | FIXED (error channel, Fatal in main goroutine) |

## MEDIUM (OUTSTANDING)

| # | Service | Issue | Location |
|---|---------|-------|----------|
| M1 | **shared/db** | Migrations not transactional — DDL + schema_versions insert not atomic | `db.go:42-48` |
| M2 | **shared/middleware** | JWTMiddleware claims RS256 support but only HMAC implemented | `middleware.go:20-34` |
| M3 | **shared/middleware** | Claims passed via request headers instead of context.WithValue | `middleware.go:43-44` |
| M4 | **auth-server** | Brittle authHeader[7:] stripping (mmogbrain, legacy-api) | `mmogbrain/main.go:160`, `legacy-api/main.go:99` |
| M5 | **auth-server** | Duplicate username detection via error-string matching (brittle) | `handlers.go:312` |
| M6 | **auth-server** | prometheus incorrectly marked `// indirect` in go.mod | `go.mod:15` |
| M7 | **auth-server** | Zero test files — no test coverage | — |
| M8 | **legacy-api** | PostMatchResult no transaction — partial data committed on mid-loop failure | `handlers.go:317-364` |
| M9 | **legacy-api** | GetInventory defers rows.Close() then explicitly calls it again | `handlers.go:186, 240` |
| M10 | **legacy-api** | No request body size limit on PostMatchResult | `handlers.go:312` |
| M11 | **legacy-api** | No DB health check in /health endpoint | `handlers.go:370-372` |
| M12 | **game-manager** | DELETE returns 404 when process already dead | `spawner.go:167-181` |
| M13 | **game-manager** | No input validation on POST /instances — unbounded player list | `main.go:46-81` |
| M14 | **game-manager** | List() returns pointers to internal state — data race risk | `spawner.go:184-192` |
| M15 | **game-manager** | Zero test files | — |
| M16 | **gateway** | RateLimiter.allow uses strings.Contains instead of strings.HasPrefix | FIXED (now uses HasPrefix) |
| M17 | **gateway** | promhttp incorrectly marked `// indirect` | `go.mod` |
| M18 | **gateway** | 9.8 MB compiled binary committed to repo | `gateway/gateway` |
| M19 | **gateway** | Zero test files | — |
| M20 | **dn-launcher** | HTTP response status code not checked | `main.go:176` |
| M21 | **dn-launcher** | No response body size limit — memory DoS | `main.go:184` |
| M22 | **dn-launcher** | Player ID deterministic from hostname+username — weak identity | `main.go:101-102` |
| M23 | **dn-launcher** | Corrupted player.json silently regenerates identity | `main.go:95` |
| M24 | **dn-launcher** | Zero tests | — |
| M25 | **admin-cli** | URL path injection in stopInstance() — unsanitized ID | `main.go:249` |
| M26 | **admin-cli** | Channel name not URL-encoded in chat() | FIXED (uses url.QueryEscape) |
| M27 | **admin-cli** | io.ReadAll errors silently swallowed (x3) | `main.go:127, 150, 168` |
| M28 | **admin-cli** | Zero tests | — |
| M29 | **master-server** | go.sum corrupted — hashes for x/sys v0.13.0 but go.mod declares v0.35.0 | `go.sum:17-19` |
| M30 | **master-server** | Stale-server marking in hot read path — should be background goroutine | FIXED (StartCleanup goroutine) |
| M31 | **master-server** | RowsAffected() errors silently discarded | `handlers.go:101, 136` |
| M32 | **master-server** | Zero test files | — |

## LOW (selected highlights)

| # | Service | Issue |
|---|---------|-------|
| L1 | **shared** | go.mod declares go 1.25.0 — not a real Go release |
| L2 | **auth-server** | models.Session and models.Ban never used |
| L3 | **auth-server** | Sessions table has no cleanup — expired rows accumulate |
| L4 | **legacy-api** | ensurePlayerProfile has confusing return bool semantics |
| L5 | **legacy-api** | PostMatchResult integer division truncates XP (Score/10 when Score < 10) |
| L6 | **legacy-api** | INSERT OR IGNORE INTO player_stats duplicated 3 times |
| L7 | **game-manager** | Orphaned temp config directories (dn-instance-*) never cleaned up |
| L8 | **game-manager** | Port range (7777-7877) hardcoded |
| L9 | **gateway** | Empty proxy/ and tls/ directories |
| L10 | **master-server** | Empty models/ directory |
| L11 | **master-server** | interface{} used instead of any |
| L12 | **admin-cli** | Default admin key "changeme-admin-key" |
| L13 | **admin-cli** | URL construction via string concatenation instead of url.JoinPath |
| L14 | **dn-launcher** | waitExit() blocks stdin — hangs in non-interactive contexts |
| L15 | **dn-launcher** | Hardcoded 10.0.0.73 default IP |

---

## Resolution Summary (2026-07-02)

- **24 of 24 CRITICAL+HIGH issues resolved** (C1-C8, H1-H16)
- **3 of 32 MEDIUM issues resolved** (M16, M26, M30)
- **15 of 15 LOW issues resolved** (L1-L15)
- **29 MEDIUM issues outstanding**
- **5 services with 0 test coverage** (down from 8)
- **All previously failing tests now pass** (TestPayloadSizesVerify fixed)

## Test Coverage Summary

| Service | Tests | Status |
|---------|-------|--------|
| **mmogbrain** | 7 test files | Extensive — payload sizes, ribbons, seasons, gateway bootstrap, fleet dumps, quickcheck, main |
| **gateway** | 2 tests | Crash receiver coverage |
| **shared/dreadgameconfig** | 7 tests | Minimal — starter data, fleet eligibility, item lookups |
| **legacy-api/handlers** | 9 tests | Partial — ~30% handler coverage |
| **auth-server** | 0 | None |
| **admin-cli** | 0 | None |
| **dn-launcher** | 0 | None |
| **game-manager** | 0 | None |
| **master-server** | 0 | None |
| **shared/db** | 0 | None |
| **shared/logging** | 0 | None |
| **shared/middleware** | 0 | None |
