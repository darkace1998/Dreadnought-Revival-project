# Server browser: runbook (setup, testing, go-live)

End-to-end runbook for the cluster directory (`master-master`), cluster
registration, and `dn-dedicated-browser.exe`. Do it top to bottom on first
install; later, the Troubleshooting table at the end covers single failures.

Conventions: repo root is `Dreadnought-Revival-project/`. `run/` is gitignored.

## 0. Prerequisites

- Linux host(s), Go 1.24+, `openssl`, `curl`, `sqlite3`:
  `sudo apt install golang openssl curl sqlite3 iproute2`
- Wine 64-bit on every host that runs battle servers (or `wine-staging`).
- Game files + `GAME_BINARY` set (see main README).
- Ports per cluster (unchanged): TCP web port (→ 443), TCP 65443, TCP 48843,
  UDP battle range (default 7777–7877).
- Directory host additionally: TCP **8091** (clusters + browsers) and TCP
  **8092** (admin panel) reachable. Prefer the panel via SSH tunnel.

## 1. Build everything

```bash
bash scripts/setup.sh
# builds all services incl. run/dn-dedicated-browser.exe (windows/amd64),
# generates certs + run/secrets.env on first run

bash master-master/setup.sh
# builds run/master-master, writes run/master-master.env (dashboard password)

cd master-master && go test ./... && go vet ./... && cd ..
cd dn-server-browser && go test ./... && cd ..
cd dn-dedicated && GOWORK=off go build ./... && GOWORK=off go test ./... && cd ..
```

Browser window resources: `dn-server-browser/winres/winres.json` reuses the
launcher icon. If you change it, regenerate before building:

```bash
cd dn-server-browser && go run github.com/tc-hib/go-winres@v0.3.3 make --in winres/winres.json --arch amd64
```

Bake an operator default directory into distributed browser builds:

```bash
cd "$PROJECT_DIR" && GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui \
  -X main.defaultDirectory=https://directory.example.org:8091" \
  -o run/dn-dedicated-browser.exe ./dn-server-browser
```

## 2. Start the directory (once, on its host)

```bash
bash master-master/start.sh
curl -s http://127.0.0.1:8091/health; echo
# -> {"service":"master-master",...}
# Admin panel: http://127.0.0.1:8092/admin (password in run/master-master.env)
```

Expected: health `ok`, panel login works, cluster list empty.

## 3. Register a cluster

In the cluster's `run/secrets.env` (exported automatically on start):

```bash
MASTER_MASTER_URL=http://<directory-host>:8091
CLUSTER_NAME="My Community Server"
CLUSTER_WEB_URL=https://<this cluster's public address>
#CLUSTER_VERSION=1.0
#CLUSTER_MOTD=Welcome!
```

`CLUSTER_WEB_URL` is the address **players** dial for auth/news (usually the
same IP/DNS the game uses). `SERVER_IP` (existing) stays the battle address.
Restart the stack:

```bash
bash scripts/stop-services.sh && sleep 3 && bash scripts/start-services.sh
```

Verify (on the directory host):

```bash
curl -s http://127.0.0.1:8091/clusters | python3 -m json.tool
# -> your cluster with players/servers counts, ca_cert + ca_fingerprint
```

In the admin panel (`:8092/admin`): row appears within ~30 s. Edit the MOTD
there and confirm it shows in the listing.

## 4. Opt-out test (unlisted cluster)

```bash
touch run/dn-no-master-server.txt
sleep 40
curl -s http://127.0.0.1:8091/clusters   # -> count 0 (or without this cluster)
rm run/dn-no-master-server.txt
sleep 40
curl -s http://127.0.0.1:8091/clusters   # -> back, no restart needed
```

## 5. Admin dashboard test

- Login with `MASTER_ADMIN_PASSWORD` from `run/master-master.env`.
- MOTD edit → visible in `GET /clusters` and in the browser.
- Block a test cluster → disappears from public list; direct re-register
  returns **403** (stays kicked).
- Unblock → back on next heartbeat (~30 s).
- Delete → row gone.

## 6. Browser test (fresh Windows VM — the real proof)

Use a VM with **no** ca.crt installed, **no** hosts entries, **no** JSON:

1. Copy only `dn-dedicated-browser.exe` to the VM (game installed via Steam).
2. Start it. Cluster list shows your cluster (name, players, MOTD).
3. Pick it → fingerprint screen. Compare SHA-256 with the directory value
   (`ca_fingerprint` from step 3, or admin panel) → confirm → **one**
   Windows confirmation → installed (current user only, no admin).
4. Register/sign in (new account lives on this cluster), Play → hangar →
   queue → match → spawn → play to end-of-match.
5. Second join: no fingerprint screen (remembered in `trusted-cas.json`).
6. Manual server: remove the cluster from the directory (opt-out file),
   then "Add server by hand" with IP + pasted `ca.crt` text → same flow.

Pass criteria: at no point `certmgr.msc`, hosts file, or JSON editing.

## 7. Gameplay verification (per cluster, via browser-joined clients)

- Two players queue → one match, opposite teams (`match_slots` 1+2).
- Battle server spawns both pawns (mod `team sync:` lines per player).
- End of match: `match result:` lines with `new=true`, credits/XP granted,
  visible after re-login at the latest (currency push covers connected
  clients).
- `run/battle-logs/` holds one log per match; no `signal: killed` without a
  preceding `stopping …` line in `run/dn-dedicated.log` (see Troubleshooting).

## Troubleshooting

| Symptom | Check |
|---|---|
| Cluster never appears in `GET /clusters` | `MASTER_MASTER_URL`/`CLUSTER_NAME`/`CLUSTER_WEB_URL` set? `dn-dedicated` log: `directory: …` lines (disabled/opt-out/heartbeat rejected?). `DN_NO_MASTER_SERVER_FILE` file present? |
| `heartbeat rejected (HTTP 4xx)` | `web_url` must be http(s) with host; `ca_cert` must be a PEM cert; name charset |
| Browser: directory unreachable | `:8091` forwarded? `curl http://<host>:8091/health` from the VM |
| Browser: fingerprint mismatch, stops | directory `ca_cert` ≠ cluster's live `certs/server.crt` (regenerated certs after registering?) → wait one beat (CA re-read every 30 s) |
| Game: TLS/connection error after Play | CA installed for *this* Windows user? Other user profile = other store. LE-cert clusters skip this entirely |
| Game: login map instead of hangar | normal without positional URL (browser joins clusters, not servers) |
| Match kills the battle server at end | see main troubleshooting: `grep -a "stopping\|battle server exited"` in `run/dn-dedicated.log`; OOM (`dmesg`, journal oomd) |
| Admin panel: 401 loop | `MASTER_ADMIN_PASSWORD` from `run/master-master.env`; Basic must reach `:8092` |
| Stale/ghost clusters in list | heartbeats stopped → auto-hidden after 120 s; force-remove via panel Delete |
| Spam cluster | panel Block (re-register → 403, stays kicked) |
