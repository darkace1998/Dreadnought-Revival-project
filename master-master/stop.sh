#!/usr/bin/env bash
# stop.sh -- stop the master-master directory server.
set -u
cd "$(dirname "$0")/.." || exit 1
RUN_DIR="$PWD/run"

pid="$(cat "$RUN_DIR/master-master.pid" 2>/dev/null)"
stopped=0
if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
  kill "$pid" 2>/dev/null && stopped=1
fi
if [ "$stopped" = 0 ] && command -v pkill >/dev/null 2>&1; then
  pkill -x "master-master" 2>/dev/null && stopped=1
fi
if [ "$stopped" = 0 ] && command -v taskkill >/dev/null 2>&1; then
  taskkill /F /IM "master-master.exe" >/dev/null 2>&1 && stopped=1
fi
if [ "$stopped" = 1 ]; then
  echo "stopped master-master"
else
  echo "not running: master-master"
fi
rm -f "$RUN_DIR/master-master.pid"
