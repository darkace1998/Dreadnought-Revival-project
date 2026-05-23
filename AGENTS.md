# AGENTS.md — Dreadnought Private Server Project

## Overview

This project is a community-operated private server infrastructure for the discontinued game **Dreadnought** (UE4, Steam App 835860, Yager/Grey Box). The goal is to restore full playability via a Go-based backend and minimal client patches.

## Directory Map

```
/root/projects/
├── dreadnought-private-server/   ← MAIN WORKING DIRECTORY (git repo)
│   ├── auth-server/              :8081   Auth, login, JWT (Go + SQLite)
│   ├── legacy-api/               :8082   Profiles, inventory, match history
│   ├── mmogbrain/                :8083   Matchmaking + Firmament TLS :48843
│   ├── master-server/            :8084   Server registry + heartbeat
│   ├── game-manager/             :8085   Wine game server spawner
│   ├── gateway/                  :80,443 TLS termination + reverse proxy
│   ├── admin-cli/                       CLI management tool
│   ├── dn-launcher/                     Custom client launcher (Windows only)
│   ├── shared/                          Common Go packages (db, middleware, logging, config)
│   ├── scripts/                         Setup, cert generation, docker-compose
│   ├── certs/                           TLS certificates (CA, server, firmament)
│   ├── docs/                            API.md, PROTOCOL.md, ARCHITECTURE.md
│   ├── run/                             Compiled binaries + runtime DBs
│   ├── go.work                          Go workspace file (8 modules)
│   ├── progress.md                      Task tracker (READ FIRST every session)
│   ├── issues.md                        Known bugs and blockers
│   ├── README.md                        Project overview + quick start
│   ├── .github/copilot-instructions.md  Copilot AI instructions
│   └── .env.example                     Environment variable template
│
├── src/                           ← GAME FILES (not in git, READ-ONLY)
│   ├── Dreadnought/               Original game client + launcher files
│   │   ├── DreadnoughtLauncher.exe
│   │   └── launcher_extracted/    Extracted AngularJS launcher web app
│   └── Documents/                 Reverse-engineering knowledge base
│       ├── ghidra_decompile/      Ghidra decompilation of game binary
│       ├── ghidra_output/         Binary RE findings
│       ├── networking/            Wire protocol, packet layouts, RPCs
│       ├── ships/                 Ship stats, classes, hardpoints
│       ├── weapons/               Weapon system documentation
│       ├── abilities/             103+ abilities documented
│       ├── game_modes/            13 game modes, maps catalog
│       ├── progression/           XP, ranks, seasons, ribbons
│       ├── datatables/            379+ DataTables indexed
│       ├── config/                Game config docs, feature flags
│       ├── ai/                    AI behavior trees, boss AI
│       ├── market/                Store, contracts
│       ├── damage/                Damage formulas
│       ├── diagrams/              ASCII system relationship diagrams
│       ├── file_index.md          Master index of 4,854 files
│       └── summary.md             Comprehensive 940-line overview
│
├── DreadGame/                    ← Extracted UE4 game content
│   ├── Config/                   62 INI+JSON game config files
│   ├── Content/                  Maps, environments, ships, weapons, UI
│   ├── Plugins/                  8 UE4 plugins (YMmogbrain, OnlineSubsystemMmogbrain, etc.)
│   └── DreadGame.uproject
│
├── test/                         ← Test data (JSON lookup tables)
├── ue-env/                       ← Python 3.13 venv for UE4 tooling
├── DreadGame.zip                 ← Compressed game assets
└── *.log, *.pid                  ← Runtime logs and PID files
```

## Session Startup Checklist

Every session, in order:

1. **Confirm access:**
   ```
   ls dreadnought-private-server/ src/Documents/ src/Dreadnought/
   ```

2. **Load progress tracker:**
   ```
   cat dreadnought-private-server/progress.md
   ```

3. **Load issues:**
   ```
   cat dreadnought-private-server/issues.md
   ```

4. **If pending/in-progress steps exist, continue them immediately.**

5. **Before implementing any feature:**
   - Read relevant files in `src/Documents/` (NOT in git — read-only reference)
   - NEVER modify `src/Documents/` or `src/Dreadnought/`
   - If documentation is missing or incomplete, report it — don't invent

## Key Rules

- **All code changes go in `dreadnought-private-server/`** (the git repo)
- **`src/` is READ-ONLY** — game files, decompilation output, documentation
- **`Documents/` has moved back to `src/Documents/`** (not in the git repo)
- **Never break working functionality** — flag risks before proceeding
- **Never guess silently** — mark inferences with `// [INFERRED]` and explain
- **Keep both codebases in sync** — protocol/data changes must match on both sides
- **Update progress.md after every completed step**
- **Never redo completed work** — check progress.md first

## Service Architecture

