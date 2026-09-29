#!/usr/bin/env bash
# setup.sh -- one-shot setup for the master-master directory server.
#
# Standalone on purpose: the directory is run by the directory operator (you),
# not by every cluster, so the main scripts/setup.sh neither builds nor starts
# it. Safe to re-run: never overwrites the binary's surroundings, secrets, or DB.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
RUN_DIR="$PROJECT_DIR/run"

echo "================================================"
echo "  master-master directory -- setup"
echo "================================================"
echo

if ! command -v go &>/dev/null; then
  echo "[!] Go not found. Install Go 1.24+ from https://golang.org/dl/" >&2
  exit 1
fi
echo "[OK] $(go version)"

echo
echo "[*] Syncing module sums (needs network once)..."
(cd "$PROJECT_DIR/master-master" && go mod tidy) || echo "[!] tidy failed -- build may fail offline"

echo
echo "[*] Building..."
mkdir -p "$RUN_DIR"
(cd "$PROJECT_DIR" && go build -o "$RUN_DIR/master-master" ./master-master)
echo "[OK] $RUN_DIR/master-master"

echo
if [ -f "$RUN_DIR/master-master.env" ]; then
  echo "[OK] run/master-master.env already exists (left untouched)"
else
  echo "[*] Generating run/master-master.env with the dashboard password..."
  PASS="$(openssl rand -hex 24)"
  printf '# master-master directory secrets (mode 600, gitignored).\n' > "$RUN_DIR/master-master.env"
  printf '# Dashboard login (HTTP Basic, username is anything):\n' >> "$RUN_DIR/master-master.env"
  printf 'MASTER_ADMIN_PASSWORD=%s\n' "$PASS" >> "$RUN_DIR/master-master.env"
  chmod 600 "$RUN_DIR/master-master.env"
  echo "[OK] run/master-master.env written (mode 600, gitignored)"
fi

echo
echo "================================================"
echo "  Setup complete"
echo "================================================"
echo
echo "  Binary:      $RUN_DIR/master-master"
echo "  Secrets:     $RUN_DIR/master-master.env (dashboard password)"
echo "  Database:    $RUN_DIR/master-master.db (created on first start)"
echo
echo "  Next:"
echo "    1. Start it:   bash master-master/start.sh"
echo "    2. Dashboard:  http://127.0.0.1:8091/admin  (or via SSH tunnel)"
echo "    3. Clusters point MASTER_MASTER_URL at it and set CLUSTER_NAME + CLUSTER_WEB_URL."
