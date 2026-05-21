# Dreadnought Private Server

A community-operated private server infrastructure for the discontinued game **Dreadnought** (UE4, Steam App 835860). Allows players to connect, authenticate, queue for matches, and play on instances of the original game engine.

## Architecture

```
[Windows Client (unmodified)]
        |
        | HTTPS (redirected via hosts file + self-signed CA)
        v
[Gateway :443]  ← TLS termination + reverse proxy
        |
        ├── profile-api.prod.greybox.sixfoot.live → [Auth Server :8081]   (Go + SQLite)
        ├── legacyapi.prod.greybox.sixfoot.live   → [Legacy API   :8082]   (Go + SQLite)
        ├── mmog.greybox.sixfoot.live             → [YMmogbrain   :8083]   (Go + SQLite)
        └── masterserver.local                    → [Master Server :8084]  (Go + SQLite)

[YMmogbrain Matchmaker] ──match found──► [Game Instance Manager :8085]
                                                    |
                                                    ▼
                                         Wine + DreadGame-Win64-Shipping.exe
                                         (one process per active match, port 7777–7877 UDP)
```

## Services

| Service | Port | Purpose |
|---|---|---|
| **gateway** | 80, 443 | TLS termination + reverse proxy for all Greybox API calls |
| **auth-server** | 8081 | Player registration, login, JWT issuance |
| **legacy-api** | 8082 | Player profiles, inventory, match history |
| **mmogbrain** | 8083 | Matchmaking queue, match assignment, chat |
| **master-server** | 8084 | Server registry, heartbeat, server browser |
| **game-manager** | 8085 | Spawns and monitors Wine+DreadGame instances |
| **DreadGame (Wine)** | 7777–7877 UDP | One dedicated game server per active match |

## Quick Start

### 1. Prerequisites (Server — Linux)
```bash
apt install golang openssl wine  # or wine-staging for better compatibility
go version  # needs 1.21+
```

### 2. Build and configure
```bash
cd /root/projects/dreadnought-private-server

# Generate TLS certificates
bash scripts/gen-certs.sh

# Build all services
bash scripts/setup.sh

# Set a strong JWT secret (same for all services)
export JWT_SECRET="$(openssl rand -hex 32)"
```

### 3. Start services (bare metal)
```bash
cd run/

JWT_SECRET=$JWT_SECRET DB_PATH=../data/auth.db   ./auth-server &
JWT_SECRET=$JWT_SECRET DB_PATH=../data/legacy.db  ./legacy-api &
JWT_SECRET=$JWT_SECRET DB_PATH=../data/mmog.db    GAME_MGR_URL=http://127.0.0.1:8085 ./mmogbrain &
                        DB_PATH=../data/master.db  ./master-server &
SERVER_IP=<your-ip>    GAME_BINARY=/src/Dreadnought/DreadGame/DreadGame/Binaries/Win64/DreadGame-Win64-Shipping.exe \
                        MASTER_URL=http://127.0.0.1:8084 ./game-manager &
TLS_CERT=../certs/server.crt TLS_KEY=../certs/server.key ./gateway &
```

### 4. Or use Docker Compose
```bash
SERVER_IP=<your-public-ip> JWT_SECRET=<secret> docker-compose -f scripts/docker-compose.yml up -d
```

### 5. Configure client machines
On each Windows client machine, add to `C:\Windows\System32\drivers\etc\hosts`:
```
<SERVER_IP>  profile-api.prod.greybox.sixfoot.live
<SERVER_IP>  legacyapi.prod.greybox.sixfoot.live
<SERVER_IP>  mmog.greybox.sixfoot.live
```

Or on Linux clients:
```bash
SERVER_IP=<your-server-ip> bash scripts/hosts-redirect.sh
```

Then install `certs/ca.crt` as a trusted root CA:
- **Windows:** `certmgr.msc` → Trusted Root Certification Authorities → Import
- **Linux:** `sudo cp certs/ca.crt /usr/local/share/ca-certificates/dn-ps.crt && sudo update-ca-certificates`

### 6. Launch the game
Start the Dreadnought launcher normally. It will connect to your private server instead of the official Greybox backend.

## Source Files Used (`/src`)

This project uses the following from `/src` per the project constraint:

