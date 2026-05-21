# Dreadnought Private Server — Architecture

## Overview

The Dreadnought Private Server infrastructure emulates the original Greybox/Yager backend services
so that the **unmodified game client and launcher** can connect to community-operated servers.

The approach:
1. Redirect Greybox API domains to our server via `/etc/hosts` (or DNS override)
2. Present self-signed TLS certificates trusted by the client machine
3. Serve compatible API responses from our Go backend services
4. Use the original `DreadGame-Win64-Shipping.exe` binary (via Wine) as the dedicated match server

---

## System Diagram

```
[Windows Client]
      │
      │ HTTPS (hosts-file redirect: *.greybox.sixfoot.live → server IP)
      ▼
┌─────────────────────────────────────────────────────────┐
│                    Gateway (:443)                        │
│  TLS termination · routing by Host header · rate limit  │
└──┬───────────────┬────────────────┬───────────────┬─────┘
   │               │                │               │
   ▼               ▼                ▼               ▼
Auth Server    Legacy API      YMmogbrain      Master Server
  :8081          :8082            :8083            :8084
  SQLite         SQLite           SQLite           SQLite
  JWT HS256      Profiles         Matchmaking      Server list
  Register       Inventory        Lobby/Chat       Heartbeat
  Login          Match results    Queue
   │                                   │
   │                                   │ Spawn on match-found
   │                                   ▼
   │                            Game Manager (:8085)
   │                            Port pool: 7777-7877
   │                            Wine + DreadGame.exe
   │                                   │
   └──────────── Admin CLI ────────────┘
                (CLI tool, reads all services)
```

---

## Components

### Gateway (`:443`)

- Language: Go (`net/http`, `httputil.ReverseProxy`)
- Routes traffic by `Host` header to internal services
- Terminates TLS with self-signed cert covering all Greybox domains
- Rate-limits `/auth/` to 100 req/min per IP
- HTTP → HTTPS redirect on port 80

**Config:** `gateway/config.yaml`

---

### Auth Server (`:8081`)

Emulates `profile-api.prod.greybox.sixfoot.live`.

- Language: Go
- Database: SQLite (`auth.db`)
- Issues HS256 JWT with Dreadnought-compatible claims:
  - `sub` = user UUID
  - `realm` = `dreadnought.pc-us`
  - `aud` = `dreadnought`
- Endpoints:
  - `POST /auth/` — Login (Dreadnought launcher compat)
  - `POST /auth/register` — Registration
  - `GET /auth/me` — Token validation
  - `POST /auth/logout` — Session invalidation
  - `POST /admin/ban` — Ban player (admin key required)
  - `POST /admin/unban` — Unban player (admin key required)
  - `GET /metrics` — Prometheus metrics
  - `GET /health` — Health check

**Schema:** `users`, `sessions`, `bans`

---

### Legacy API (`:8082`)

Emulates `legacyapi.prod.greybox.sixfoot.live`.

- Language: Go
- Database: SQLite (`legacy.db`)
- Returns launcher tiles (news), age consent approval, player profiles and inventory
- Records match results and updates player statistics
- Endpoints:
  - `GET /v2/dreadnought/launcher/dn/tiles/` — Launcher news tiles
  - `GET /v2/dreadnought/ageconsent/` — Age consent (always approved)
  - `GET /v2/dreadnought/player/{id}/profile` — Player profile + stats
  - `GET /v2/dreadnought/player/{id}/inventory` — Ships + modules
  - `POST /v2/dreadnought/match/result` — Record match result
  - `GET /metrics` — Prometheus metrics
  - `GET /health` — Health check

**Schema:** `player_profiles`, `player_stats`, `player_inventory`, `match_history`, `match_players`

---

### YMmogbrain Emulator (`:8083`)

Emulates Yager's proprietary YMmogbrain matchmaking backend.

- Language: Go
- Database: SQLite (`mmog.db`)
- Runs an internal matchmaker goroutine (polls every 3 seconds)
- Groups players by game mode; fires match when `PLAYERS_PER_MATCH` (default: 2 for testing, 10 for production) are ready
- Calls Game Manager to spawn a dedicated server instance
- Endpoints:
  - `POST /mmog/queue` — Join matchmaking queue
  - `GET /mmog/queue/status` — Poll queue/match status
  - `DELETE /mmog/queue` — Leave queue
  - `GET /mmog/match/{id}` — Get match details (server IP:port)
  - `POST /mmog/chat` — Send chat message
  - `GET /mmog/chat` — Retrieve chat history
  - `GET /admin/queue` — List all queue entries (admin key required)
  - `GET /metrics` — Prometheus metrics
  - `GET /health` — Health check

**Schema:** `queue_entries`, `matches`, `match_slots`, `chat_messages`

**Key config:** `PLAYERS_PER_MATCH=10` for production (set to 2 for testing)

---

### Master Server (`:8084`)

Central server registry and browser.

- Language: Go
- Database: SQLite (`master.db`)
- Game instances self-register and send heartbeats (60s timeout)
- Provides server browser list to clients
- Endpoints:
  - `POST /servers/register` — Register game instance
  - `DELETE /servers/{id}` — Deregister
  - `POST /servers/{id}/heartbeat` — Heartbeat
  - `GET /servers` — Server browser list
  - `GET /metrics` — Prometheus metrics
  - `GET /health` — Health check

