#!/usr/bin/env bash
# start-services.sh -- start the whole server stack.
#
# This lived in run/ for a long time, which is in .gitignore, so a fresh clone
# had no way to start anything. It is here now so it ships with the repo.
#
# All six services matter. Starting only auth-server, gateway, mmogbrain and
# legacy-api is enough to reach the hangar but silently breaks matchmaking:
# mmogbrain's matchmaker POSTs a formed match to game-manager at GAME_MGR_URL,
# and when nothing is listening the request fails, formMatch rolls the queue
# entries back to 'waiting', and the player sits in the queue forever with no
# error. master-server and game-manager have to be up for a match to reach a
# real battle server.
#
# Secrets come from run/secrets.env (see scripts/secrets.env.example), not from
# this file. game-manager and master-server authenticate to each other with
# INTERNAL_API_KEY, falling back to ADMIN_KEY.
set -u
cd "$(dirname "$0")/.." || exit 1
ROOT="$PWD"
RUN_DIR="$ROOT/run"

if [ ! -d "$RUN_DIR" ]; then
    echo "No run/ directory -- run scripts/setup.sh first." >&2
    exit 1
fi

if [ -f "$RUN_DIR/secrets.env" ]; then
    set -a
    # shellcheck disable=SC1091
    . "$RUN_DIR/secrets.env"
    set +a
fi
: "${JWT_SECRET:?run/secrets.env must define JWT_SECRET (see scripts/secrets.env.example)}"

# SERVER_IP is the address CLIENTS are handed for a battle server, so it has to
# be this host's LAN address, not loopback. It must also match the IP the TLS
# certificate was generated for; see scripts/gen-certs.sh.
if [ -z "${SERVER_IP:-}" ]; then
    SERVER_IP="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{print $7; exit}')"
    SERVER_IP="${SERVER_IP:-127.0.0.1}"
fi
export SERVER_IP

# mmogbrain serves three sockets: ADDR is its plain HTTP health/admin port,
# while GATEWAY_ADDR and FIRMAMENT_ADDR are the TLS sockets the game client
# itself dials. Note GATEWAY_ADDR belongs to mmogbrain, NOT to the gateway
# service -- the gateway is the reverse proxy on HTTP_ADDR/HTTPS_ADDR.
export FIRMAMENT_ADDR="${FIRMAMENT_ADDR:-:48843}"
export GATEWAY_ADDR="${GATEWAY_ADDR:-:65443}"
export GATEWAY_CERT="${GATEWAY_CERT:-$ROOT/certs/server.crt}"
export GATEWAY_KEY="${GATEWAY_KEY:-$ROOT/certs/server.key}"
# The firmament socket gets its OWN certificate, not server.crt: the client pins
# that connection, and gen-certs.sh builds certs/firmament.* with an
# "Amazon RSA 2048 M01" issuer specifically to satisfy the pin. Falling back to
# server.crt here would make the social/presence connection fail on a fresh
# install for a reason nothing in the log explains.
export FIRMAMENT_CERT="${FIRMAMENT_CERT:-$ROOT/certs/firmament.crt}"
export FIRMAMENT_KEY="${FIRMAMENT_KEY:-$ROOT/certs/firmament.key}"

export MASTER_URL="${MASTER_URL:-http://127.0.0.1:8084}"
export GAME_MGR_URL="${GAME_MGR_URL:-http://127.0.0.1:8085}"
export INTERNAL_API_KEY="${INTERNAL_API_KEY:-${ADMIN_KEY:-}}"
export WINE_EXE="${WINE_EXE:-wine}"

# The battle server is the CLIENT executable run headless -- there is no
# separate dedicated-server build. See game-manager/spawner for the argv.
export GAME_BINARY="${GAME_BINARY:-}"

# Sourcing secrets.env under `set -a` runs it as shell, so backslashes in an
# unquoted value are escape characters and get eaten: a Windows path like
#   GAME_BINARY=D:\Dreadnought\...\DreadGame-Win64-Shipping.exe
# arrives as D:Dreadnought...  Reported from a clean install, where it cost a
# whole session because the spawn failure was silently recorded as a mock.
# Quote the value in secrets.env, or use forward slashes (Go accepts them on
# Windows). Checking here makes the mistake visible at startup either way.
if [ -n "$GAME_BINARY" ] && [ ! -e "$GAME_BINARY" ]; then
    echo "WARNING: GAME_BINARY does not exist: $GAME_BINARY" >&2
    case "$GAME_BINARY" in
        *\\*) : ;;
        *:*) echo "         Looks like a Windows path with its backslashes stripped." >&2
             echo "         Quote it in run/secrets.env, or use forward slashes." >&2 ;;
    esac
    echo "         Battle servers will fail to spawn and matches will not form." >&2
fi

# The spawned battle server needs a CONFIGURED Wine prefix, not a fresh one:
# with an empty prefix wine cannot start the game and the instance exits within
# seconds (status 3). Point this at the same prefix the client harness uses.
# spawner.battleServerEnv also supplies the software-GL defaults the shipping
# build needs under Wine with no GPU, so they do not need setting here.
export GAME_WINEPREFIX="${GAME_WINEPREFIX:-$HOME/.wine}"

