#!/usr/bin/env bash
# stop-services.sh -- stop the whole stack.
#
# Matches on the exact process NAME rather than a pattern. `pkill -f <pattern>`
# kills your own shell here, because the bash -c command line that runs the
# script contains the pattern.
set -u
cd "$(dirname "$0")/.." || exit 1
RUN_DIR="$PWD/run"

for name in legacy-api mmogbrain gateway game-manager master-server auth-server; do
    if pgrep -x "$name" >/dev/null 2>&1; then
        pkill -x "$name" && echo "stopped $name"
    else
        echo "not running: $name"
    fi
    rm -f "$RUN_DIR/$name.pid"
done
