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
request**, but `contact_email` is required), `POST /clusters/{id}/heartbeat`,
`DELETE /clusters/{id}`, `GET /clusters` (public, online only), plus the
operator dashboard under `/admin` (HTTP Basic, `MASTER_ADMIN_PASSWORD`):
full list incl. stale and blocked, MOTD editing, **secret generate/revoke/
send-to-cluster (https only)**, block/unblock, delete — plus user mirror
(search, balances, ban state) and the sync audit log (every communication
logged, both directions).

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
| `--cluster-email` | `CLUSTER_EMAIL` | — | **required to list**: contact address; the operator mails the sync secret there by hand |
| `--cluster-agent-url` | `CLUSTER_AGENT_URL` | — | public https URL of this host's sync agent (`:8093`); empty means no automatic secret delivery |
| `--cluster-ca-file` | `CLUSTER_CA_FILE` | `certs/ca.crt` | CA cert uploaded for TOFU — must be the CA, not server.crt (a server cert is refused with a clear error). Re-read every beat, so rotation needs no restart. Missing file: registers without a CA (public-cert clusters). |
| `--cluster-version` | `CLUSTER_VERSION` | `1.0` | shown in the browser |
| `--cluster-motd` | `CLUSTER_MOTD` | — | shown in the browser (also editable from the dashboard) |
| `--no-master-server-file` | `DN_NO_MASTER_SERVER_FILE` | `run/dn-no-master-server.txt` | presence opts out |

`--server-ip` is reused as the battle address (it already is what clients
are handed). Player/server counts come from the live instances (mocks
excluded). On shutdown the cluster deregisters best-effort.

**Opt out:** create `run/dn-no-master-server.txt` (any content). Checked
every beat and every sync interval: a listed cluster deregisters at once, an
unlisted one stays silent; deleting the file re-lists. No restart either way.
Unlisted clusters are joinable only by manual IP (browser button). Opt-out is
total: no listing AND no account replication — the sync agent sends nothing
and applies nothing while the file exists (so no roaming in or out, and no
presence either: double-play against an opted-out cluster is undetectable by
design — the operator chose invisibility).

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

## Account roaming (stage 3): one account everywhere

Joining a new cluster used to start from zero (own `auth.db` per cluster).
With roaming, accounts — identity, balances, rank, ships, loadouts,
purchases, contracts, career claims, friends/ignores, bans — follow the
player. Design notes:

- **Hub, not mesh.** The directory (`master-master`) holds the mirror
  (`sync_users`, `sync_bans`, `sync_snapshots`, `sync_log`); clusters never
  talk to each other. No shared key exists anywhere.
- **Per-cluster secret, human in the loop.** First registration must carry
  `contact_email` (`CLUSTER_EMAIL`). In the admin dashboard you
  **generate** a secret (shown once — mail it yourself), **revoke** it
  (sync auth dies immediately), or **send** it straight to the cluster
  (generates if none exists; refuses if one does — revoke first to rotate).
  Only hashes are stored. Auto-send works **only over https** to the
  cluster's agent URL (`CLUSTER_AGENT_URL`, e.g. `https://play.example.org:8093`);
  anything else is refused, never downgraded.
- **The cluster stores it** in `run/sync.env` (0600), either via the agent's
  `POST /sync/key` endpoint (https only, first write wins — a stored secret
  is never overwritten remotely) or by hand + agent restart.
- **`sync-agent` binary** (own module, in the normal start/stop scripts):
  every `SYNC_INTERVAL` (60 s) it pushes every local user and pulls remote
  changes; needs `SYNC_MASTER_URL` + `CLUSTER_NAME`, waits without a secret.
  Listens `:8093` https (cluster certs) for key receive + `/health`.
- **Merge rule: last-write-wins per user** on `updated_at` (ties break toward
  the lexicographically greater source, so all hosts agree). Resets
  propagate naturally (emptied state replaces). Clock skew corrupts this —
  run NTP. True simultaneous play is refused up front by the presence guard
  above; the remaining edge (opt-out clusters, dead directory) can still lose
  one side's additive grants — the common case (one cluster at a time) is
  exact.
- **One account, one match.** Every push carries each user's live state
  (active match slot or not); the directory keeps it as presence
  (`sync_presence`, fresh for 120 s — twice the push interval, so one missed
  beat doesn't clear anyone). The browser asks `GET /presence/{user_id}`
  (public, like the cluster list) before enabling Play — and again at launch
  moment — and blocks with the other cluster's name when that account is
  mid-match elsewhere: *"You're already connected to a match on 'X'. Finish
  or leave it there first."* Unknown (no/unreachable directory) lets the
  player through — a dead directory must not strand anyone; the guard is
  best-effort, not a lock. Manual servers exclude by cluster name (no id).
- **Two tables merge smarter than last-write-wins.** Friendships are pair
  rows (two owners): they upsert with accepted-wins — accepted on either
  side, or cross-requested, settles the pair everywhere; one side's sync
  never deletes the other's row. Career claims (`claimed_stages`, monotonic)
  merge per key with MAX, so claiming on two clusters between syncs adds up
  instead of reverting. Unfriending does not propagate (no delete tombstone).
- **Not synced, on purpose:** sessions (login tokens stay local),
  queue/matches/slots (live matchmaking), battle results + match history
  (per-cluster audit), chat.
- **Visibility:** admin dashboard shows mirrored users (search, balances,
  ban state) and the full sync audit log (every push/pull/denial, both
  directions). Applied mid-session changes show after re-login at the
  latest (connected clients learn balances at `YA_PlayerGet` / match end).
