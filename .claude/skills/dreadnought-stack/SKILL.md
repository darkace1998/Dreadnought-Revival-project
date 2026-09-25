---
name: dreadnought-stack
description: Orientation for the Dreadnought private server - what runs where, how to build/deploy/restart it, where every log lives, and the human-in-the-loop test cycle. Use at the START of a session that touches this project, before running the stack, before asking the operator to test anything, and whenever you need to know what is already solved versus still open.
---

# Working on this stack

A community private server for the discontinued game **Dreadnought** (UE4
4.13.1). The client is **unmodified**; the only thing a player installs is
`dn-launcher/`. The **battle server** is moddable — same executable, run headless
by the operator (`battle-server-mod/`). That asymmetry is the project's core rule.

## The loop you will actually run

You cannot test the game yourself. **The operator runs the Windows client and
uploads logs**; you read them. So:

1. Make the change, `go test ./...` in the module, `go vet ./...`.
2. Build and deploy (below).
3. **Say exactly which log line will distinguish success from failure**, and what
   each outcome would mean. One deploy = one operator test cycle; they are the
   scarce resource, so do not spend one on a change whose result you cannot read.
4. Read the uploaded log, and label every claim **verified** (naming the
   measurement) or **theory**. This is a standing instruction from the operator.

Battle-server DLL changes are slower still: this box cannot build Windows/MSVC,
so the operator builds and copies it. Bundle host-side changes accordingly.

## Build, deploy, restart

```bash
cd /root/projects/dreadnought-private-server

go build -o run/mmogbrain ./mmogbrain          # single service
bash scripts/setup.sh                          # everything, + certs/secrets first run

bash scripts/stop-services.sh && sleep 3 && bash scripts/start-services.sh && sleep 8
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8083/health
```

`run/secrets.env` (gitignored) holds runtime config and debug switches.

**Verify the switch reached the process, not just the file** — `pgrep` right
after a restart can catch a pid that is still coming up:

```bash
pid=$(pgrep -f 'run/mmogbrain$' | head -1)
tr '\0' '\n' < /proc/$pid/environ | grep DN_
```

**Do not verify a Go build by searching the binary for a string literal.** The
compiler inlines short literals as immediate moves, so `YA_TuneReturn` never
appears contiguously; `grep` finds nothing and the build looks broken when it is
fine. Check the mtime, or search for two 8-byte halves.

## Tests

Per-module — `go test ./...` from the repo root fails.

```bash
cd mmogbrain && go test ./... && go vet ./...
cd shared    && go test ./...
cd dn-dedicated && GOWORK=off go build ./... && GOWORK=off go test ./...
```

`TestPayloadSizesVerify` pins every response's byte size as a deliberate
tripwire. When one moves, update it **with a comment explaining what grew and
why** — that comment block is a changelog of protocol findings.

## Logs — where each one really is

| What | Where |
| --- | --- |
| Client (operator uploads) | `/root/projects/Input/DreadGame.log` (older uploads: `/root/projects/DreadGame.log`) |
| Our services | `run/<service>.log` (startup, warnings) |
| **mmogbrain, every frame** | `mmogbrain.log` in mmogbrain's **working directory** = the repo root (`main.go:38`), NOT `run/`. Request names, request ids, send/defer decisions. |
| Battle server stdout | `run/dn-dedicated.log`, prefixed by instance id |
| **One battle server, whole run** | `run/battle-logs/battle-<date>-<time>-port<N>.log`, ends with `# exited at … (err: …)` |
| Battle-server mod | `dn_host_loadout.log` **beside the game exe** |

`run/mmogbrain.log` only has startup and warnings; the per-request trail is in
the root `mmogbrain.log`. With two hosts up, both write the one
`dn_host_loadout.log`, so read each host's mod lines from its `battle-logs` file.