# The battle server embeds CEF, which aborts the whole process when it has no X
# display, so a headless box still needs one. Start Xvfb on demand and hand the
# display to game-manager; GAME_DISPLAY lets an operator point at their own.
export GAME_DISPLAY="${GAME_DISPLAY:-${DISPLAY:-:99}}"
if [ "$GAME_DISPLAY" = ":99" ] && ! pgrep -f "Xvfb :99" >/dev/null 2>&1; then
    if command -v Xvfb >/dev/null 2>&1; then
        Xvfb :99 -screen 0 1280x800x24 >"$RUN_DIR/xvfb.log" 2>&1 &
        sleep 1
        echo "started Xvfb on :99 for the battle server"
    else
        echo "WARNING: Xvfb is not installed; battle servers will crash in libcef.dll" >&2
    fi
fi

# One player forms a match, so a single tester can reach a battle server without
# a second client.
#
# THE COST, now that players can actually spawn: a match forms the instant
# anyone queues, so two people queueing together get two SEPARATE battle servers
# and can never meet. In a PvP mode that is an empty map -- and PvP modes have no
# bots to fill it; across our host logs AYAICombatSceneManager::StartCombat fires
# only under Training Match. "There is nothing to do in matches" (AGENT-CHAT
# C25.6) is this setting plus a PvP mode, not a missing backend feature.
#
# Set PLAYERS_PER_MATCH=2 (or more) for anything other than solo testing.
export PLAYERS_PER_MATCH="${PLAYERS_PER_MATCH:-1}"

# FALLBACK for how long mmogbrain holds YA_Connect back after a match forms.
#
# The normal path no longer uses it: the matchmaker polls the control plane's
# GET /instances/<id> and pushes YA_Connect as soon as the battle server reports
# it is hosting, which is usually a few seconds. This value only applies when
# that readiness never arrives -- an older game-manager, a control plane without
# the route, or an instance whose engine never printed the hosting line.
#
# It still has to cover the battle server's load time, because YA_Connect makes
# the client travel immediately. Verified at 45s on this host; the same map
# reached WaitingToStart in 4s with a warm page cache and about a minute cold.
# Which gate opened is in the mmogbrain log line for the push, as gate=ready or
# gate=delay.
export DN_CONNECT_PUSH_DELAY="${DN_CONNECT_PUSH_DELAY:-45s}"

# Each service defaults DB_PATH to a bare filename, so which database it opens
# depends on the working directory. Starting from the repo root and starting
# from run/ therefore used two different sets of files, and they had diverged.
# Pin them explicitly so the cwd cannot decide.
db_for() {
    case "$1" in
    auth-server) echo "$RUN_DIR/auth.db" ;;
    legacy-api) echo "$RUN_DIR/legacy.db" ;;
    master-server) echo "$RUN_DIR/master.db" ;;
    mmogbrain) echo "$RUN_DIR/mmog.db" ;;
    *) echo "" ;;
    esac
}

# ---------------------------------------------------------------- process check
#
# pgrep and pkill DO NOT EXIST in Git Bash / MSYS. `if pgrep -x "$name"` there is
# not a failed match, it is `command not found` -- non-zero -- so every caller
# takes the else branch. The damage is not theoretical: stop-services.sh reported
# every service as "not running" and stopped none, and then start() decided
# nothing was running and started a SECOND copy. Three gateway processes
# accumulated across three runs, the first holding the port, and an operator
# spent an hour measuring a stale mmogbrain that predated the environment
# variable they were testing (AGENT-CHAT C13.5).
#
# So: pidfile first, because it is portable and exact; then pgrep; then tasklist
# on Windows. If none of them can answer, say so rather than guessing "not
# running", which is the answer that starts duplicates.
service_pid() {
    local name="$1" pidfile="$RUN_DIR/$1.pid"
    [ -f "$pidfile" ] || return 1
    local pid
    pid="$(cat "$pidfile" 2>/dev/null)"
    [ -n "$pid" ] || return 1
    kill -0 "$pid" 2>/dev/null || return 1
    echo "$pid"
}

service_running() {
    local name="$1"
    service_pid "$name" >/dev/null && return 0
    if command -v pgrep >/dev/null 2>&1; then
        pgrep -x "$name" >/dev/null 2>&1 && return 0
        return 1
    fi
    if command -v tasklist >/dev/null 2>&1; then
        tasklist /FI "IMAGENAME eq $name.exe" 2>/dev/null | grep -qi "$name" && return 0
        return 1
    fi
    echo "WARNING: cannot tell whether $name is running (no pidfile, no pgrep, no tasklist)." >&2
    return 1
}

