# Dreadnought Revival Project

[![GitHub](https://img.shields.io/badge/GitHub-darkace1998/Dreadnought--Revival--project-blue?logo=github)](https://github.com/darkace1998/Dreadnought-Revival-project)

A community-operated private server for the discontinued game **Dreadnought** (UE4 4.13.1, Steam App 835860). The goal is to run the **unmodified game client** against our own backend: everything is emulated server-side, and the only client-side component is a replacement launcher.

> **Status.** Login, the hangar, inventory, the market, ship loadouts, career progression and matchmaking work. The tech tree screen is the current work in progress — see [Current state](#current-state).

## Architecture

```
[Windows client (unmodified)]
        |
        |  hostnames redirected via hosts file, TLS trusted via our own CA
        |
        ├─ HTTPS :443 ──► [gateway]  TLS termination + reverse proxy
        │                     ├── profile-api.prod.greybox.sixfoot.live ─► [auth-server   :8081]
        │                     ├── legacyapi.prod.greybox.sixfoot.live   ─► [legacy-api    :8082]
        │                     └── masterserver.local                    ─► [master-server :8084]
        │
        ├─ HTTPS :65443 ─► [mmogbrain]  Greybox web-services API (catalog, inventory, session)
        └─ TLS   :48843 ─► [mmogbrain]  firmament social/presence socket

[mmogbrain matchmaker] ──match formed──► [game-manager :8085]
                                               │
                                               ▼
                                     wine + DreadGame-Win64-Shipping.exe
                                     (one process per match, UDP 7777-7877)
```

Two different things are called "gateway", which is worth knowing before reading the config:

- the **`gateway` service** is the TLS reverse proxy on ports 80/443;
- **`GATEWAY_ADDR` (:65443)** is a socket inside **`mmogbrain`** — the Greybox web-services API the client calls directly.

## Services

| Service | Ports | Purpose |
|---|---|---|
| **gateway** | 80, 443, 57005 | TLS termination, reverse proxy, crash-report receiver |
| **auth-server** | 8081 | Registration, login, JWT issuance |
| **legacy-api** | 8082 | Player profiles, inventory, match history |
| **mmogbrain** | 8083, 48843, 65443 | The bulk of the backend: mmog binary protocol, catalog, fleets, tech tree, matchmaking |
| **master-server** | 8084 | Server registry, heartbeat, server browser |
| **game-manager** | 8085 | Spawns and monitors battle-server processes |
| **DreadGame (Wine)** | 7777-7877/UDP | One battle server per active match |
| **admin-cli** | — | Operator CLI (`servers`, `instances`, `stop-instance`, `ban`, `unban`, `queue`, `chat`) |
| **dn-launcher** | — | Windows launcher replacement (register / sign in / start the game) |

---

## Build and run (server, Linux)

### 1. Prerequisites

```bash
sudo apt install golang openssl curl iproute2
sudo apt install wine            # or wine-staging; only needed to host matches
go version                       # needs 1.24+
```

Go **1.24 or newer** is required — `go.work` declares 1.25 and the modules declare 1.24. An older toolchain fails with a per-module `go.mod requires go >= ...`.

### 2. Clone and set up

```bash
git clone https://github.com/darkace1998/Dreadnought-Revival-project.git
cd Dreadnought-Revival-project

bash scripts/setup.sh
```

`setup.sh` is safe to re-run and does everything needed for a first start:

- checks the toolchain (and warns, without failing, if Wine is absent);
- generates a self-signed CA and server certificate into `certs/` if none exists;
- builds all seven services into `run/`, plus `run/dn-launcher.exe` (cross-compiled for Windows);
- writes `run/secrets.env` with a freshly generated `JWT_SECRET` and `ADMIN_KEY` (mode 600) if it does not exist;
- verifies the extracted game data under `data/` is present.

It never overwrites existing certificates or secrets.

### 3. Configure

Everything the operator sets lives in **`run/secrets.env`** — see [`scripts/secrets.env.example`](scripts/secrets.env.example) for the annotated list. `run/` is gitignored, so real secrets never enter the repository.

The one value you must set by hand to host matches is the path to the game executable:

```bash
GAME_BINARY=/path/to/Dreadnought/DreadGame/DreadGame/Binaries/Win64/DreadGame-Win64-Shipping.exe
```

There is no dedicated-server build of Dreadnought. The battle server is the **ordinary client executable** launched headless (`"<map>?listen" -server -nullrhi -unattended`), which is why this points at the same `.exe` players run.

`SERVER_IP` is auto-detected from the default route and only needs setting on multi-homed hosts, or when clients reach you through a router or VPN. It must match the IP the certificate covers.

### 4. Start and stop

```bash
bash scripts/start-services.sh      # starts all six services, then health-checks them
bash scripts/stop-services.sh
```

`start-services.sh` refuses to double-start anything already running, pins each service's `DB_PATH` so the working directory cannot decide which database is opened, and prints the listening sockets when it finishes. Logs land in `run/<service>.log`.

A healthy start ends with sockets on 80, 443, 8081-8085, 48843 and 65443.

### 5. Point clients at the server

On the **server**, the certificate must cover the address clients dial. `gen-certs.sh` auto-detects it; override and regenerate when needed:

```bash
rm -rf certs/ && SERVER_IP=<your-lan-ip> bash scripts/gen-certs.sh
```

`certs/` is gitignored, so this leaves your working tree clean. Regenerating
mints a new CA, so redistribute the new `certs/ca.crt` to every client
afterwards — the old one will no longer be trusted.

On each **client** machine:

```powershell
# Windows (elevated PowerShell)
powershell -ExecutionPolicy Bypass -File scripts\hosts-redirect.ps1 -ServerIP <your-server-ip>
```

```bash
# Linux
SERVER_IP=<your-server-ip> sudo bash scripts/hosts-redirect.sh
```

Then install `certs/ca.crt` as a trusted root CA:

- **Windows:** `certmgr.msc` → Trusted Root Certification Authorities → Import
- **Linux:** `sudo cp certs/ca.crt /usr/local/share/ca-certificates/dn-ps.crt && sudo update-ca-certificates`

### 6. Launch the game

Run `dn-launcher.exe` (built into `run/`) on the client. It offers **Create account** and **Sign in** with an email and password, stores the returned token with DPAPI so it is readable only by that Windows user, and starts the game. `dn-launcher.exe --sign-out` clears the stored credentials.

Because the account lives on the server rather than being derived from the machine, the same login works from any PC.

---

## Current state

Working end to end: account registration and login, the hangar, the starter fleet with weapons/modules/officer slots, inventory, the market catalog (62 items), ship and captain customisation, career goal progression, the daily login bonus, matchmaking, and battle-server spawning.

Known gaps:

- **Tech tree** — the active work. Two server-side causes have been fixed (the `Prereq` encoding and the `ClassId` category); remaining downstream warnings are still being worked through.
- **Daily contracts** — the client's loader never reads the contract list, so no payload avoids its quest-cycle recursion.
- **Market artwork** — item icons come from the client's own assets and work; the storefront banner URLs (`ImgUrlS/M/L`) pointed at a Greybox CDN that no longer exists, so those areas render blank.

## Project layout

```
Dreadnought-Revival-project/
├── auth-server/     Go + SQLite -- registration, login, JWT
├── legacy-api/      Go + SQLite -- profiles, inventory, match history
├── mmogbrain/       Go + SQLite -- mmog binary protocol, catalog, fleets, matchmaking
├── master-server/   Go + SQLite -- server registry and browser
├── game-manager/    Go         -- battle-server spawner and port pool
├── gateway/         Go         -- TLS termination and reverse proxy
├── admin-cli/       Go         -- operator CLI
├── dn-launcher/     Go         -- Windows launcher replacement (client-side)
├── shared/          shared packages (db, logging, middleware, game data loader)
├── data/            extracted game data: item tables, loadouts, assets (committed)
├── certs/           CA + server certificate  (generated, gitignored)
├── run/             binaries, databases, logs, secrets.env  (gitignored)
├── scripts/
│   ├── setup.sh              build + certs + secrets, one shot
│   ├── start-services.sh     start the stack
│   ├── stop-services.sh      stop the stack
│   ├── gen-certs.sh          CA + server certificate
│   ├── secrets.env.example   annotated configuration template
│   ├── hosts-redirect.sh     client hostname redirect (Linux)
│   ├── hosts-redirect.ps1    client hostname redirect (Windows)
│   ├── backup.sh             database backup
│   └── docker-compose.yml    container deployment (unmaintained; see below)
└── docs/
    ├── client-data-reference.md    extracted item/ship id maps and naming rules
    ├── client-data-validation.md   audit of every id the server emits
    └── reference/                  third-party reference material
```

`scripts/docker-compose.yml` predates the current service topology and is not exercised by the maintainers; `scripts/start-services.sh` is the supported path.

## Development

The repository is a Go workspace (`go.work`) of independent modules under `github.com/darkace1998/Dreadnought-Revival-project/<service>`.

```bash
go build ./mmogbrain          # build one service
go test ./mmogbrain/...       # run one suite
go vet ./mmogbrain/...        # per module; the workspace has no unified ./...
```

Useful debug switches, all read by `mmogbrain`:

| Variable | Effect |
|---|---|
| `DN_TECHTREE_LIMIT` | Cap tech tree items per manufacturer group (for bisecting client-side load failures) |
| `DN_ANSWER_DAILY_CONTRACTS` | Send a daily-contracts payload instead of suppressing it |
| `DN_NO_DEFER_PLAYER_FLEETS` | Answer `YA_PlayerFleets` immediately instead of after player data |
| `DATA_DIR` | Override the location of the extracted game data |

`DN_NO_DEFER_PLAYER_FLEETS` reproduces a real bug: answering the fleet request before player data makes the client reject a byte-perfect fleet array with "Invalid fleet data, fleet array is empty".

## Configuration reference

Set in `run/secrets.env` unless noted.

| Variable | Services | Default | Description |
|---|---|---|---|
| `JWT_SECRET` | auth, legacy, mmog | — | **Required.** HMAC key for JWTs; must be identical across services |
| `ADMIN_KEY` | all | — | Key for `/admin` endpoints and `admin-cli` |
| `INTERNAL_API_KEY` | mmog, game-mgr, master, legacy | `ADMIN_KEY` | Service-to-service authentication |
| `DB_PATH` | auth, legacy, mmog, master | pinned by the start script | SQLite file path |
| `ADDR` | all | `:<service port>` | Plain HTTP listen address |
| `SERVER_IP` | game-manager | auto-detected | Address handed to clients for battle servers |
| `GAME_BINARY` | game-manager | — | Path to `DreadGame-Win64-Shipping.exe` |
| `WINE_EXE` | game-manager | `wine` | Wine executable; `none` on Windows |
| `PLAYERS_PER_MATCH` | mmogbrain | `1` | Queued players needed to form a match |
| `MASTER_URL` | game-manager | `http://127.0.0.1:8084` | Master server URL |
| `GAME_MGR_URL` | mmogbrain | `http://127.0.0.1:8085` | Game manager URL |
| `DN_FORCE_GAME_MODE` | mmogbrain | `TM` | Game mode matches actually run in, whatever the player queued for. A battle server has no backend, so its loadout manager is empty and only a mode that supplies its own loadout (`TM`) can spawn a pawn — every other mode leaves the player a spectator. Set to empty to honour the queued mode instead. Remove once the battle server can obtain player data |
| `DN_CONNECT_PUSH_DELAY` | mmogbrain | `75s` (`45s` from `start-services.sh`) | **Fallback only.** How long to hold the `YA_Connect` travel push back when the control plane never reports the battle server ready. Normally the matchmaker polls `GET /instances/<id>` and pushes as soon as the engine is hosting; the push's log line records which gate opened as `gate=ready` or `gate=delay` |
| `DN_GAME_MGR_TIMEOUT` | mmogbrain | `30s` | How long the matchmaker waits for game-manager to answer `POST /instances`. The matchmaker ticks on one goroutine, so a control plane that accepts the connection and never replies would otherwise stop matchmaking for everyone. Raise it if your control plane waits for the engine to report ready before answering |
| `GATEWAY_ADDR` / `GATEWAY_CERT` / `GATEWAY_KEY` | mmogbrain | `:65443`, `certs/server.*` | Client-facing web-services socket |
| `FIRMAMENT_ADDR` / `FIRMAMENT_CERT` / `FIRMAMENT_KEY` | mmogbrain | `:48843`, `certs/firmament.*` | Social/presence socket. Uses its **own** certificate: the client pins this connection, and `gen-certs.sh` builds `certs/firmament.*` with an "Amazon RSA 2048 M01" issuer to satisfy the pin |
| `HTTP_ADDR` / `HTTPS_ADDR` | gateway | `:80`, `:443` | Proxy listen addresses |
| `TLS_CERT` / `TLS_KEY` | gateway | `certs/server.crt`, `.key` | Proxy certificate |
| `CRASH_RECEIVER_ADDR` / `CRASH_REPORT_DIR` | gateway | `:57005`, `crash-reports` | UE4 crash-report receiver |

## Security notes

- `setup.sh` generates random secrets. If you write your own, use `openssl rand -hex 32`.
- **`certs/` is generated, not committed.** Every install mints its own CA on first `setup.sh`, so no private key in this repository can sign a certificate your clients trust. Keep `certs/ca.key` to yourself; anyone holding it can mint certificates your clients will accept. The directory is gitignored, so `git add -A` cannot publish it by accident.
- SQLite databases are plain files under `run/`. Use filesystem encryption if that matters to you.
- The gateway rate-limits proxied requests to 100/min per IP, and crash-report uploads to 5/min per IP.

## Troubleshooting

**`run/secrets.env must define JWT_SECRET`** — run `bash scripts/setup.sh`, or copy `scripts/secrets.env.example` to `run/secrets.env` and fill it in.

**Client shows a TLS or connection error** — check that `certs/ca.crt` is installed as a trusted root on the client, that the hosts redirect is in place, and that the certificate covers the IP the client dials:

```bash
openssl x509 -in certs/server.crt -noout -text | grep -A1 'Subject Alternative Name'
```

Regenerate with `SERVER_IP=<ip>` if it does not.

**Players queue but never get a match** — all six services must be running. The matchmaker POSTs to `game-manager`, and when nothing answers it rolls the queue back to `waiting` with no error shown to the player. Check `run/game-manager.log` and `run/mmogbrain.log`.

**A match forms but no battle server starts** — `GAME_BINARY` is unset or wrong, or Wine is missing. Wine must be 64-bit capable; Wine Staging is more reliable for UE4.

**Everything says "already running"** — that is the double-start guard. Run `bash scripts/stop-services.sh` first.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). The short version: this is a server for a client we cannot change and did not write, so **never invent data** — everything the server sends must be traceable to the client's own tables or to its binary. Read the client's parsers before forming a theory about a payload.

## Documentation

- [Contributing guide](CONTRIBUTING.md) — working rules, protocol invariants, testing
- [Client data reference](docs/client-data-reference.md) — extracted item and ship id maps, naming rules
- [Client data validation](docs/client-data-validation.md) — audit of every id the server emits

## Licence

Apache License 2.0 — see [LICENSE](LICENSE).

This covers the server code in this repository. It does **not** cover the contents of `data/`, which are extracted from the game and belong to their original owner; they are included on a fan-preservation basis. No game code or assets are distributed here — you need your own copy of Dreadnought.
