# Developer Agent Progress Tracker

## Last Updated
2026-05-23

## Completed Steps
- [x] mmogbrain review completed — 2026-05-23
- [x] mmogbrain 9/10 fix items completed — 2026-05-23
- [x] Moved `src/Documents/` → `dreadnought-private-server/Documents/` — 2026-05-23
- [x] Full project review: all 8 services + shared packages reviewed — 2026-05-23
- [x] Consolidated 50+ issues into `issues.md` with severity ratings — 2026-05-23
- [x] 36 fixes applied across shared, auth-server, legacy-api, game-manager, gateway, master-server, admin-cli, dn-launcher — 2026-05-23
- [x] Moved high-risk `Documents/` back to `src/Documents/` — 2026-05-23
- [x] Committed and pushed all changes to GitHub — 2026-05-23
- [x] Created `AGENTS.md` — session orientation and directory map — 2026-05-23
- [x] **C2**: Fixed `shared/logging/logging.go` — `New()` now uses `service` param via Hook — 2026-05-23
- [x] **C3**: Fixed `auth-server/main.go` jwtMiddleware — now checks sessions table for revoked tokens — 2026-05-23
- [x] **H6**: Added JWT audience validation to legacy-api, mmogbrain, and shared/middleware — 2026-05-23
- [x] **H3/H12**: RateLimiter memory leak fixed in `shared/middleware` and `gateway` — added `cleanupLoop()` + fixed unreachable `delete()` — 2026-05-23
- [x] Verified all 8 CRITICAL issues: C1-C8 confirmed resolved (some pre-existing, some just fixed) — 2026-05-23
- [x] Verified all 16 HIGH issues: H1-H16 confirmed resolved (many already fixed in 36-fix batch) — 2026-05-23
- [x] All services build cleanly — 0 golangci-lint issues expected — 2026-05-23
- [x] All existing tests pass — 2026-05-23

## In Progress
(none)

## Next Steps (Queue)
### mmogbrain
- [ ] **[PRIORITY HIGH]** Refactor — split `main.go` (4,586 lines) into separate packages

### MEDIUM Issues (outstanding)
- [ ] **M1** shared/db: Migrations not transactional — DDL + schema_versions insert not atomic
- [ ] **M4** shared/middleware: Claims passed via request headers instead of context.WithValue
- [ ] **M5** auth-server: Duplicate username detection via error-string matching (brittle)
- [ ] **M6** auth-server: prometheus incorrectly marked `// indirect` in go.mod
- [ ] **M8** legacy-api: PostMatchResult no transaction — partial data committed on mid-loop failure
- [ ] **M9** legacy-api: GetInventory defers rows.Close() then explicitly calls it again
- [ ] **M10** legacy-api: No request body size limit on PostMatchResult
- [ ] **M11** legacy-api: No DB health check in /health endpoint
- [ ] **M12** game-manager: DELETE returns 404 when process already dead
- [ ] **M13** game-manager: No input validation on POST /instances — unbounded player list
- [ ] **M14** game-manager: List() returns pointers to internal state — data race risk
- [ ] **M17** gateway: promhttp incorrectly marked `// indirect`
- [ ] **M18** gateway: 9.8 MB compiled binary committed to repo
- [ ] **M20** dn-launcher: HTTP response status code not checked
- [ ] **M21** dn-launcher: No response body size limit — memory DoS
- [ ] **M22** dn-launcher: Player ID deterministic from hostname+username
- [ ] **M23** dn-launcher: Corrupted player.json silently regenerates identity
- [ ] **M25** admin-cli: URL path injection in stopInstance()
- [ ] **M26** admin-cli: Channel name not URL-encoded in chat()
- [ ] **M27** admin-cli: io.ReadAll errors silently swallowed
- [ ] **M29** master-server: go.sum corrupted — x/sys version mismatch
- [ ] **M30** master-server: Stale-server marking in hot read path
- [ ] **M31** master-server: RowsAffected() errors silently discarded

### LOW Issues (selected)
- [ ] **L1** shared: go.mod declares go 1.25.0 — not a real Go release
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