start() {
    local name="$1"
    shift
    if service_running "$name"; then
        echo "already running: $name"
        return
    fi
    if [ ! -x "$RUN_DIR/$name" ]; then
        echo "missing binary: run/$name -- run scripts/setup.sh" >&2
        return
    fi
    # NOTE: run/<name>.log only captures stdout/stderr. mmogbrain ALSO opens its
    # own "mmogbrain.log" relative to the working directory (main.go), and that
    # is where the detailed JSON request log goes -- so the mmog frame log lives
    # at <repo>/mmogbrain.log, not run/mmogbrain.log. Grep the former when
    # tracing client requests.
    DB_PATH="$(db_for "$name")" "$@" >>"$RUN_DIR/$name.log" 2>&1 &
    echo $! >"$RUN_DIR/$name.pid"
    echo "started $name $(cat "$RUN_DIR/$name.pid")"
}

# The control plane is what mmogbrain's matchmaker POSTs a formed match to, and
# what actually spawns the battle server. dn-dedicated replaced game-manager:
# same routes, same argv, plus per-instance readiness (GET /instances/{id}
# reports "ready"), which is what lets the YA_Connect travel push fire when the
# server is genuinely hosting instead of after DN_CONNECT_PUSH_DELAY.
#
# Both listen on :8085 and only one may run. Set DN_CONTROL_PLANE=game-manager
# to go back; the old service is still built and still works, it just makes
# every match wait out the fixed delay.
start_control_plane() {
    local choice="${DN_CONTROL_PLANE:-dn-dedicated}"
    if [ "$choice" = "dn-dedicated" ] && [ ! -x "$RUN_DIR/dn-dedicated" ]; then
        echo "run/dn-dedicated is missing -- falling back to game-manager (run scripts/setup.sh)" >&2
        choice="game-manager"
    fi
    if [ "$choice" = "game-manager" ]; then
        start game-manager "$RUN_DIR/game-manager"
        return
    fi
    # serve takes its game binary, ports and key from the same environment
    # game-manager reads, so nothing else here changes. --register puts matches
    # in the server browser, which game-manager did unconditionally.
    start dn-dedicated "$RUN_DIR/dn-dedicated" serve \
        --addr ":8085" \
        --engine-log-cmds "$DN_ENGINE_LOG_CMDS" \
        ${DN_MAP_URL_OPTION:+--url-option "$DN_MAP_URL_OPTION"} \
        --server-ip "$SERVER_IP" \
        --game-binary "$GAME_BINARY" \
        --register \
        --master-url "$MASTER_URL" \
        --log-dir "$RUN_DIR/battle-logs"
}

# Engine log verbosity for battle servers, passed through as -LogCmds.
#
# On while the spawn path is being reverse-engineered: the battle server writes
# no log of its own, so the only record of why a player could not spawn is the
# stream dn-dedicated captures, and the lines that explain it -- the loadout
# manager's contents and the id it failed to find -- are Verbose.
#
# LogYComVOComponent is held at Log on purpose. Above Verbose the process
# crashes in UYComVOComponent::PlayVoiceLineInternal (it logs two UObject names
# before validating them), so "global verbose" must exempt it. Set
# DN_ENGINE_LOG_CMDS= (empty) to turn the whole thing off.
export DN_ENGINE_LOG_CMDS="${DN_ENGINE_LOG_CMDS-global verbose, LogYLoadout veryverbose, LogYComVOComponent log}"

# An extra map URL option for every battle server, e.g.
# DN_MAP_URL_OPTION="ylevelvariation=1".
#
# A map's game-mode sublevels are streamed by index, and the orbit manager takes
# its spawn points from the streamed sublevel: a TM match on Highlands loads
# variation 0 and then logs "ActivateBattlePlayerStarts: no orbit spawn
# locations set!", because MP_Highlands_TM is a different variation. The option
# is real and is honoured (verified live at 1, 2, 3 and 9), but WHICH index is
# TM is not yet known -- the map declares Territory, TM and Onslaught in that
# order, and the index only shows its effect with a client attached. Left unset
# until a client test identifies it.
export DN_MAP_URL_OPTION="${DN_MAP_URL_OPTION:-}"

echo "=== Starting services ==="
start auth-server "$RUN_DIR/auth-server"
start master-server "$RUN_DIR/master-server"
start_control_plane
TLS_CERT="$ROOT/certs/server.crt" TLS_KEY="$ROOT/certs/server.key" \
    CRASH_REPORT_DIR="$RUN_DIR/crash-reports" start gateway "$RUN_DIR/gateway"
start mmogbrain "$RUN_DIR/mmogbrain"
start legacy-api "$RUN_DIR/legacy-api"

sleep 3
echo "=== Health checks ==="
for p in 8081 8082 8083 8084 8085; do
    printf ':%s ' "$p"
    curl -s -m 2 "http://127.0.0.1:$p/health" || printf 'no response'
    echo
done
echo "=== Listening sockets ==="
ss -lntp 2>/dev/null | grep -E ':(80|443|8081|8082|8083|8084|8085|48843|65443)\b' || true
echo "=== Done (PLAYERS_PER_MATCH=$PLAYERS_PER_MATCH SERVER_IP=$SERVER_IP) ==="
if [ "$PLAYERS_PER_MATCH" = "1" ]; then
    echo "    NOTE: PLAYERS_PER_MATCH=1 gives every player a PRIVATE match."
    echo "          Two people queueing together get two servers and never meet."
    echo "          Export PLAYERS_PER_MATCH=2 before this script for PvP."
fi
