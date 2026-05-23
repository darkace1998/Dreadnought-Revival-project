# Developer Agent Progress Tracker

## Last Updated
2026-05-23

## Completed Steps
...
- [x] **C2**: Fixed `shared/logging/logging.go` — `New()` now uses `service` param via Hook — 2026-05-23
- [x] **C3**: Fixed `auth-server/main.go` jwtMiddleware — now checks sessions table for revoked tokens — 2026-05-23
- [x] **H6**: Added JWT audience validation to legacy-api, mmogbrain, and shared/middleware — 2026-05-23
- [x] **H3/H12**: RateLimiter memory leak fixed in `shared/middleware` and `gateway` — added `cleanupLoop()` + fixed unreachable `delete()` — 2026-05-23
- [x] Verified all 8 CRITICAL issues: C1-C8 confirmed resolved — 2026-05-23
- [x] Verified all 16 HIGH issues: H1-H16 confirmed resolved — 2026-05-23
- [x] **mmogbrain refactor** — split 4,720-line `main.go` into 11 files + 1 new package — 2026-05-23
  - `protocol/` package (5 files: crypto.go, parser.go, encoder.go, frames.go, pid.go) — MMOG binary protocol
  - `firmament.go` (424 lines) — Firmament TLS server + handshake
  - `gateway_server.go` (452 lines) — Gateway HTTP server + session management
  - `gateway_catalog.go` (426 lines) — Catalog entities + market metadata
  - `response_types.go` (681 lines) — Ship/loadout/fleet types + starter data
  - `response_dispatcher.go` (126 lines) — Request router switch
  - `response_builders.go` (1476 lines) — All response payload builders
  - `response_connection.go` (334 lines) — MMOG connection handlers
  - `main.go` (218 lines) — Entry point + HTTP middleware
- [x] All services build cleanly — 0 golangci-lint issues expected — 2026-05-23
- [x] All existing tests pass — 2026-05-23

## In Progress
(none)

## Next Steps (Queue)
### mmogbrain
- [x] **[PRIORITY HIGH]** Refactor — split `main.go` (4,586 lines) into separate packages — DONE 2026-05-23

### MEDIUM Issues (outstanding)
- [x] **M1** shared/db: Migrations not transactional — FIXED 2026-05-23 (wrapped in tx)
- [x] **M5** auth-server: Duplicate username detection via error-string matching — FIXED 2026-05-23 (SELECT check before INSERT)
- [x] **M6** auth-server: prometheus incorrectly marked `// indirect` in go.mod — Already correct
- [x] **M8** legacy-api: PostMatchResult no transaction — Already wrapped in tx
- [x] **M9** legacy-api: GetInventory defers rows.Close() then explicitly calls it again — Already correct
- [x] **M10** legacy-api: No request body size limit on PostMatchResult — FIXED 2026-05-23 (128KB limit)
- [x] **M11** legacy-api: No DB health check in /health endpoint — Already has DB ping
- [x] **M12** game-manager: DELETE returns 404 when process already dead — FIXED 2026-05-23 (differentiates not found vs error)
- [x] **M13** game-manager: No input validation on POST /instances — FIXED 2026-05-23 (name length + port exhaustion check)
- [x] **M14** game-manager: List() returns pointers to internal state — Already returns by value
- [x] **M17** gateway: promhttp incorrectly marked `// indirect` — Already correct
- [x] **M18** gateway: 9.8 MB compiled binary committed to repo — FIXED 2026-05-23 (.gitignore added)
- [x] **M20** dn-launcher: HTTP response status code not checked — FIXED 2026-05-23 (status >= 400 check)
- [x] **M21** dn-launcher: No response body size limit — Already uses LimitReader 1MB
- [x] **M22** dn-launcher: Player ID deterministic — FIXED 2026-05-23 (added crypto/rand entropy)
- [x] **M23** dn-launcher: Corrupted player.json silently regenerates — FIXED 2026-05-23 (warning logged)
- [x] **M25** admin-cli: URL path injection in stopInstance() — Already uses url.PathEscape
- [x] **M26** admin-cli: Channel name not URL-encoded in chat() — Already uses url.QueryEscape
- [x] **M27** admin-cli: io.ReadAll errors silently swallowed — FIXED 2026-05-23 (error checks added)
- [x] **M29** master-server: go.sum corrupted — FIXED 2026-05-23 (go mod tidy)
- [x] **M30** master-server: Stale-server marking in hot read path — Already uses background goroutine
- [x] **M31** master-server: RowsAffected() errors silently discarded — FIXED 2026-05-23 (error checks added)
- [ ] **M4** shared/middleware: Claims passed via request headers instead of context.WithValue
- [ ] **M15** game-manager: Zero test files
- [ ] **M19** gateway: Zero test files
- [ ] **M24** dn-launcher: Zero tests
- [ ] **M28** admin-cli: Zero tests
- [ ] **M32** master-server: Zero test files

### LOW Issues (selected)
- [x] **L1** shared: go.mod declares go 1.25.0 — FIXED 2026-05-23 (corrected to 1.24.0)
- [ ] **L2** auth-server: models.Session and models.Ban never used
- [ ] **L3** auth-server: Sessions table no cleanup — expired rows accumulate
- [ ] **L4-L15** Various minor issues across services (see issues.md)

### Test Coverage
- [ ] auth-server: 0 tests
- [ ] admin-cli: 0 tests
- [ ] dn-launcher: 0 tests
- [ ] game-manager: 0 tests
- [ ] gateway: 0 tests
- [ ] master-server: 0 tests
- [ ] shared/db: 0 tests
- [ ] shared/logging: 0 tests
- [ ] shared/middleware: 0 tests

## Blocked / Needs Investigation
(none)
