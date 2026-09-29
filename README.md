# Dreadnought Revival Project

[![GitHub](https://img.shields.io/badge/GitHub-darkace1998/Dreadnought--Revival--project-blue?logo=github)](https://github.com/darkace1998/Dreadnought-Revival-project)

A community-operated private server for the discontinued game **Dreadnought** (UE4 4.13.1, Steam App 835860). The goal is to run the **unmodified game client** against our own backend: everything is emulated server-side, and the only thing a player installs is a replacement launcher.

Operators running battle servers have one optional extra, `battle-server-mod/` — a DLL for the headless host process, not for anyone's game client. It supplies the loadouts the host cannot obtain without a login, which is what lets players spawn into a match.

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

[mmogbrain matchmaker] ──match formed──► [dn-dedicated :8085]  (control plane;
                                                │               game-manager is the fallback)
                                                ▼
                                      wine + DreadGame-Win64-Shipping.exe
                                      (one process per match, UDP 7777-7877)

[Windows server browser (dn-dedicated-browser.exe)]
        │
        │  picks a cluster from the directory (or by hand), trusts its CA,
        │  signs in — then starts the same unmodified client above pointed
        │  at that cluster (no hosts file: the game resolves no backend by name)
        ▼
[public directory: master-master :8091, admin :8092] ◄── register/heartbeat ── [dn-dedicated]
        │   cluster list + CA certs + MOTDs + player counts (for the browser)
        │   presence: who is mid-match on which cluster (one account, one match)
        │   account mirror: sync_users / sync_bans / sync_snapshots / sync_log
        ▲
        │  push local users, pull everyone else's (per cluster)
[sync-agent :8093] ── one per cluster (opt-out clusters replicate nothing)

[operator browser] ──► [web-dashboard :8090] ──loopback──► :8081-:8085 admin endpoints
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
| **master-master** | 8091, 8092 (admin) | Public cluster directory for the server browser (see [Server browser](docs/server-browser.md)) |
| **dn-dedicated** | 8085 | Spawns and monitors battle-server processes; registers the cluster with the directory (default control plane, `DN_CONTROL_PLANE=game-manager` falls back) |
| **game-manager** | 8085 | Previous battle-server spawner; fallback only, one of the two may run |
| **sync-agent** | 8093 (https) | Account roaming: pushes local users to the directory master, pulls everyone else's (see [Account roaming](#account-roaming)) |
| **web-dashboard** | 8090 | Operator web UI: health, players, queue, matches, chat, logs, metrics (see [Web dashboard](#web-dashboard)) |
| **DreadGame (Wine)** | 7777-7877/UDP | One battle server per active match |
| **admin-cli** | — | Operator CLI (`servers`, `instances`, `stop-instance`, `ban`, `unban`, `queue`, `chat`, `players`, `grant`) |
| **dn-launcher** | — | Windows launcher replacement (register / sign in / start the game) |
| **dn-server-browser** | — | Windows server browser (`dn-dedicated-browser.exe`): pick a cluster, auto-handles certs, then the normal launcher flow (see [Server browser](docs/server-browser.md)) |

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
- builds all eight services into `run/`, plus `run/dn-launcher.exe` and `run/dn-dedicated-browser.exe` (cross-compiled for Windows);
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
bash scripts/start-services.sh      # starts all eight services, then health-checks them
bash scripts/stop-services.sh
```

`start-services.sh` refuses to double-start anything already running, pins each service's `DB_PATH` so the working directory cannot decide which database is opened, and prints the listening sockets when it finishes. Logs land in `run/<service>.log`.

A healthy start ends with sockets on 80, 443, 8081-8085, 8090-8093, 48843 and 65443.

### 5. Point clients at the server

On the **server**, the certificate must cover the address clients dial. `gen-certs.sh` auto-detects it; override and regenerate when needed:

```bash
rm -rf certs/ && SERVER_IP=<your-lan-ip> bash scripts/gen-certs.sh
```

`certs/` is gitignored, so this leaves your working tree clean. Regenerating
mints a new CA, so redistribute the new `certs/ca.crt` to every client
afterwards — the old one will no longer be trusted.

<details>
<summary>Upgrading an install from before <code>certs/</code> was gitignored</summary>

They used to be tracked, so a tree that followed the instruction above has
modified tracked files and git will refuse to remove them on pull. Put yours
aside, take the change, then put them back:

```bash
cp -a certs certs.bak
git checkout -- certs        # let the pull remove the tracked copies
git pull
cp -a certs.bak/. certs/     # yours are ignored now
rm -rf certs.bak
```

Restore rather than regenerate unless you are ready to redistribute a new
`ca.crt`: a new CA invalidates every client that trusts the old one.
`server_chain.crt` is not part of this — nothing reads it and `gen-certs.sh`
does not produce it.
</details>

**Easiest: let the launcher do it -- no admin rights, no hosts file.** Put the
server's public hostname or IP in `dn-launcher.json` beside `dn-launcher.exe`,
and ship `certs/ca.crt` in the same folder:

```json
{ "server": "play.example.org" }
```

The launcher then:

- passes the server's address to the game (`-GatewayAddress`,
  `-YFirmamentAddress`). The game resolves no backend by name, so it needs no
  DNS or hosts-file change;
- sends its own sign-in straight to that address while keeping the original
  host name, so the gateway still routes it, and verifies the server
  certificate against the shipped `ca.crt`;
- installs `ca.crt` into the **current user's** Trusted Root store, once. No
  admin; Windows shows its own confirmation. The game needs it: it verifies the
  gateway and Firmament certificates.

If 443 is already taken on your router, map another outside port to the
server's 443 and add `"web_port": "8443"` (only the launcher's sign-in and
news use 443; the game's own ports are unaffected). Both settings can be built
into a distributed launcher, so testers need no `dn-launcher.json` at all:

```bash
GOOS=windows go build -ldflags "-H windowsgui \
  -X main.defaultServer=play.example.org -X main.defaultWebPort=8443 \
  -X main.defaultCA=$(base64 -w0 certs/ca.crt)" ./dn-launcher
```

The exe's icon, version information and manifest (runs without admin rights)
come from `dn-launcher/rsrc_windows_amd64.syso`, which `go build` links in
automatically. It is generated from `dn-launcher/winres/` (`winres.json` and
`icon.png`); after changing either, regenerate it:
`cd dn-launcher && go run github.com/tc-hib/go-winres@v0.3.3 make --in winres/winres.json --arch amd64`.

`-H windowsgui` opens the desktop window with no console flashing up (the
console flow opens its own console when it needs one). `defaultCA` builds the
server's CA into the exe, so testers download **only `dn-launcher.exe`**. On
first start the window shows an **Install the public testing certificate**
screen with the CA's SHA-256 fingerprint (publish it so testers can compare,
`openssl x509 -in certs/ca.crt -noout -fingerprint -sha256`); Play stays
blocked until it is installed. A `ca.crt` beside the exe overrides the built-in
one. Rebuild after regenerating the CA.

Ports to forward to the server: TCP `web_port` (-> 443), TCP 65443, TCP 48843,
UDP 7777-7877, and optionally TCP 57005 (crash reports). Never 8081-8085.
The battle-server range is `PORT_RANGE_START`/`PORT_RANGE_END` in
`run/secrets.env` (e.g. 7900/8000 when 7777+ is taken): players are sent each
match's exact port, so any free range works -- forward it unchanged.

Without those build flags, a tester's download is `dn-launcher.exe`, `dn-launcher.json` and `ca.crt`. The
server's certificates must name the address testers use:
`SERVER_IP=<public ip> bash scripts/gen-certs.sh`.

**By hand** (older launchers, or without a `server` setting), on each **client** machine:

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

**Linux players:** the game runs through Proton or Wine, and the certificate,
the sign-in token and the game's arguments must be set up inside that same
Wine/Proton prefix. `dn-launcher/linux/dn-launcher-linux.sh` therefore runs the
Windows launcher there: it finds the game in the Steam libraries (or
`DN_GAME_DIR`), uses the game's Proton prefix when Steam created one (else Wine
with `~/.local/share/dreadnought-wine`), and passes the game path as
`DN_GAME_PATH`. WebView2 is not available under Wine, so the launcher opens in
the Linux browser instead: the same page as the desktop window (sign-in, the
certificate, the game folder, options, Play), served on a random loopback port
(`browser_windows.go`). Windows PCs without WebView2, or `--console`, get the
same browser page. Ship the script with `dn-launcher.exe` and `icon.png` beside it;
`--install` adds a menu entry.

### 6. Launch the game

Run `dn-launcher.exe` on the client. It opens a **desktop window** (Microsoft
Edge WebView2, part of Windows 11 and installed with Edge on most Windows 10
PCs) with **Sign in** / **Create account**, then a home screen with the
server's news tiles (legacy-api's `/v2/dreadnought/launcher/dn/tiles/`), a
server status light and **Play**. The sign-in is kept, DPAPI-protected for that
Windows user, so the next start goes straight to the home screen.

Without WebView2 (detected before anything is created), or with `--console`,
it falls back to the older flow: a console window and the same sign-in page in
the default browser. `dn-launcher.exe --sign-out` clears the stored
credentials.

Because the account lives on the server rather than being derived from the
machine, the same login works from any PC.

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
├── dn-dedicated/    Go         -- battle-server spawner + cluster directory registration (default control plane on :8085)
├── game-manager/    Go         -- previous spawner, fallback via DN_CONTROL_PLANE=game-manager
├── sync-agent/      Go         -- account roaming: push local users, pull the rest (see below)
├── gateway/         Go         -- TLS termination and reverse proxy
├── admin-cli/       Go         -- operator CLI
├── dn-launcher/     Go         -- Windows launcher replacement (client-side)
├── dn-server-browser/ Go       -- Windows server browser, `dn-dedicated-browser.exe` (client-side)
├── master-master/   Go + SQLite -- public cluster directory for the server browser
├── web-dashboard/   Go         -- operator web UI (see below)
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
    ├── dashboard.md                operator web UI reference
    ├── server-browser.md           cluster directory + server browser reference
    ├── server-browser-testing.md   runbook: setup, testing, go-live
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
| `DN_ANSWER_DAILY_CONTRACTS` | `0` withholds the `YA_GetDailyContractsData` reply (answered by default since the quest catalogue fixed the hangar-entry recursion) |
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
| `PLAYERS_PER_MATCH` | mmogbrain | `1` | Only with `DN_MATCH_AUTOSCALE=0`: a fixed number of queued players needed to form a match. **1 gives every player a private match** — two people queueing together get two servers and never meet. Use 2+ for PvP. |
| `DN_MATCH_AUTOSCALE` | mmogbrain | on | Size matches by who is around: a match waits for every player who is **online and not in a battle**, up to `DN_MATCH_MAX_PLAYERS`, or `DN_MATCH_MAX_WAIT` after its first player queued. A lone player starts at once; two players online land in one match. `0` = the fixed `PLAYERS_PER_MATCH` |
| `DN_MATCH_MAX_PLAYERS` | mmogbrain | `10` | Largest auto-scaled match (the host's `-maxplayers`) |
| `DN_MATCH_MAX_WAIT` | mmogbrain | `60s` | How long an auto-scaled match waits for idle online players who have not queued |
| `DN_MAX_INSTANCES` | dn-dedicated | from memory | Concurrent battle servers. Default `(RAM - 2 GB) / 1.6 GB` (8 on 16 GB): each Wine host is ~1.45 GB and nine of them got a live match OOM-killed. At the cap, new matches get 503 and their players stay queued until a server frees. `0` = no cap |
| `MASTER_URL` | game-manager | `http://127.0.0.1:8084` | Master server URL |
| `MASTER_MASTER_URL` | dn-dedicated | — | Public directory URL; empty disables listing |
| `CLUSTER_NAME` | dn-dedicated | — | Browser display name |
| `CLUSTER_WEB_URL` | dn-dedicated | — | Public https URL players authenticate against |
| `CLUSTER_EMAIL` | dn-dedicated | — | **Required to list**: contact address; the operator mails the sync secret there by hand |
| `CLUSTER_AGENT_URL` | dn-dedicated | — | Public https URL of this host's sync agent (`:8093`); empty means no automatic secret delivery |
| `CLUSTER_CA_FILE` | dn-dedicated | `certs/ca.crt` | CA cert uploaded for TOFU (re-read every beat, so rotation needs no restart) |
| `CLUSTER_VERSION` / `CLUSTER_MOTD` | dn-dedicated | `1.0` / — | Shown in the browser |
| `DN_NO_MASTER_SERVER_FILE` | dn-dedicated, sync-agent | `run/dn-no-master-server.txt` | Opt-out: present means no listing **and** no account replication (no push, no pull, no presence) |
| `SYNC_MASTER_URL` | sync-agent | — | Directory master to roam with; unset means no roaming |
| `SYNC_INTERVAL` | sync-agent | `60s` | Push/pull cadence (minimum 10s) |
| `SYNC_SECRET_FILE` / `SYNC_STATE_FILE` / `SYNC_ADDR` | sync-agent | `run/sync.env` / `run/sync-state.json` / `:8093` | Secret store (0600), pull cursor, https listen address |
| `GAME_MGR_URL` | mmogbrain | `http://127.0.0.1:8085` | Game manager URL |
| `DN_FORCE_GAME_MODE` | mmogbrain | *(unset — the queued mode runs)* | Forces every match into one game mode. A mode name, or `1` for `TM`. Off by default: TM is the only mode whose host logs `no orbit spawn locations set!`, and a player in it never reaches the ship selection screen. TM is also the only mode that supplies a loadout, so the two failures are mutually exclusive — see `docs/battle-server-data-path.md` |
| `DN_CONNECT_PUSH_DELAY` | mmogbrain | `75s` (`45s` from `start-services.sh`) | **Fallback only.** How long to hold the `YA_Connect` travel push back when the control plane never reports the battle server ready. Normally the matchmaker polls `GET /instances/<id>` and pushes as soon as the engine is hosting; the push's log line records which gate opened as `gate=ready` or `gate=delay` |
| `DN_GAME_MGR_TIMEOUT` | mmogbrain | `30s` | How long the matchmaker waits for game-manager to answer `POST /instances`. The matchmaker ticks on one goroutine, so a control plane that accepts the connection and never replies would otherwise stop matchmaking for everyone. Raise it if your control plane waits for the engine to report ready before answering |
| `DN_REWARD_WIN_CREDITS` / `_LOSS_CREDITS` / `_KILL_CREDITS` | mmogbrain | `1500` / `750` / `100` | Credits per match result reported by the battle-server mod (`/battle/result`). **Placeholders** — the original reward tables are lost. A draw or unknown result pays the loss value |
| `DN_REWARD_WIN_XP` / `_LOSS_XP` / `_KILL_XP` | mmogbrain | `1000` / `500` / `50` | XP per match result, granted as free XP, as rank XP, and split over the ships the player flew. **Placeholders** |
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
- [Web dashboard](docs/dashboard.md) — operator UI: setup, routes, security
- [Server browser](docs/server-browser.md) — cluster directory + browser exe reference
- [Server browser runbook](docs/server-browser-testing.md) — setup, testing, go-live

## Web dashboard

`web-dashboard/` (port **8090**) is the operator web UI for the stack — a Go
backend-for-frontend with an embedded static frontend, so it ships as a single
binary with no Node toolchain. The browser never sees `ADMIN_KEY`: you log in
once with the key from `run/secrets.env` and get a session cookie, while all
calls to the other services go over loopback. Do not expose it to the internet
directly — reach it through an SSH tunnel (`ssh -L 8090:127.0.0.1:8090
user@server`, then `http://localhost:8090`).

What it currently does:

- **Overview** — health of every service, hero cards (queue, matches,
  instances, servers, online players), live history graphs, status-change feed
- **Online** — connected Firmament peers with queue and live-match state
- **Players** — every registered account with live balances, player drill-down
  (fleets, ships + ship XP, purchases, queue, match, results), grant (single
  and bulk to all), ban/unban with active-ban list, live test-account
  provisioning, and account reset (currencies and/or research back to fresh)
- **Queue** — waiting entries with per-player kick, clear-all and force-match
- **Matches** — battle instances (stop) plus reported match results and payouts
- **Servers** — server-browser listing from master-server
- **Chat** — channel history plus operator broadcast to all connected players
- **News** — launcher home-page tiles editor (no restart needed)
- **Logs** — tail viewer for every service log, mmogbrain's frame log and
  per-instance battle logs, with filter and follow mode
- **Metrics** — gauges scraped from every service's `/metrics` endpoint
- **Backups** — overview of the `scripts/backup.sh` archives
- **Config** — non-secret configuration, TLS certificate expiry and debug
  switches

It also required small new read-only admin endpoints on the existing services
(`GET /admin/users` and `/admin/bans` on auth-server, `/admin/results`,
`/admin/online`, `/admin/player/{id}` on mmogbrain, `/admin/tiles` on
legacy-api) plus the action endpoints above. After match end, purchases and
grants the server pushes fresh balances to connected clients, so no client
restart is needed to see them.

## Server browser

New: players pick a **cluster** (one full stack) instead of hand-configuring
a launcher. `master-master/` (ports **8091**, admin panel **8092**) is the
public cluster directory — run once, with its own `master-master/setup.sh`,
`start.sh`, `stop.sh` (it is deliberately not part of the main scripts).
Each cluster lists itself from `dn-dedicated` every 30 seconds (name, addresses,
version, MOTD, CA certificate, live player/server counts); stale clusters
vanish automatically, and `run/dn-no-master-server.txt` opts a cluster out
(manual IP join only, no restart needed either way).

`dn-server-browser/` builds `dn-dedicated-browser.exe`: cluster list with
player counts and MOTDs, manual server add for unlisted clusters, per-cluster
CA trust (fingerprint compare, one Windows confirmation on first join, then
remembered), per-cluster accounts, and the normal launcher flow into the game
— no certificate to install by hand, no JSON editing, no hosts file. Details
in [Server browser](docs/server-browser.md), setup through go-live in the
[runbook](docs/server-browser-testing.md).

> **Live directory:** a master-master server is currently running and its
> address (`http://91.51.31.83:8091`) is baked into the distributed browser
> builds (`-X main.defaultDirectory=…`), so testers see clusters without
> typing anything.

## Account roaming

Joining a new cluster used to start from zero (own `auth.db` per cluster).
With roaming, accounts — identity, balances, rank, ships, loadouts,
purchases, tech-tree progress, contracts, career claims, friends/ignores,
bans — follow the player across every cluster that syncs with the same
directory master.

How it works:

- **Hub, not mesh.** The directory (`master-master`) holds the mirror
  (`sync_users`, `sync_bans`, `sync_snapshots`, `sync_presence`, `sync_log`);
  clusters never talk to each other, and no shared key exists anywhere.
- **Per-cluster secret, human in the loop.** First registration must carry
  `contact_email` (`CLUSTER_EMAIL`). In the directory admin dashboard
  (`:8092`) you **generate** a secret (shown once — mail it to the cluster
  owner yourself), **revoke** it (sync auth dies immediately), or **send** it
  straight to the cluster's agent (generates if none exists; refuses if one
  does — revoke first to rotate). Only hashes are stored, and auto-send works
  **only over https** to `CLUSTER_AGENT_URL` — anything else is refused,
  never downgraded. The cluster keeps it in `run/sync.env` (0600), via the
  agent's `POST /sync/key` (https only, first write wins) or by hand.
- **`sync-agent`** (own module, in the normal start/stop scripts) pushes
  every local user and pulls remote changes every `SYNC_INTERVAL` (60 s).
  It roams 25 tables (2 auth + 20 mmog + 3 legacy): everything the account
  owns, including ship XP and purchases (which is what tech-tree progress
  is), contracts, career claims, friends and ignores. Needs
  `SYNC_MASTER_URL` + `CLUSTER_NAME`; without a secret it waits.
- **Merge rules.** Snapshots are last-write-wins per user on `updated_at`
  (ties break toward the lexicographically greater source, so all hosts
  agree — run NTP). Three cases merge smarter: pulls apply only strictly
  newer state (local unsynced earnings are never clobbered by a stale
  master copy); friendships are pair rows that upsert with accepted-wins;
  career-claim counts merge with MAX. Unfriending does not propagate.
- **Register once.** Create the account on any cluster; everywhere else just
  sign in with the same email and password — identity roams with everything
  else. The browser pre-checks the directory mirror (`GET /register-check`)
  and every cluster rejects a taken callsign or address with "already
  registered" (pulled accounts count: roamed identities land in the local
  user table, so the local 409 covers them). Do not create a second account
  with the same name or address: accounts are keyed by id, and two created
  inside the sync window stay two accounts.
- **Manual sync from the directory dashboard.** **Sync all now** triggers a
  cycle on every cluster with an agent URL; **roll out** copies one
  designated main cluster everywhere (forced apply, converge-not-wipe —
  accounts that exist only elsewhere are kept). If it happens anyway, the agent
  skips the duplicate on apply instead of failing the whole pull, and the
  local row wins by staying.
- **Bans are global.** A ban issued on one cluster lands on every synced
  cluster: the ban rows roam with the identity, each cluster maintains
  `banned_at` from them, and login is refused while it is set. Lifting a
  ban propagates as an empty set — the account is playable everywhere again
  after the next pull. No per-cluster re-banning, no appeal shopping.
- **One account, one match.** Every push carries each user's live state
  (active match slot or not); the browser asks `GET /presence/{user_id}`
  before enabling Play — and again at launch — and blocks with the other
  cluster's name while that account is mid-match elsewhere. Unknown (no or
  unreachable directory) lets the player through: a dead directory must not
  strand anyone.
- **Deliberately local:** login sessions, live matchmaking state, battle
  results and match history (per-cluster audit), chat, diagnostics.
- **Opt-out is total:** `run/dn-no-master-server.txt` removes the cluster
  from the listing *and* stops all replication (no push, no pull, no
  presence) — so double-play against an opted-out cluster is undetectable
  by design.

## Licence

Apache License 2.0 — see [LICENSE](LICENSE).

This covers the server code in this repository. It does **not** cover the contents of `data/`, which are extracted from the game and belong to their original owner; they are included on a fan-preservation basis. No game code or assets are distributed here — you need your own copy of Dreadnought.
