# Server browser: clusters, directory, `dn-dedicated-browser.exe`

Players pick a **cluster** (one full stack from `scripts/start-services.sh`)
and join it. The launcher configures everything automatically: backend
addresses, per-cluster CA trust (TOFU, one Windows confirmation on first
join), sign-in, play. No certificate to install by hand, no JSON editing, no
hosts file — ever.

## Parts

| Part | What | Where |
|---|---|---|
| `master-master/` | public cluster directory (you run it) | port **8091**, `run/master-master.db` |
| `dn-dedicated` registration loop | each cluster lists itself, no key needed | `dn-dedicated/internal/directory/` |
| `dn-server-browser/` | Windows browser exe | `dn-dedicated-browser.exe` |

## Directory (`master-master`)

One public instance that every cluster knows (`MASTER_MASTER_URL`). SQLite,
same conventions as the other services (`getenv` config, logrus JSON,
`/health`, `/metrics`). **It is deliberately NOT part of the main
`setup.sh`/`start-services.sh`**: only you run it, once, with its own
scripts in `master-master/`:

```bash
bash master-master/setup.sh   # tidy + build run/master-master + run/master-master.env (dashboard password)
bash master-master/start.sh   # :8091 cluster traffic, :8092 admin panel, run/master-master.db
bash master-master/stop.sh
# Admin panel: http://<host>:8092/admin (all interfaces; prefer an SSH tunnel)
```

Routes: `POST /clusters/register` (upsert by name, **open — no key to
request**), `POST /clusters/{id}/heartbeat`, `DELETE /clusters/{id}`,
`GET /clusters` (public, online only), plus the operator dashboard under
`/admin` (HTTP Basic, `MASTER_ADMIN_PASSWORD`): full list incl. stale and
blocked, MOTD editing, block/unblock, delete.

Open registration means anyone can list a cluster — removal, not prevention,
is the moderation model: blocking a name refuses its re-registration (403),
so a kicked cluster stays kicked. A stale cluster (no heartbeat for 120 s)
vanishes on its own. `CLUSTER_KEY` does not exist: there is deliberately no
shared secret, so nobody ever needs to ask you for one.

Details per route:

- `POST /clusters/register` — upsert by name: `{name, web_url, battle_ip,
  version, motd, ca_cert, players, servers}` → `{id}`. The `ca_cert` must be
  a PEM certificate; the server computes its SHA-256 fingerprint, which is
  the TOFU anchor every browser shows before first join. Blocked names get
  403.
- `POST /clusters/{id}/heartbeat` — `{players, servers}` (refused for
  blocked ids).
- `DELETE /clusters/{id}` — graceful goodbye.
- `GET /clusters` — online clusters only (stale after 120 s without heartbeat).

## Cluster side (`dn-dedicated serve`)

Every 30 s the control plane re-registers (upsert = heartbeat). Needs, via
flags or env (all optional; unset means unlisted):

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `--master-master-url` | `MASTER_MASTER_URL` | — | directory URL; empty disables |
| `--cluster-name` | `CLUSTER_NAME` | — | browser display name |
| `--cluster-web-url` | `CLUSTER_WEB_URL` | — | public https URL players authenticate against |
| `--cluster-ca-file` | `CLUSTER_CA_FILE` | `certs/ca.crt` | CA cert uploaded for TOFU — must be the CA, not server.crt (a server cert is refused with a clear error). Re-read every beat, so rotation needs no restart. Missing file: registers without a CA (public-cert clusters). |
| `--cluster-version` | `CLUSTER_VERSION` | `1.0` | shown in the browser |
| `--cluster-motd` | `CLUSTER_MOTD` | — | shown in the browser (also editable from the dashboard) |
| `--no-master-server-file` | `DN_NO_MASTER_SERVER_FILE` | `run/dn-no-master-server.txt` | presence opts out |