The mod's log is NOT in `run/`. `$GAME_BINARY`'s directory
(`src/Dreadnought/DreadGame/DreadGame/Binaries/Win64/`) also holds its switch
files (`dn_host_dedicated.txt`, `dn_host_bc_ai.txt`, `dn_host_ship_physics.txt`,
`dn_host_player_loadouts.txt`, and off-switches such as `dn_host_no_eom_stats.txt`)
and `wer.dll`, which is how the mod is side-loaded. `battle-server-mod/build.bat`
writes `build.log` next to itself -- ask for that file when a build fails.

**Check the client log's timestamps before analysing it.** It is uploaded
manually and is often an older run than you assume; client clock ran 2h behind
server clock in past sessions. Correlate against `run/mmogbrain.log` timestamps
before concluding a fix did or did not take.

## Services

```
HTTPS :443    gateway ── profile-api → auth-server :8081
                     ├─ legacyapi   → legacy-api  :8082
                     └─ masterserver→ master-server :8084
HTTPS :65443  mmogbrain "gateway" socket (Greybox web services)
TLS   :48843  mmogbrain Firmament socket (binary YMmogbrain protocol)  ← the complex one
UDP   :7777+  DreadGame-Win64-Shipping.exe, one Wine process per match
              spawned by dn-dedicated :8085
```

Naming trap: the **`gateway` service** (:443 reverse proxy) and **`GATEWAY_ADDR`**
(:65443, a socket inside mmogbrain) are different things.

There is no dedicated-server build; the battle server is the client exe run
headless (`"<map>?listen" -server -nullrhi -unattended`). `-dedicatedserver` does
not exist in the binary, and dropping `-server` was measured to change nothing.

## Debug switches (`run/secrets.env` or env)

| Switch | Effect |
| --- | --- |
| `PLAYERS_PER_MATCH` | defaults to **1**, so every player gets a private match and can never meet another. Set 2+ for PvP. |
| `DN_TUNE_EMPTY=1` | send empty tuning tables (the old behaviour; empty tables break weapon lookups — see `dreadnought-mmog-responses`) |
| `DN_TECHTREE_*` | many; printed at startup by `logTechTreeSwitches()` |
| `DN_HOST_FLEET_TIER` / `DN_HOST_POSTLOGIN_SPAWN` | battle-server mod, marker file or env |
| `DN_CLAIM_ITEM_PUSH` | OFF: the push wiped the inventory to 0 items |

## A test account with everything

```bash
curl -s -X POST http://127.0.0.1:8081/auth/register -H 'Content-Type: application/json' \
  -d '{"username":"Name","email":"name@test.local","password":"..."}'   # -> {"id": "<uuid>"}
bash scripts/stop-services.sh
DB_PATH=$PWD/run/mmog.db JWT_SECRET=x run/mmogbrain provision-test-account \
  -user <uuid> [-rank 20] [-credits 200000] [-premium 200000] [-free-xp 200000]
bash scripts/start-services.sh
```

Unlocks every ship (base + hero) and owns every weapon/ability/officer perk the
server can offer, through the SAME grant path a real unlock uses -- so it
exercises the code under test. Idempotent; currency and rank are SET, not added.
Stop the stack first so the running server holds no stale view of the player.
Existing account: **UnlockAll** (password in the operator's notes, not here).

## Where the knowledge lives

- `AGENT-CHAT.md` — the running log with the client-side project. `S##` entries
  are ours, `C##` theirs. **Read the last few before starting**; it is the record
  of what was tried, what worked, and what was retracted.
- `CONTRIBUTING.md` — working rules and protocol invariants. Read before touching
  anything protocol-related.
- `docs/` — client data reference, validation, battle-server data path.
- Sibling skills: `dreadnought-mmog-responses` (response shape),
  `dreadnought-rva` (find code in the client binary), `dreadnought-hooks`
  (hooking safely), `dreadnought-verify` (proving a change worked).

## Two habits this project punishes you for skipping

**A missing log line is data.** If a function with two logged branches printed
neither, it never ran — and nothing about your payload is in question yet. Several
multi-round detours came from reading a fallback value as a rejection.

**A default indistinguishable from a real value is a hidden failure.** Log every
path, including early returns. A fix that "did nothing" three times in a row had
never once executed, and said nothing about it.