**Schema:** `game_servers`, `server_events`

---

### Game Manager (`:8085`)

Spawns and monitors dedicated game server processes.

- Language: Go
- No database (in-memory state)
- Port pool: 7777–7877 (101 concurrent matches)
- On match-found: acquires port → starts Wine + game binary → registers with Master Server
- Monitors process health; deregisters dead instances
- **Mock mode:** If `WINE_EXE=none` or game binary absent, records a mock instance (for testing)
- Endpoints:
  - `POST /instances` — Launch new instance
  - `GET /instances` — List running instances
  - `DELETE /instances/{id}` — Stop instance
  - `GET /metrics` — Prometheus metrics
  - `GET /health` — Health check

**Game binary flags:**
```
wine DreadGame-Win64-Shipping.exe \
  -dedicatedserver -port=<PORT> -maxplayers=10 \
  -GameMode=<MODE> -Map=<MAP> \
  -AuthServer=http://127.0.0.1:8081 \
  -nop4 -nosound -noeac -NoSteam
```

---

### Admin CLI

Command-line management tool for server operators.

- Language: Go (single binary, no dependencies on running services' databases)
- Communicates with services via their REST APIs
- Protected by `X-Admin-Key` header
- Commands: `status`, `servers`, `instances`, `stop-instance`, `ban`, `unban`, `queue`, `chat`

Usage:
```bash
ADMIN_KEY=<key> admin-cli status
ADMIN_KEY=<key> admin-cli ban cheater123 "Speed hacking"
ADMIN_KEY=<key> admin-cli stop-instance <instance-id>
```

---

## Data Flow: Login → Match → Stats

```
1. Launcher reads API URLs from embedded production.json
2. Hosts redirect: profile-api.greybox... → our server
3. Launcher sends POST /auth/ with credentials
4. Auth Server issues JWT (HS256, realm=dreadnought.pc-us)
5. Launcher stores JWT; game client uses it for all subsequent calls
6. Client GETs /v2/.../tiles/, /ageconsent/, /player/profile, /player/inventory
7. Client sends POST /mmog/queue with game_mode
8. Matchmaker goroutine groups 2+ players → calls Game Manager POST /instances
9. Game Manager: Wine + DreadGame.exe starts on port 7777+
10. Game Manager: registers instance with Master Server
11. Matchmaker: records match in SQLite, updates queue entries to "matched"
12. Client polls GET /mmog/queue/status → receives {server_ip, server_port}
13. Client connects to game server directly via UDP:7777
14. Match plays out via UE4 networking (direct client↔server, no relay)
15. Post-match: client/server POSTs /v2/.../match/result
16. Legacy API updates player_stats (kills, deaths, XP, credits)
```

---

## Port Reference

| Service | Protocol | Port | Notes |
|---|---|---|---|
| Gateway | TCP/HTTPS | 443 | TLS terminator for all Greybox domains |
| Gateway | TCP/HTTP | 80 | Redirect to HTTPS |
| Auth Server | TCP/HTTP | 8081 | Internal only |
| Legacy API | TCP/HTTP | 8082 | Internal only |
| YMmogbrain | TCP/HTTP | 8083 | Internal only |
| Master Server | TCP/HTTP | 8084 | Internal only |
| Game Manager | TCP/HTTP | 8085 | Internal only |
| Game Instances | UDP | 7777–7877 | One port per active match |

---

## Security Model

- **TLS:** Self-signed CA; cert covers all Greybox domains. CA cert must be installed on client.
- **Authentication:** HS256 JWT, secret in `JWT_SECRET` env var. Rotate periodically.
- **Admin access:** All admin endpoints require `X-Admin-Key` header.
- **Rate limiting:** Gateway enforces 100 req/min per IP on `/auth/` to prevent brute force.
- **Database:** SQLite WAL mode for concurrent reads. Backup via `scripts/backup.sh`.
- **Input validation:** All handlers validate required fields and sanitize SQL via parameterized queries.

---

## Deployment

### Single host (bare metal / VM)

```bash
# 1. Generate certs
bash scripts/gen-certs.sh

# 2. Add hosts entries on client machines (or set up DNS)
bash scripts/hosts-redirect.sh 1.2.3.4   # replace with your server IP

# 3. Start all services
JWT_SECRET=<secret> ADMIN_KEY=<key> bash scripts/setup.sh
```

### Docker Compose

```bash
cd scripts
docker-compose up -d
```

See `scripts/docker-compose.yml` for environment variable configuration.

---

## Known Limitations & Open Questions

| Item | Status | Notes |
|---|---|---|
| YMmogbrain protocol | **UNKNOWN** | HTTP REST emulation; actual UE4 plugin may use binary WebSocket/UDP. Requires packet capture during live testing. |
| EasyAntiCheat | **RISK** | `-noeac` flag may not bypass EAC for dedicated server. Test with actual binary. |
| Steam integration | **RISK** | May need `steam_appid.txt` + Steamworks emulator (Goldberg) for the game binary. |
| Launcher ZIP patch | **OPTIONAL** | Alternative to hosts-file: extract+patch+repack `production.json` in launcher binary. |
| UE4 Beacon protocol | **UNKNOWN** | Game server may use UE4 Online Beacon for matchmaking handoff (not HTTP). |
