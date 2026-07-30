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

# One player forms a match, so a single tester can reach a battle server without
# a second client. Raise it for real play.
export PLAYERS_PER_MATCH="${PLAYERS_PER_MATCH:-1}"

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
