#!/usr/bin/env bash
set -euo pipefail

# ──────────────────────────────────────────────────────────────────────
# Brezel — PostgreSQL backup script with S3 upload & retention cleanup
# ──────────────────────────────────────────────────────────────────────

DRY_RUN=false
if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=true
fi

# ── Helpers ──────────────────────────────────────────────────────────

log() {
  echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*"
}

die() {
  log "ERROR: $*" >&2
  exit 1
}

# ── Validate required env vars ───────────────────────────────────────

DB_URL="${DATABASE_URL:-${POSTGRES_DSN:-}}"
[[ -n "$DB_URL" ]]            || die "DATABASE_URL (or POSTGRES_DSN) is not set"
[[ -n "${S3_BACKUP_BUCKET:-}" ]]   || die "S3_BACKUP_BUCKET is not set"
[[ -n "${AWS_ACCESS_KEY_ID:-}" ]]  || die "AWS_ACCESS_KEY_ID is not set"
[[ -n "${AWS_SECRET_ACCESS_KEY:-}" ]] || die "AWS_SECRET_ACCESS_KEY is not set"
[[ -n "${AWS_REGION:-}" ]]         || die "AWS_REGION is not set"

RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-30}"
S3_PREFIX="db-backups"
TIMESTAMP="$(date -u '+%Y-%m-%d-%H%M%S')"
DUMP_FILE="brezel-backup-${TIMESTAMP}.dump"
TMPDIR_BACKUP="$(mktemp -d)"
DUMP_PATH="${TMPDIR_BACKUP}/${DUMP_FILE}"
S3_DEST="s3://${S3_BACKUP_BUCKET}/${S3_PREFIX}/${DUMP_FILE}"

# Clean up temp directory on exit
cleanup() {
  rm -rf "$TMPDIR_BACKUP"
}
trap cleanup EXIT

# ── Summary counters ─────────────────────────────────────────────────

BACKUP_SIZE=""
UPLOAD_SECONDS=""
CLEANED_COUNT=0

# ── Step 1: Dump the database ────────────────────────────────────────

log "Starting backup: ${DUMP_FILE}"
log "  Database URL: ${DB_URL%%@*}@****"
log "  S3 destination: ${S3_DEST}"
log "  Retention: ${RETENTION_DAYS} days"

if $DRY_RUN; then
  log "[DRY-RUN] Would run: pg_dump -Fc -f ${DUMP_PATH} <DATABASE_URL>"
else
  log "Dumping database..."
  pg_dump -Fc -f "$DUMP_PATH" "$DB_URL"
  BACKUP_SIZE="$(du -h "$DUMP_PATH" | cut -f1)"
  log "Dump complete: ${DUMP_FILE} (${BACKUP_SIZE})"
fi

# ── Step 2: Upload to S3 ────────────────────────────────────────────

if $DRY_RUN; then
  log "[DRY-RUN] Would run: aws s3 cp ${DUMP_PATH} ${S3_DEST}"
else
  log "Uploading to S3..."
  UPLOAD_START="$(date +%s)"
  aws s3 cp "$DUMP_PATH" "$S3_DEST" --only-show-errors
  UPLOAD_END="$(date +%s)"
  UPLOAD_SECONDS="$(( UPLOAD_END - UPLOAD_START ))"
  log "Upload complete in ${UPLOAD_SECONDS}s"
fi

# ── Step 3: Retention cleanup ────────────────────────────────────────

log "Checking for backups older than ${RETENTION_DAYS} days..."

CUTOFF_EPOCH="$(date -u -j -v-${RETENTION_DAYS}d '+%s' 2>/dev/null || date -u -d "${RETENTION_DAYS} days ago" '+%s')"

if $DRY_RUN; then
  log "[DRY-RUN] Would list objects in s3://${S3_BACKUP_BUCKET}/${S3_PREFIX}/ and delete those older than ${RETENTION_DAYS} days"
else
  # List all backup objects in the prefix
  OBJECTS_JSON="$(aws s3api list-objects-v2 \
    --bucket "$S3_BACKUP_BUCKET" \
    --prefix "${S3_PREFIX}/" \
    --query "Contents[?ends_with(Key, '.dump')]" \
    --output json 2>/dev/null || echo "[]")"

  if [[ "$OBJECTS_JSON" != "null" && "$OBJECTS_JSON" != "[]" ]]; then
    # Parse each object and check its LastModified date
    KEYS_TO_DELETE="$(echo "$OBJECTS_JSON" | python3 -c "
import sys, json, datetime

cutoff = datetime.datetime.utcfromtimestamp(${CUTOFF_EPOCH})
objects = json.load(sys.stdin)
if objects:
    for obj in objects:
        # Parse ISO 8601 date
        modified = datetime.datetime.fromisoformat(obj['LastModified'].replace('Z', '+00:00')).replace(tzinfo=None)
        if modified < cutoff:
            print(obj['Key'])
" 2>/dev/null || true)"

    if [[ -n "$KEYS_TO_DELETE" ]]; then
      while IFS= read -r key; do
        log "Deleting expired backup: s3://${S3_BACKUP_BUCKET}/${key}"
        aws s3 rm "s3://${S3_BACKUP_BUCKET}/${key}" --only-show-errors
        CLEANED_COUNT=$((CLEANED_COUNT + 1))
      done <<< "$KEYS_TO_DELETE"
    fi
  fi

  log "Retention cleanup: ${CLEANED_COUNT} old backup(s) deleted"
fi

# ── Summary ──────────────────────────────────────────────────────────

log "────────────────────────────────────"
log "Backup Summary"
log "  File:            ${DUMP_FILE}"
if $DRY_RUN; then
  log "  Mode:            DRY-RUN (no actions taken)"
else
  log "  Size:            ${BACKUP_SIZE}"
  log "  Upload time:     ${UPLOAD_SECONDS}s"
  log "  S3 location:     ${S3_DEST}"
  log "  Expired removed: ${CLEANED_COUNT}"
fi
log "────────────────────────────────────"
log "Done."
