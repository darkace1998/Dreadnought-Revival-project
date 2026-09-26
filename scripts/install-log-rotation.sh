#!/usr/bin/env bash
# Install log rotation for this server (run as root): bash scripts/install-log-rotation.sh
#
# Added 2026-09-26 (audit): nothing rotated. mmogbrain logs every frame (with
# hex and text dumps) to mmogbrain.log in its working directory, the services
# append to run/<service>.log, and dn-dedicated writes one file per match to
# run/battle-logs/ -- all growing without bound, which with public testers
# means gigabytes.
#
# The system's logrotate already runs daily (logrotate.timer / cron.daily); this
# adds /etc/logrotate.d/dreadnought. copytruncate, because the services write
# through shell redirection (">> run/x.log") and would keep writing to a
# renamed file. Per-match battle logs and crash reports are one file each, so
# they are pruned by age instead.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BATTLE_DAYS="${BATTLE_DAYS:-7}"
CRASH_DAYS="${CRASH_DAYS:-30}"
CONF=/etc/logrotate.d/dreadnought

cat > "$CONF" <<EOF
# Installed by $PROJECT_DIR/scripts/install-log-rotation.sh -- edit there.
$PROJECT_DIR/mmogbrain.log $PROJECT_DIR/run/*.log {
    daily
    maxsize 200M
    rotate 7
    compress
    delaycompress
    copytruncate
    missingok
    notifempty
    create 0600 root root
    postrotate
        find $PROJECT_DIR/run/battle-logs -type f -mtime +$BATTLE_DAYS -delete 2>/dev/null || true
        find $PROJECT_DIR/run/crash-reports -type f -mtime +$CRASH_DAYS -delete 2>/dev/null || true
    endscript
    sharedscripts
}
EOF
chmod 644 "$CONF"
logrotate --debug "$CONF" 2>&1 | grep -E "error|considering log" | head -5 || true
echo "Installed $CONF (battle logs kept $BATTLE_DAYS days, crash reports $CRASH_DAYS days)."
