# Web dashboard (`web-dashboard`)

Operator web UI for the stack: health overview of every service, players,
matchmaking queue, battle instances, server browser, chat, logs, metrics and
config — dark glass look, with live graphs (canvas, 5s polling).

**Principle: backend-for-frontend, Go only, no Node.** The browser never sees
`ADMIN_KEY`/`INTERNAL_API_KEY`: one login with the admin key, then an
HttpOnly session cookie; the dashboard server makes all upstream calls
(`/health`, `/metrics`, `/admin/*`, `/servers`, `/instances`) over loopback.
The UI (`web/`) is embedded into the binary via `go:embed` — one file, no
build step.

## Commands on Linux (server host)

Everything from the repo root (`Dreadnought-Revival-project/`). Go 1.24+ required.

```bash
# 1. One-off: check toolchain + build everything (incl. dashboard) + secrets
bash scripts/setup.sh
#    Also handles: go mod tidy for web-dashboard (go.sum/go.work.sum),
#    certificates, run/secrets.env with JWT_SECRET + ADMIN_KEY.

# 2. Build only the dashboard (single binary into run/)
go build -o run/web-dashboard ./web-dashboard

# 3. Test + vet only the dashboard (mandatory before every commit, per module!)
cd web-dashboard && go test ./... && go vet ./... && cd ..

# 4. Start the whole stack (dashboard runs on :8090)
bash scripts/start-services.sh

# 5. Verify
curl -s http://127.0.0.1:8090/health; echo
curl -s http://127.0.0.1:8090/api/me -H "X-Admin-Key: $(grep ADMIN_KEY run/secrets.env | cut -d= -f2)"; echo

# 6. Open in browser (via SSH tunnel from your own machine):
#    ssh -L 8090:127.0.0.1:8090 user@server
#    → http://localhost:8090  (login with ADMIN_KEY from run/secrets.env)

# 7. Stop
bash scripts/stop-services.sh
```

### Environment variables (from `run/secrets.env` + `start-services.sh`)

| Variable | Default | Purpose |
|---|---|---|
| `ADMIN_KEY` | — (required) | Login key + upstream auth for `/admin/*` |
| `INTERNAL_API_KEY` | `ADMIN_KEY` | Upstream auth for `DELETE /instances` |
| `DASHBOARD_ADDR` / `ADDR` | `:8090` | Dashboard listen address |
| `RUN_DIR` | `run` | Where logs/DBs are looked up (set by the start script) |
| `AUTH_URL` / `LEGACY_API_URL` / `MMOG_URL` / `MASTER_URL` / `GAME_MGR_URL` | `http://127.0.0.1:8081`…`:8085` | Upstream base URLs |
| `TLS_CERT` | `certs/server.crt` | For the cert-expiry display in the Config tab |

### API routes (every `/api/*` except login needs a session or `X-Admin-Key`)

Reading: `GET /api/status` (aggregate of every `/health` + queue/matches/instances/online),
`/api/config`, `/api/queue`, `/api/players`, `/api/accounts` (every
**registered** account from auth-server with live balance from mmogbrain —
including fresh accounts with no game data), `/api/player/{id}` (details:
fleets, ships + ship XP, purchases, queue, live match, results),
`/api/online` (connected Firmament peers with queue/match status),
`/api/instances`, `/api/instance/{id}`, `/api/servers`, `/api/chat?channel=global`,
`/api/results` (reported match results + payouts),
`/api/matches` + `/api/match/{id}` (match rows with slots for the match center),
`/api/catalog` (buyable hulls/heroes + prices + owner counts),
`/api/history` (legacy match archive with rosters),
`/api/sessions` + `DELETE /api/sessions/{id}` (login sessions, revoke),
`/api/player/{id}/progress` (career goals, seasons, contracts, counters),
`/api/bans` (active bans), `/api/tiles` (launcher news),
`/api/logs?name=…&lines=200` (allowlist, incl. `mmog-frames` = repo-root log
and `battle-logs`), `/api/metrics-summary`, `/api/backups` (archives from
`scripts/backup.sh`, read-only), `/api/crashes` (client crash reports: list +
text view) and `/api/audit` (dashboard action log).
Writing (with frontend confirm): `POST /api/grant` (credits/premium/free-XP
to one player), `POST /api/grant-all` (same amounts to **every** account
with game data), `POST /api/provision` (equip test account live, values are
set), `/api/ban`, `/api/unban`, `POST /api/broadcast`
(operator message, live + history), `POST /api/tiles` + `DELETE
/api/tiles/{id}` (launcher news), `DELETE /api/queue/kick/{entry}` + `POST
/api/queue/clear` + `POST /api/force-match` (queue control), `POST /api/reset`
(account back to fresh: reset currencies and/or research),
`POST /api/stop-instance/{id}`. Plus `POST /api/login|logout`, `GET /api/me`,
`GET /health`, `GET /metrics` (promhttp, like every service).

New backend endpoints for it: auth `GET /admin/users`, `GET /admin/bans`;
mmogbrain `GET /admin/results|online|player/{id}`, `POST
/admin/broadcast|provision|reset|queue/clear|force-match`, `DELETE
/admin/queue/kick/{entry}`; legacy `GET|POST /admin/tiles`, `DELETE
/admin/tiles/{id}`. After match end, purchases and grants the server pushes
fresh balances (`YA_RewardCurrencies`) to connected clients — no relog needed.

### Security notes

- Do **not** expose the dashboard directly to the internet — use an SSH tunnel
  or put it behind the gateway as an internal host (like `gamemanager.local`).
- Logs are tailed read-only (max. 2000 lines), paths come from an allowlist —
  no request can open an arbitrary path.
- IDs are validated (instances = UUID, usernames = `admin-cli` rule,
  grants only 32-hex + amounts 0…1e9).
