#!/usr/bin/env bash
# setup.sh — One-shot setup for the Dreadnought Private Server on Linux.
# Builds all Go services, generates certificates, and prepares run directories.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
RUN_DIR="$PROJECT_DIR/run"

echo "================================================"
echo "  Dreadnought Private Server — Setup"
echo "================================================"
echo ""

# Check Go
if ! command -v go &>/dev/null; then
  echo "[!] Go not found. Install Go 1.21+ from https://golang.org/dl/"
  exit 1
fi
echo "[✓] Go $(go version | awk '{print $3}')"

# Check openssl
if ! command -v openssl &>/dev/null; then
  echo "[!] openssl not found. Install with: apt install openssl"
  exit 1
fi
echo "[✓] openssl found"

# Generate TLS certificates
if [ ! -f "$PROJECT_DIR/certs/server.crt" ]; then
  echo ""
  echo "[*] Generating TLS certificates..."
  bash "$SCRIPT_DIR/gen-certs.sh"
else
  echo "[✓] TLS certificates already exist"
fi

# Build all Go services
echo ""
echo "[*] Building Go services..."
mkdir -p "$RUN_DIR"

SERVICES=("auth-server" "legacy-api" "mmogbrain" "master-server" "game-manager" "gateway" "admin-cli")
for svc in "${SERVICES[@]}"; do
  echo "    Building $svc..."
  cd "$PROJECT_DIR/$svc"
  go build -o "$RUN_DIR/$svc" . 2>&1
  echo "    [✓] $svc"
done

# Create data directory
mkdir -p "$PROJECT_DIR/data"

echo ""
echo "================================================"
echo "  Setup complete!"
echo "================================================"
echo ""
echo "  Binaries:     $RUN_DIR/"
echo "  Certificates: $PROJECT_DIR/certs/"
echo "  Data:         $PROJECT_DIR/data/"
echo ""
echo "  Next steps:"
echo "    1. Set SERVER_IP and run: SERVER_IP=<your-ip> bash scripts/hosts-redirect.sh"
echo "       (on each client machine)"
echo "    2. Install certs/ca.crt as trusted root on client machines"
echo "    3. Start services: docker-compose up  OR  run each binary individually"
echo ""
echo "  Environment variables (set before running each binary):"
echo "    JWT_SECRET=<strong-secret>   (same for auth-server, legacy-api, mmogbrain)"
echo "    ADMIN_KEY=<strong-secret>    (same for auth-server, mmogbrain; used by admin-cli)"
echo "    DB_PATH=<path>.db"
echo "    ADDR=:<port>"
echo "    SERVER_IP=<public-ip>        (game-manager)"
echo "    GAME_BINARY=<path-to-exe>    (game-manager)"
echo "    WINE_EXE=wine                (game-manager)"
echo "    PLAYERS_PER_MATCH=10         (mmogbrain; use 2 for quick testing)"
echo ""
echo "  Admin CLI usage:"
echo "    ADMIN_KEY=<key> $RUN_DIR/admin-cli status"
echo "    ADMIN_KEY=<key> $RUN_DIR/admin-cli ban <username> <reason>"
echo "    ADMIN_KEY=<key> $RUN_DIR/admin-cli instances"
echo ""
