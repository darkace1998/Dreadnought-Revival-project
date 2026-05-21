#!/usr/bin/env bash
# Backup all SQLite databases to a timestamped directory.
set -euo pipefail

BACKUP_DIR="${BACKUP_DIR:-/var/backups/dreadnought}"
DATA_DIR="${DATA_DIR:-/var/lib/dreadnought}"
TIMESTAMP=$(date -u +%Y%m%dT%H%M%SZ)
DEST="$BACKUP_DIR/$TIMESTAMP"

mkdir -p "$DEST"

for db in auth legacy mmog master; do
  SRC="$DATA_DIR/${db}.db"
  if [ -f "$SRC" ]; then
    # Use SQLite online backup (safe for WAL mode databases)
    sqlite3 "$SRC" ".backup '$DEST/${db}.db'"
    echo "Backed up ${db}.db → $DEST/${db}.db"
  else
    echo "WARNING: $SRC not found, skipping"
  fi
done

# Compress backup
tar -czf "$BACKUP_DIR/${TIMESTAMP}.tar.gz" -C "$BACKUP_DIR" "$TIMESTAMP"
rm -rf "$DEST"
echo "Backup complete: $BACKUP_DIR/${TIMESTAMP}.tar.gz"

# Prune backups older than 7 days
find "$BACKUP_DIR" -name "*.tar.gz" -mtime +7 -delete
echo "Old backups pruned."
