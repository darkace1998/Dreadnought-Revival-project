#!/usr/bin/env bash
# Back up the server's state to a timestamped, compressed archive.
#
#   bash scripts/backup.sh                 # run/*.db + certs/ + secrets -> backups/
#   BACKUP_DIR=/mnt/offsite bash scripts/backup.sh
#   bash scripts/backup.sh --install-cron  # daily at 04:30, logs to backups/backup.log
#
# FIXED 2026-09-26 (audit): DATA_DIR defaulted to /var/lib/dreadnought, which
# does not exist here -- the databases live in run/ (start-services.sh pins
# DB_PATH there) -- and a missing database only printed a WARNING, after which
# an EMPTY archive was written and "Backup complete" printed. No backup had
# ever been taken. A missing or corrupt database now fails the run.
#
# What is saved and why:
#   run/{auth,legacy,mmog,master}.db  accounts, purchases, progress (SQLite
#                                     online backup: safe while services run)
#   certs/                            the CA key: losing it means every tester
#                                     must install a new ca.crt
#   run/secrets.env                   JWT_SECRET etc.: losing it signs everyone out
# The archive therefore contains secrets: it is written mode 600 in a 700
# directory. Keep a copy OFF this machine -- a backup on the same disk does not
# survive the disk.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
DATA_DIR="${DATA_DIR:-$PROJECT_DIR/run}"
BACKUP_DIR="${BACKUP_DIR:-$PROJECT_DIR/backups}"
KEEP_DAYS="${KEEP_DAYS:-14}"

if [ "${1:-}" = "--install-cron" ]; then
  line="30 4 * * * BACKUP_DIR=$BACKUP_DIR DATA_DIR=$DATA_DIR bash $PROJECT_DIR/scripts/backup.sh >> $BACKUP_DIR/backup.log 2>&1"
  mkdir -p "$BACKUP_DIR" && chmod 700 "$BACKUP_DIR"
  # "crontab -l" fails when there is no crontab yet; pipefail must not abort on that.
  ( { crontab -l 2>/dev/null || true; } | { grep -v "scripts/backup.sh" || true; } ; echo "$line" ) | crontab -
  echo "Installed: $line"
  exit 0
fi

command -v sqlite3 >/dev/null || { echo "ERROR: sqlite3 is not installed" >&2; exit 1; }

TIMESTAMP=$(date -u +%Y%m%dT%H%M%SZ)
umask 077
mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"
DEST="$BACKUP_DIR/$TIMESTAMP"
mkdir -p "$DEST"
trap 'rm -rf "$DEST"' EXIT

failed=0
for db in auth legacy mmog master; do
  SRC="$DATA_DIR/${db}.db"
  if [ ! -f "$SRC" ]; then
    echo "ERROR: $SRC not found" >&2
    failed=1
    continue
  fi
  sqlite3 "$SRC" ".backup '$DEST/${db}.db'"
  check=$(sqlite3 "$DEST/${db}.db" "PRAGMA integrity_check;")
  if [ "$check" != "ok" ]; then
    echo "ERROR: ${db}.db copy failed its integrity check: $check" >&2
    failed=1
    continue
  fi
  echo "Backed up ${db}.db ($(du -h "$DEST/${db}.db" | cut -f1))"
done
[ "$failed" = 0 ] || { echo "Backup FAILED -- nothing was archived." >&2; exit 1; }

[ -d "$PROJECT_DIR/certs" ] && cp -a "$PROJECT_DIR/certs" "$DEST/certs" && echo "Backed up certs/"
[ -f "$DATA_DIR/secrets.env" ] && cp -a "$DATA_DIR/secrets.env" "$DEST/secrets.env" && echo "Backed up secrets.env"

tar -czf "$BACKUP_DIR/${TIMESTAMP}.tar.gz" -C "$BACKUP_DIR" "$TIMESTAMP"
chmod 600 "$BACKUP_DIR/${TIMESTAMP}.tar.gz"
echo "Backup complete: $BACKUP_DIR/${TIMESTAMP}.tar.gz ($(du -h "$BACKUP_DIR/${TIMESTAMP}.tar.gz" | cut -f1))"

find "$BACKUP_DIR" -maxdepth 1 -name "*.tar.gz" -mtime +"$KEEP_DAYS" -print -delete | sed 's/^/Pruned: /'
