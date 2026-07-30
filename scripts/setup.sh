#!/usr/bin/env bash
# setup.sh -- one-shot setup for the Dreadnought private server on Linux.
#
# Builds every service, generates TLS certificates, and writes a run/secrets.env
# with fresh random keys. Safe to re-run: it never overwrites an existing
# certificate or secrets file.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
RUN_DIR="$PROJECT_DIR/run"

echo "================================================"
echo "  Dreadnought private server -- setup"
echo "================================================"
echo

# ---------------------------------------------------------------- prerequisites
if ! command -v go &>/dev/null; then
  echo "[!] Go not found. Install Go 1.24+ from https://golang.org/dl/" >&2
  exit 1
fi
GO_VERSION="$(go version | awk '{print $3}' | sed 's/^go//')"
GO_MAJOR="${GO_VERSION%%.*}"
GO_MINOR="$(echo "$GO_VERSION" | cut -d. -f2)"
if [ "$GO_MAJOR" -lt 1 ] || { [ "$GO_MAJOR" -eq 1 ] && [ "$GO_MINOR" -lt 24 ]; }; then
  # go.work declares go 1.25.0 and the modules declare 1.24.0. An older
  # toolchain fails with a confusing "go.mod requires go >= ..." per module.
  echo "[!] Go $GO_VERSION is too old; the workspace needs 1.24+." >&2
  exit 1
fi
echo "[OK] Go $GO_VERSION"

if ! command -v openssl &>/dev/null; then
  echo "[!] openssl not found. Install with: apt install openssl" >&2
  exit 1
fi
echo "[OK] openssl"

if command -v wine &>/dev/null; then
  echo "[OK] wine $(wine --version 2>/dev/null || echo '?')"
else
  # Only game-manager needs it, and only to spawn battle servers. Everything up
  # to and including the hangar works without Wine.
  echo "[--] wine not found -- matchmaking will form matches but no battle"
  echo "     server can be spawned. Install wine (or wine-staging) for that."
fi

# ---------------------------------------------------------------- certificates
if [ -f "$PROJECT_DIR/certs/server.crt" ]; then
  echo "[OK] TLS certificates already exist (delete certs/ to regenerate)"
else
  echo
  echo "[*] Generating TLS certificates..."
  bash "$SCRIPT_DIR/gen-certs.sh"
fi

# ---------------------------------------------------------------------- build
echo
echo "[*] Building services..."
mkdir -p "$RUN_DIR"

SERVICES=(auth-server legacy-api mmogbrain master-server game-manager gateway admin-cli)
for svc in "${SERVICES[@]}"; do
  printf '    %-16s ' "$svc"
  (cd "$PROJECT_DIR" && go build -o "$RUN_DIR/$svc" "./$svc")
  echo "OK"
done

# The launcher is the one client-side component, and it is Windows-only
# (//go:build windows). Cross-compiling it here is optional -- skip quietly if
# the toolchain cannot.
printf '    %-16s ' "dn-launcher.exe"
if (cd "$PROJECT_DIR" && GOOS=windows GOARCH=amd64 go build -o "$RUN_DIR/dn-launcher.exe" ./dn-launcher) 2>/dev/null; then
  echo "OK (windows/amd64)"
else
  echo "skipped (cross-compile unavailable)"
fi

# ---------------------------------------------------------------------- secrets
echo
if [ -f "$RUN_DIR/secrets.env" ]; then
  echo "[OK] run/secrets.env already exists (left untouched)"
else
  echo "[*] Generating run/secrets.env with fresh random keys..."
  JWT="$(openssl rand -hex 32)"
  ADMIN="$(openssl rand -hex 32)"
  sed -e "s|^JWT_SECRET=$|JWT_SECRET=$JWT|" \
      -e "s|^ADMIN_KEY=$|ADMIN_KEY=$ADMIN|" \
      "$SCRIPT_DIR/secrets.env.example" > "$RUN_DIR/secrets.env"
  chmod 600 "$RUN_DIR/secrets.env"
  echo "[OK] run/secrets.env written (mode 600, gitignored)"
fi

# ------------------------------------------------------------------ game data
# The server reads the game's extracted item tables from data/. They are
# committed, so a clone has them; a missing data/ means every id-to-name lookup
# silently falls back and the client gets nonsense.
if [ -d "$PROJECT_DIR/data/assets" ]; then
  echo "[OK] game data present ($(find "$PROJECT_DIR/data" -type f | wc -l) files under data/)"
else
  echo "[!] data/assets is missing -- item names and ids will not resolve." >&2
fi

echo
echo "================================================"
echo "  Setup complete"
echo "================================================"
echo
echo "  Binaries:      $RUN_DIR/"
echo "  Certificates:  $PROJECT_DIR/certs/"
echo "  Secrets:       $RUN_DIR/secrets.env"
echo
echo "  Next:"
echo "    1. Review run/secrets.env (set GAME_BINARY to run battle servers)."
echo "    2. Start the stack:   bash scripts/start-services.sh"
echo "    3. On each client:    install certs/ca.crt as a trusted root CA and"
echo "                          redirect the Greybox hostnames to this server"
echo "                          (scripts/hosts-redirect.sh / .ps1)."
echo
echo "  Stop the stack with:    bash scripts/stop-services.sh"
echo "  Admin CLI:              ADMIN_KEY=<key> $RUN_DIR/admin-cli help"
echo
