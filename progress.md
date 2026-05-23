# Developer Agent Progress Tracker

## Last Updated
2026-05-23

## Completed Steps
- [x] mmogbrain review completed — 2026-05-23
- [x] mmogbrain 9/10 fix items completed (session expiry, graceful shutdown, tier matching, rate limiting, INFERRED markers, logging fix, crypto docs, models cleanup, build+test fixes) — 2026-05-23
- [x] Moved `src/Documents/` → `dreadnought-private-server/Documents/` — 2026-05-23
- [x] Full project review: all 8 services + shared packages reviewed — 2026-05-23
- [x] Consolidated 50+ issues into `issues.md` with severity ratings — 2026-05-23

## In Progress
(none)

## Next Steps (Queue)
### mmogbrain
- [ ] **[PRIORITY HIGH]** Refactor — split `main.go` (4,586 lines) into separate packages

### shared (shared/middleware, shared/logging, shared/db)
- [ ] **[CRITICAL]** Add `sync.Mutex` to RateLimiter — data race fix
- [ ] **[CRITICAL]** Fix `logging.New` — `WithField` return value discarded
- [ ] **[HIGH]** Fix `db.Open` — close on Ping failure
- [ ] **[HIGH]** Fix `db.Migrate` — handle schema_versions scan error
- [ ] **[MEDIUM]** Add tests for db, logging, middleware packages

### auth-server
- [ ] **[CRITICAL]** Fix logout — check sessions table in jwtMiddleware
- [ ] **[CRITICAL]** Fix JWT expiry — remove WithoutClaimsValidation or add explicit check
- [ ] **[CRITICAL]** Remove password logging from request body dump
- [ ] **[HIGH]** Add request body size limits
- [ ] **[HIGH]** Fix JWT audience validation
- [ ] **[MEDIUM]** Add tests

### legacy-api
- [ ] **[HIGH]** Fix panic on short userID (`userID[:8]`)
- [ ] **[HIGH]** Fix GetProfile zero-value stats on first call
- [ ] **[MEDIUM]** Wrap PostMatchResult in transaction
- [ ] **[MEDIUM]** Add tests for PostMatchResult, Tiles, AgeConsent

### game-manager
- [ ] **[CRITICAL]** Fix port pool exhaustion — Release ports on Stop/monitor
- [ ] **[CRITICAL]** Add graceful shutdown — kill spawned game servers on SIGTERM
- [ ] **[HIGH]** Add HTTP status code check in registerWithMaster
- [ ] **[HIGH]** Replace http.DefaultClient with custom timeout client
- [ ] **[MEDIUM]** Add tests

### gateway
- [ ] **[CRITICAL]** Fix IPv6 parsing in rate limiter
- [ ] **[HIGH]** Add ReadHeaderTimeout to HTTPS server
- [ ] **[MEDIUM]** Fix rate limiter memory leak (stale IP cleanup)
- [ ] **[MEDIUM]** Remove compiled binary from repo, add .gitignore
- [ ] **[MEDIUM]** Add tests

### master-server
- [ ] **[HIGH]** Fix log.Fatal in goroutine → use error channel
- [ ] **[HIGH]** Fix go.sum corruption (run go mod tidy)
- [ ] **[MEDIUM]** Move stale-server marking to background goroutine
- [ ] **[MEDIUM]** Add tests

### dn-launcher
- [ ] **[HIGH]** Fix CryptProtectData error wrapping (GetLastError not syscall error)
- [ ] **[HIGH]** Mitigate InsecureSkipVerify — add cert pinning
- [ ] **[MEDIUM]** Add HTTP status code check
- [ ] **[MEDIUM]** Add tests

### admin-cli
- [ ] **[HIGH]** Fix status() type assertion bug
- [ ] **[MEDIUM]** URL-encode channel name in chat()
- [ ] **[MEDIUM]** Sanitize instance ID in stopInstance()
- [ ] **[MEDIUM]** Add tests

## Blocked / Needs Investigation
(none)