`--server-ip` is reused as the battle address (it already is what clients
are handed). Player/server counts come from the live instances (mocks
excluded). On shutdown the cluster deregisters best-effort.

**Opt out:** create `run/dn-no-master-server.txt` (any content). Checked
every beat: a listed cluster deregisters at once, an unlisted one stays
silent; deleting the file re-lists. No restart either way. Unlisted clusters
are joinable only by manual IP (browser button).

## Browser (`dn-dedicated-browser.exe`)

Built from `dn-server-browser/` for Windows (`GOOS=windows go build`).
Flow per cluster, manual or listed:

1. List (or hand-entered IP) → pick a cluster.
2. First join: download its `ca_cert`, show the SHA-256 fingerprint next to
   the directory's value; on confirm, install once into the *current user's*
   Trusted Root store (Windows' own confirmation, no admin, no certmgr).
   Clusters with publicly trusted certificates skip this entirely.
3. The CA is remembered per cluster — later joins need no dialog.
4. Sign in / register on the cluster (accounts live per cluster), Play:
   the game starts with `-GatewayAddress`/`-YFirmamentAddress` pointed at
   the cluster, exactly like `dn-launcher` does today. No hosts file: the
   game resolves no backend by name.

## Browser (`dn-dedicated-browser.exe`)

Built from `dn-server-browser/` for Windows (`GOOS=windows go build`, by
`scripts/setup.sh` into `run/dn-dedicated-browser.exe`). Self-contained
alongside `dn-launcher` (adapted copies, zero changes to the launcher, so no
regression risk where no Windows toolchain is available to verify). Window
resources come from `dn-server-browser/winres/` (`winres.json` reuses the
launcher icon); generate before building:

```bash
cd dn-server-browser && go run github.com/tc-hib/go-winres@v0.3.3 make --in winres/winres.json --arch amd64
```

Build flags (like the launcher): `-H windowsgui` (no console flash),
`-X main.defaultDirectory=https://directory.example.org:8091` (baked-in
directory, overridable in the UI and saved per user).

Flow per cluster, listed or manual:

1. List (or hand-entered IP) → pick a cluster.
2. First join: download its `ca_cert`, show the SHA-256 fingerprint next to
   the directory's value (they must match — a mismatch stops); on confirm,
   install once into the *current user's* Trusted Root store and remember
   the approval in `trusted-cas.json` (re-approval only if the cert
   changes). Clusters with publicly trusted certificates skip this: the
   browser probes with system roots first.
3. The remembered approval is per cluster id (listed) or URL (manual).
   Manual servers additionally need the `ca.crt` text pasted once (stored in
   `browser.json`); without a CA there is nothing the game could trust.
4. Sign in / register on the cluster (credentials stored per cluster,
   DPAPI-encrypted, like `dn-launcher`). Accounts live per cluster.
5. Play: the game starts with `-GatewayAddress`/`-YFirmamentAddress` pointed
   at the cluster — the normal flow, no hosts file (the game resolves no
   backend by name).

`--console` serves the same page in the default browser (loopback API with a
per-run key, like the launcher). `--sign-out` forgets all saved sign-ins.
Game folder, log toggles and Steam lookup share `dn-launcher`'s settings
file, so choosing the folder once counts for both programs.

## Ports & firewall

Per cluster, unchanged: TCP `web_port` (→ 443), TCP 65443, TCP 48843, UDP
battle range. Plus the one public directory: TCP **8091** inbound for clusters and
browsers, and TCP **8092** for the admin panel (both on all interfaces;
firewall the panel or reach it via SSH — Basic over plain HTTP belongs on
loopback).

## Testing it

```bash
# 1. directory + two clusters (different CLUSTER_NAME / ports / DBs)
# 2. fresh Windows VM: no cert, no hosts entry, no JSON
# 3. browser → cluster appears → join → fingerprint confirm → play
# 4. touch run/dn-no-master-server.txt → cluster vanishes within ~2 min,
#    manual IP join still works
```