| Asset | Source Path | Usage |
|---|---|---|
| Game binary | `/src/Dreadnought/DreadGame/DreadGame/Binaries/Win64/DreadGame-Win64-Shipping.exe` | Runs as dedicated match server (via Wine) |
| Launcher | `/src/Dreadnought/DreadnoughtLauncher.exe` | Client launcher (connects to our auth/legacy APIs) |
| Network docs | `/src/Documents/networking/` | Protocol reference for all emulated endpoints |
| Config docs | `/src/Documents/config/launcher_embedded_config.md` | API URL structure for redirect strategy |
| Ghidra project | `/src/Dreadnought-Ghidra-Project/` | Binary analysis for YMmogbrain protocol reverse engineering |

## Dedicated Server Command Line

The game binary supports dedicated server mode:
```bash
wine DreadGame-Win64-Shipping.exe \
  -dedicatedserver \
  -port=7777 \
  -maxplayers=10 \
  -GameMode=YGameMode_TeamDeathmatch \
  -Map=Charon \
  -nop4 -nosound -noeac -NoSteam \
  -log=server.log
```

## Project Structure

```
dreadnought-private-server/
├── auth-server/     # Go + SQLite — player auth (JWT)
├── legacy-api/      # Go + SQLite — player profiles, inventory, match history
├── mmogbrain/       # Go + SQLite — matchmaking queue, match lifecycle, chat
├── master-server/   # Go + SQLite — server registry and browser
├── game-manager/    # Go — Wine process spawner and port pool manager
├── gateway/         # Go — TLS termination + reverse proxy
├── shared/          # Shared Go packages (db, logging, middleware)
├── certs/           # Self-signed CA + server TLS cert (generated by setup.sh)
├── data/            # SQLite database files (created at runtime)
├── run/             # Compiled binaries (created by setup.sh)
├── scripts/
│   ├── setup.sh             # Build + certificate generation
│   ├── gen-certs.sh         # TLS certificate generation
│   ├── hosts-redirect.sh    # Client hosts file setup
│   └── docker-compose.yml   # Docker deployment
└── docs/
    ├── PROTOCOL.md   # UE4 wire protocol + endpoint reference
    └── API.md        # Full REST API documentation
```

## Environment Variables

All services read configuration from environment variables:

| Variable | Services | Default | Description |
|---|---|---|---|
| `JWT_SECRET` | auth, legacy, mmog | `changeme-...` | **Change this!** HMAC key for JWT signing |
| `DB_PATH` | auth, legacy, mmog, master | `<svc>.db` | SQLite database file path |
| `ADDR` | all | `:<port>` | Listen address |
| `SERVER_IP` | game-manager | `127.0.0.1` | Public IP reported to clients |
| `GAME_BINARY` | game-manager | `/src/.../DreadGame.exe` | Path to game executable |
| `WINE_EXE` | game-manager | `wine` | Wine executable (`none` for Windows) |
| `MASTER_URL` | game-manager | `http://127.0.0.1:8084` | Master server URL |
| `GAME_MGR_URL` | mmogbrain | `http://127.0.0.1:8085` | Game manager URL |
| `TLS_CERT` | gateway | `certs/server.crt` | TLS certificate path |
| `TLS_KEY` | gateway | `certs/server.key` | TLS private key path |

## Security Notes

- **Change `JWT_SECRET`** before deploying. Use `openssl rand -hex 32`.
- TLS certs are self-signed. Install the CA on client machines.
- SQLite databases are stored as plain files. Use filesystem-level encryption for data at rest if needed.
- Rate limiting is applied to auth endpoints (100 req/min per IP) in the gateway.

## Troubleshooting

**Game binary fails to launch via Wine:**
- Install Wine Staging for better UE4 compatibility
- Try `WINEDEBUG=fixme-all` to reduce log noise
- The game binary is Win64; Wine must be 64-bit capable

**TLS errors in launcher:**
- Verify the CA cert is installed as a trusted root on the client
- Verify the hosts file redirect is in place
- Check gateway logs: `docker logs dn-ps-gateway`

**Players not matching:**
- Check `mmogbrain` logs for matchmaker ticks
- By default `PLAYERS_PER_MATCH=2` for easy testing; increase as needed
- Check game-manager can reach master-server

## Documentation

- [Protocol Reference](docs/PROTOCOL.md) — UE4 wire protocol, port table, JWT format
- [API Reference](docs/API.md) — Full REST API for all services
