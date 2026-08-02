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
for name in legacy-api mmogbrain gateway dn-dedicated game-manager master-server auth-server; do
    if pgrep -x "$name" >/dev/null 2>&1; then
        pkill -x "$name" && echo "stopped $name"
    else
        echo "not running: $name"
    fi
    rm -f "$RUN_DIR/$name.pid"
done
