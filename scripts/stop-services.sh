#!/usr/bin/env bash
# stop-services.sh -- stop the whole stack.
#
# Matches on the exact process NAME rather than a pattern. `pkill -f <pattern>`
# kills your own shell here, because the bash -c command line that runs the
# script contains the pattern.
set -u
cd "$(dirname "$0")/.." || exit 1
RUN_DIR="$PWD/run"

# Both control planes are listed: which one is running depends on
# DN_CONTROL_PLANE, and stopping only the one this script guessed would leave
# :8085 occupied and the next start silently talking to a stale process.
#
# The battle servers themselves are NOT killed here. They are children of the
# control plane, which stops them on shutdown; killing them by name from a
# script that also matches the harness's own client is how the LXC has been
# wedged before.
 # See start-services.sh for why this cannot just call pgrep/pkill: they do not
# exist in Git Bash, where `if pgrep ...` is `command not found` and every
# service was reported "not running" while all six kept their ports.
stop_service() {
    local name="$1" pid stopped=0
    pid="$(cat "$RUN_DIR/$name.pid" 2>/dev/null)"
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null && stopped=1
    fi
    if [ "$stopped" = 0 ] && command -v pkill >/dev/null 2>&1; then
        pkill -x "$name" 2>/dev/null && stopped=1
    fi
    if [ "$stopped" = 0 ] && command -v taskkill >/dev/null 2>&1; then
        taskkill /F /IM "$name.exe" >/dev/null 2>&1 && stopped=1
    fi
    if [ "$stopped" = 1 ]; then
        echo "stopped $name"
    else
        echo "not running: $name"
    fi
    rm -f "$RUN_DIR/$name.pid"
}

for name in legacy-api mmogbrain gateway dn-dedicated game-manager master-server auth-server; do
    stop_service "$name"
done
