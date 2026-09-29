#!/usr/bin/env bash
# start.sh -- start the master-master directory server.
#
# Standalone like setup.sh: the main scripts/start-services.sh does not know
# this service. Refuses to double-start. Reads run/master-master.env when it
# exists (MASTER_ADMIN_PASSWORD), else the environment.
set -u
cd "$(dirname "$0")/.." || exit 1
ROOT="$PWD"
RUN_DIR="$ROOT/run"

if [ ! -x "$RUN_DIR/master-master" ]; then
  echo "missing binary: run/master-master -- run bash master-master/setup.sh" >&2
  exit 1
fi

# pidfile first (portable and exact), then pgrep, then tasklist on Windows.
# See scripts/start-services.sh for why pgrep alone is not trusted.
pid="$(cat "$RUN_DIR/master-master.pid" 2>/dev/null)"
if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
  echo "already running: master-master ($pid)"
  exit 0
fi
if command -v pgrep >/dev/null 2>&1 && pgrep -x "master-master" >/dev/null 2>&1; then
  echo "already running: master-master (found by name, stale pidfile removed)"
  rm -f "$RUN_DIR/master-master.pid"
  exit 0
fi

if [ -f "$RUN_DIR/master-master.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$RUN_DIR/master-master.env"
  set +a
fi
: "${MASTER_ADMIN_PASSWORD:?run/master-master.env must define MASTER_ADMIN_PASSWORD (see master-master/setup.sh)}"

export ADDR="${ADDR:-:8091}"
export ADMIN_ADDR="${ADMIN_ADDR:-:8092}"
export DB_PATH="${DB_PATH:-$RUN_DIR/master-master.db}"

"$RUN_DIR/master-master" >>"$RUN_DIR/master-master.log" 2>&1 &
echo $! >"$RUN_DIR/master-master.pid"
echo "started master-master $!"
sleep 2
printf ':8091 '
curl -s -m 2 "http://127.0.0.1:8091/health" || printf 'no response'
echo
echo "admin panel: http://127.0.0.1:${ADMIN_ADDR##*:}/admin  (all interfaces; prefer an SSH tunnel)"
