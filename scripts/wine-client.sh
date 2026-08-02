#!/usr/bin/env bash
# wine-client.sh -- run the game client against this server, on this Linux box.
#
# This is the A/B test loop: change a response, restart mmogbrain, run this, read
# the log. It removes the "wait for the user to test on Windows" round trip that
# shaped most of this project's history.
#
#   bash scripts/wine-client.sh [seconds] [output-log]
#
# What it does, in order:
#   1. runs dn-launcher.exe with DN_PLAYER_ID set, which authenticates against
#      auth-server and DPAPI-writes the token into the Wine registry, then kills
#      the client the launcher spawns (we want our own argv);
#   2. launches the client itself;
#   3. dismisses the title screen with xdotool;
#   4. lets it sit in the hangar for the requested time, then kills it.
#
# CPU is the thing to be careful about here: rendering is llvmpipe on the CPU,
# and an unconstrained run has taken this box down before. The client is pinned
# to three cores at nice 19 with a hard timeout, which has been safe on a 12-core
# host with the whole server stack running. Do not remove those.
#
# Requirements: Xvfb on :99, xdotool, a configured Wine prefix, and
# dn-launcher.json in the game's install root pointing at this server.
set -u

SECS="${1:-190}"
OUT="${2:-/tmp/dn-wine-client.log}"

: "${GAME_ROOT:=/root/projects/src/Dreadnought}"
: "${GAME_BIN_DIR:=$GAME_ROOT/DreadGame/DreadGame/Binaries/Win64}"
: "${WINEPREFIX:=/root/.wine}"
: "${DISPLAY:=:99}"
# A stable id keeps the same test account (and its ships) across runs. The
# launcher auto-registers it on first sight.
: "${DN_PLAYER_ID:=claude-test-01}"
: "${CLIENT_CPUS:=4,5,6}"

export WINEPREFIX DISPLAY DN_PLAYER_ID
export WINEDEBUG=-all
# No GPU here: the shipping build hard-requires DX11, which works through
# software GL. Without these it page-faults during RHI init. -opengl does not
# work, and -nullrhi boots but creates no window, so the title screen can never
# be dismissed.
export LIBGL_ALWAYS_SOFTWARE=1 GALLIUM_DRIVER=llvmpipe
export MESA_GL_VERSION_OVERRIDE=4.5 MESA_GLSL_VERSION_OVERRIDE=450

if ! pgrep -f "Xvfb $DISPLAY" >/dev/null 2>&1; then
    echo "Xvfb is not running on $DISPLAY -- start it first (scripts/start-services.sh does)." >&2
    exit 1
fi

kill_clients() {
    # By PID from the process table, never `pkill -f`: the pattern would match
    # this script's own command line and kill the shell running it. `pgrep -x`
    # does not work either -- the process name is truncated to 15 characters.
    for p in $(ps -eo pid,comm | awk '$2 ~ /^DreadGame/ {print $1}'); do
        kill -9 "$p" 2>/dev/null
    done
}

echo "[harness] minting a fresh auth token as $DN_PLAYER_ID"
(cd "$GAME_ROOT" && taskset -c "$CLIENT_CPUS" nice -n 19 timeout -s KILL 90 \
    wine ./dn-launcher.exe 2>&1 | grep -E "Authenticated|token written|\[!\]")
sleep 2
kill_clients   # the launcher starts the game itself; we want our own argv
sleep 2

echo "[harness] launching the client for ${SECS}s -> $OUT"
cd "$GAME_BIN_DIR" || exit 1
taskset -c "$CLIENT_CPUS" nice -n 19 timeout -s KILL "$SECS" \
    wine ./DreadGame-Win64-Shipping.exe \
    -GatewayAddress=127.0.0.1 -GatewayPort=65443 \
    -YFirmamentAddress=127.0.0.1 -YFirmamentPort=48843 \
    -LOG -AllowStdOutLogVerbosity \
    -noeac -NoSteam -nosound -windowed -ResX=1024 -ResY=576 \
    "-LogCmds=global verbose, LogYComVOComponent log" -forcelogflush \
    >"$OUT" 2>&1 &
GAME=$!

# The client boots to a "press any key" title screen and will not talk to the
# servers until it gets input. Two windows exist; the one to drive is named
# "DreadGame" and the other carries the full exe path, so exclude any name with
# a backslash in it.
WID=""
for _ in $(seq 1 60); do
    sleep 2
    WID=$(xdotool search --name "^DreadGame" 2>/dev/null | while read -r w; do
            n=$(xdotool getwindowname "$w" 2>/dev/null)
            case "$n" in *\\*) ;; *) echo "$w"; break ;; esac
          done)
    [ -n "$WID" ] && break
done
if [ -n "$WID" ]; then
    sleep 25   # let it finish loading before the keypress
    xdotool windowactivate "$WID" windowfocus "$WID" 2>/dev/null
    xdotool key --window "$WID" space 2>/dev/null
    sleep 2
    xdotool key --window "$WID" Return 2>/dev/null
    echo "[harness] dismissed the title screen (window $WID)"
else
    echo "[harness] no game window appeared -- check $OUT" >&2
fi

wait $GAME 2>/dev/null
kill_clients
echo "[harness] done. $(grep -c . "$OUT") lines in $OUT"
echo "[harness] the client writes NO log file of its own under Wine; stdout is all there is,"
echo "[harness] and it is capped at Log verbosity even with -LogCmds."
