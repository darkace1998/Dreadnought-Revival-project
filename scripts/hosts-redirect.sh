#!/usr/bin/env bash
# hosts-redirect.sh — Add Greybox API domain redirects to /etc/hosts on the client.
# Run this on EACH CLIENT machine that will connect to the private server.
# Usage: SERVER_IP=192.168.1.100 ./hosts-redirect.sh
set -euo pipefail

SERVER_IP="${SERVER_IP:-127.0.0.1}"
HOSTS_FILE="/etc/hosts"
MARKER="# Dreadnought Private Server"

if grep -q "$MARKER" "$HOSTS_FILE" 2>/dev/null; then
  echo "[!] Redirects already present in $HOSTS_FILE"
  echo "    Remove existing entries first if you want to update."
  exit 0
fi

echo "[*] Adding Dreadnought private server redirects to $HOSTS_FILE"
echo "    Server IP: $SERVER_IP"

sudo tee -a "$HOSTS_FILE" > /dev/null << EOF

$MARKER — added by hosts-redirect.sh
$SERVER_IP profile-api.prod.greybox.sixfoot.live
$SERVER_IP legacyapi.prod.greybox.sixfoot.live
$SERVER_IP mmog.greybox.sixfoot.live
$SERVER_IP bugreports.greybox.com
$SERVER_IP masterserver.local
$SERVER_IP gamemanager.local
EOF

echo "[✓] Hosts redirected. DNS for Greybox domains now points to $SERVER_IP"
echo ""
echo "    To remove: delete lines between '$MARKER' markers in $HOSTS_FILE"
