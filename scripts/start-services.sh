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
# a second client. Raise it for real play.
export PLAYERS_PER_MATCH="${PLAYERS_PER_MATCH:-1}"

# How long mmogbrain holds YA_Connect back after a match forms. YA_Connect makes
# the client travel immediately, so this has to cover the battle server's load
# time or the client arrives before it is accepting connections. Verified at 45s
# on this host; the same map reached WaitingToStart in 4s with a warm page cache
# and about a minute cold, so tune it to your hardware. It is otherwise dead
# time on the "Battle server starting" screen.
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

start() {
    local name="$1"
    shift
    if pgrep -x "$name" >/dev/null 2>&1; then
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

echo "=== Starting services ==="
start auth-server "$RUN_DIR/auth-server"
start master-server "$RUN_DIR/master-server"
start game-manager "$RUN_DIR/game-manager"
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