```
[Windows Client]
      │
      ├── HTTPS :443 → gateway (TLS termination + Host-header routing)
      │     ├── profile-api.*       → auth-server :8081
      │     ├── legacyapi.*         → legacy-api  :8082
      │     ├── mmog.*              → mmogbrain   :8083
      │     └── masterserver.*      → master-server :8084
      │
      ├── TLS :48843 → mmogbrain (Firmament JSON-RPC + YMmogbrain binary)
      │
      └── UDP :7777-7877 → DreadGame-Win64-Shipping.exe (dedicated servers)
                              spawned by game-manager :8085
```

## JWT Format

```
Algorithm: HS256
Claims: sub, username, realm="dreadnought.pc-us", aud="dreadnought"|"launcher"
Issuer: "Dreadnought-Revival-project"
TTL: 24 hours
```

## Build & Test Commands

```bash
# Build all services
bash dreadnought-private-server/scripts/setup.sh

# Build single service
cd dreadnought-private-server/auth-server && go build -o ../run/auth-server .

# Run all tests (mmogbrain has the most)
cd dreadnought-private-server/mmogbrain && go test ./...

# Run a single test
cd dreadnought-private-server/mmogbrain && go test -run TestExtractMmogPlayerPID ./...

# Lint everything
cd dreadnought-private-server && golangci-lint run ./...

# Health-check all services
curl http://127.0.0.1:8081/health  # auth-server
curl http://127.0.0.1:8082/health  # legacy-api
curl http://127.0.0.1:8083/health  # mmogbrain
curl http://127.0.0.1:8084/health  # master-server
curl http://127.0.0.1:8085/health  # game-manager
```

## Service Details

| Service | Port | DB | Key Files | Tests |
|---------|------|----|-----------|-------|
| auth-server | 8081 | auth.db | main.go, handlers/handlers.go, jwt/jwt.go | None |
| legacy-api | 8082 | legacy.db | handlers/handlers.go, inventory_bootstrap.go | 682 lines |
| mmogbrain | 8083, 48843 | mmog.db | main.go (4586 lines), matchmaker/, handlers/ | 3200+ lines |
| master-server | 8084 | master.db | handlers/handlers.go | None |
| game-manager | 8085 | (none) | spawner/spawner.go, portpool/pool.go | None |
| gateway | 80, 443 | (none) | main.go (281 lines) | None |
| admin-cli | CLI | (none) | main.go (321 lines) | None |
| dn-launcher | Client | (none) | main.go (447 lines) | None |
| shared/* | — | db/db.go | middleware, logging, dreadgameconfig | 179 lines |

## Environment Variables

| Variable | Used By | Default | Description |
|----------|---------|---------|-------------|
| JWT_SECRET | auth, legacy, mmog | changeme-... | HMAC key for JWT signing |
| DB_PATH | auth, legacy, mmog, master | <svc>.db | SQLite database path |
| ADDR | all | :<port> | Listen address |
| SERVER_IP | game-manager | 127.0.0.1 | Public IP for clients |
| GAME_BINARY | game-manager | /src/... | Path to DreadGame.exe |
| WINE_EXE | game-manager | wine | Wine executable |
| MASTER_URL | game-manager | http://127.0.0.1:8084 | Master server URL |
| GAME_MGR_URL | mmogbrain | http://127.0.0.1:8085 | Game manager URL |
| ADMIN_KEY | auth, admin-cli | changeme-... | Admin API key |
| TLS_CERT/TLS_KEY | gateway | certs/... | TLS certificate paths |
| FIRMAMENT_CERT/KEY | mmogbrain | (none) | Firmament TLS cert |
| PLAYERS_PER_MATCH | mmogbrain | 2 | Players needed per match |
| TLS_CERT_FINGERPRINT | dn-launcher | (none) | SHA256 of server cert |

## Key Protocol Details

- **UE4 wire protocol:** 17-byte packet header (magic 0x55453400), 6 packet types, 5 channels
- **YMmogbrain binary:** Custom SAX-like tagged field encoding, RC4 variant stream cipher
- **Firmament:** JSON-RPC 2.0 over TLS, newline-delimited, on port 48843
- **Gateway routing:** Host-header based, with special handling for `/auth/` rate limiting

## Current State (2026-05-23)

- All services build and pass lint (0 golangci-lint issues)
- mmogbrain: 36 critical/high/medium fixes applied
- 25 source files modified across all services
- **24/24 CRITICAL+HIGH issues resolved** (C1-C8, H1-H16)
  - 4 new fixes applied this session: C2 (logging service field), C3 (jwtMiddleware session check), H6 (JWT audience validation), H3/H12 (RateLimiter memory leak)
  - ~20 issues pre-resolved in the 36-fix batch
- 30 MEDIUM issues tracked, 2 resolved
- Remaining major tasks: mmogbrain refactor, service consolidation, test coverage
