#!/usr/bin/env bash
# Nightly backup of the lead list to Google Cloud Storage (or Google Drive).
#
# The lead list (leads.db) is the one thing on the collector's machine that cannot be scraped
# again: which leads were sent to Saleshandy and where, and the operator's statuses and notes --
# above all "do_not_contact". This takes a consistent copy while the dashboard keeps running,
# checks it, compresses it, and uploads it (with saleshandy.json) through rclone. The last 7 days
# are also kept on the machine.
#
# Usage: backup_leads.sh            run one backup now
#        backup_leads.sh install    install and start the nightly systemd timer (03:30 UTC)
#        backup_leads.sh status     the timer and the last run's log
#
# Google Cloud Storage (the default): a bucket in the lead lab's own GCP project -- never the
# axiointel project -- and a service account key at /etc/axiointel/gcs-backup.json (root, 600)
# whose account may only create and read objects in that bucket, so a stolen key cannot delete a
# backup. The bucket's lifecycle rule deletes backups after 30 days; this script deletes nothing
# there. Name the bucket in /etc/axiointel/backup.env:  LEADS_BACKUP_BUCKET=<bucket>
#
# Google Drive instead: sudo rclone config --config /etc/axiointel/rclone.conf (a "drive" remote
# named gdrive, scope drive.file) and LEADS_BACKUP_REMOTE=gdrive:AxioCRED-backups; there, backups
# older than LEADS_BACKUP_KEEP_DAYS (default 30) are deleted by this script.
#
# Configuration (environment, or /etc/axiointel/backup.env):
#   LEADS_DATA_DIR          default /var/lib/axio-leads
#   LEADS_BACKUP_BUCKET     the Cloud Storage bucket
#   GCS_KEY_FILE            default /etc/axiointel/gcs-backup.json
#   LEADS_BACKUP_REMOTE     an rclone remote instead of the bucket, e.g. gdrive:AxioCRED-backups
#   RCLONE_CONFIG_FILE      default /etc/axiointel/rclone.conf (root, 600), for such a remote
#   LEADS_BACKUP_KEEP_DAYS  default 30 (remotes other than the bucket)
#   LEADS_BACKUP_LOCAL      default /var/backups/axio-leads
#   LEADS_BACKUP_SOURCE     what this machine is called in the backups; default the host name
#   LEADS_CSV_KEEP_DAYS     job CSVs are kept locally this long after being archived; default 14
#
# Layout, so a folder of backups says what came from where:
#   <remote>/<source>/<YYYY-MM>/<YYYY-MM-DD>/
#       leads_<source>_<YYYY-MM-DD>.db.gz          the lead list (SQLite, gzip)
#       jobs_<source>_<YYYY-MM-DD>.db.gz           the job queue, so a typed-in campaign survives
#       campaign_<source>_<YYYY-MM-DD>.db.gz       the campaign planner's state, once it exists
#       saleshandy-config_<source>_<YYYY-MM-DD>.json  which campaigns leads may go into
#       RECEIPT_<source>_<YYYY-MM-DD>.txt           what is inside: counts, checksums, versions
#   <remote>/csv/<source>/<job-id>.csv.gz            every ingested job's raw CSV (every column
#                                                     the scraper collected). Moved off the machine
#                                                     and gzipped the run after its job is folded
#                                                     into the lead list; kept locally for
#                                                     LEADS_CSV_KEEP_DAYS (default 14) after that as
#                                                     a cache, then deleted here only -- the remote
#                                                     copy is not touched. This is why "Raw CSV" and
#                                                     "Map" stop working on the dashboard for a job
#                                                     older than that: pull the .gz from the remote.
set -euo pipefail

if [ -r /etc/axiointel/backup.env ]; then
  set -a
  # shellcheck disable=SC1091
  . /etc/axiointel/backup.env
  set +a
fi

DATA="${LEADS_DATA_DIR:-/var/lib/axio-leads}"
CONF="${RCLONE_CONFIG_FILE:-/etc/axiointel/rclone.conf}"
KEY="${GCS_KEY_FILE:-/etc/axiointel/gcs-backup.json}"
BUCKET="${LEADS_BACKUP_BUCKET:-}"
REMOTE="${LEADS_BACKUP_REMOTE:-}"
SOURCE="${LEADS_BACKUP_SOURCE:-$(hostname -s)}"
KEEP_DAYS="${LEADS_BACKUP_KEEP_DAYS:-30}"
LOCAL="${LEADS_BACKUP_LOCAL:-/var/backups/axio-leads}"
HERE="$(cd "$(dirname "$0")" && pwd)"

install_timer() {
  sudo tee /etc/systemd/system/axio-leads-backup.service >/dev/null <<EOF
[Unit]
Description=Back up the lead list to Google Drive
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=$HERE/backup_leads.sh
EOF
  sudo tee /etc/systemd/system/axio-leads-backup.timer >/dev/null <<'EOF'
[Unit]
Description=Nightly lead list backup

[Timer]
OnCalendar=*-*-* 03:30:00 UTC
Persistent=true
RandomizedDelaySec=300

[Install]
WantedBy=timers.target
EOF
  sudo systemctl daemon-reload
  sudo systemctl enable --now axio-leads-backup.timer
  systemctl list-timers axio-leads-backup.timer --no-pager
}

# backup_sqlite_copy takes SQLite's own consistent online copy of src, checks it, and gzips it to
# dst.gz. Non-zero (the copy is left, ungzipped, for inspection) if the copy fails its own
# integrity check -- never fatal to the caller, so one damaged database does not stop the rest of
# the backup.
backup_sqlite_copy() {
  local src="$1" dst="$2"
  python3 - "$src" "$dst" <<'PY'
import sqlite3, sys
src = sqlite3.connect(sys.argv[1])
dst = sqlite3.connect(sys.argv[2])
src.backup(dst)
ok = dst.execute("PRAGMA quick_check").fetchone()[0]
dst.close()
src.close()
if ok != "ok":
    sys.exit("the copy of " + sys.argv[1] + " failed its integrity check: " + ok)
PY
  # Explicit: callers use this in an `if`, where bash ignores set -e inside the function.
  [ $? -eq 0 ] || return 1
  gzip -f "$dst"
}

# archive_job_csvs moves every ingested job's raw CSV out of $DATA -- gzipped, into
# $LOCAL/csv -- so a 60-day run of tens of thousands of jobs does not fill the collector's disk.
# Only a job that is both in leads.db's ingested_jobs and finished (ok or failed in jobs.db) and
# untouched for an hour: a job yielded to AxioIntel, a stalled one, and one sent back with Rerun all
# return to the queue under the same id and CSV path, and moving a CSV while its job is writing it
# would lose that run. Uploaded on every run (rclone skips what has not changed) and kept locally
# for LEADS_CSV_KEEP_DAYS as a cache; the remote copy is never pruned here.
archive_job_csvs() {
  local keep_days="${LEADS_CSV_KEEP_DAYS:-14}"
  local archive_dir="$LOCAL/csv"
  mkdir -p "$archive_dir"

  local ingested moved=0 job_id csv
  [ -f "$DATA/jobs.db" ] || return 0
  ingested=$(python3 - "$DATA/leads.db" "$DATA/jobs.db" <<'PY'
import sqlite3, sys, time
leads = sqlite3.connect("file:" + sys.argv[1] + "?mode=ro", uri=True)
jobs = sqlite3.connect("file:" + sys.argv[2] + "?mode=ro", uri=True)
done = {r[0] for r in leads.execute("SELECT job_id FROM ingested_jobs")}
cutoff = int(time.time()) - 3600
for (job_id,) in jobs.execute(
        "SELECT id FROM jobs WHERE status IN ('ok', 'failed') AND updated_at < ?", (cutoff,)):
    if job_id in done:
        print(job_id)
PY
)

  while IFS= read -r job_id; do
    [ -n "$job_id" ] || continue
    csv="$DATA/$job_id.csv"
    [ -f "$csv" ] || continue
    gzip -f "$csv" && mv "$csv.gz" "$archive_dir/" && moved=$((moved + 1))
  done <<<"$ingested"
  [ "$moved" -gt 0 ] && echo "archived $moved job CSV(s) off the machine"

  if [ -n "$(ls -A "$archive_dir" 2>/dev/null)" ]; then
    "${RCLONE[@]}" copy "$archive_dir" "$DEST/csv/$SOURCE"
  fi

  find "$archive_dir" -type f -mtime +"$keep_days" -delete
}

case "${1:-run}" in
  install) install_timer; exit 0 ;;
  status)
    systemctl list-timers axio-leads-backup.timer --no-pager || true
    journalctl -u axio-leads-backup.service -n 20 --no-pager || true
    exit 0 ;;
  run) ;;
  *) echo "usage: $0 [run|install|status]" >&2; exit 1 ;;
esac

[ "$(id -u)" -eq 0 ] || { echo "run as root (the lead list and the keys are root's)" >&2; exit 1; }

# Where the backup goes: the bucket (an rclone connection string, no config file needed) or a
# configured remote.
if [ -n "$REMOTE" ]; then
  [ -r "$CONF" ] || { echo "no rclone config at $CONF; run: rclone config --config $CONF" >&2; exit 1; }
  RCLONE=(rclone --config "$CONF")
  DEST="$REMOTE"
  PRUNE=1
elif [ -n "$BUCKET" ]; then
  [ -r "$KEY" ] || { echo "no service account key at $KEY" >&2; exit 1; }
  RCLONE=(rclone --config /dev/null)
  DEST=":gcs,service_account_file=$KEY,bucket_policy_only=true,no_check_bucket=true:$BUCKET/leads"
  PRUNE=0 # the bucket's lifecycle rule deletes old backups; the key cannot
else
  echo "set LEADS_BACKUP_BUCKET (or LEADS_BACKUP_REMOTE) in /etc/axiointel/backup.env" >&2
  exit 1
fi
[ -f "$DATA/leads.db" ] || { echo "no lead list at $DATA/leads.db" >&2; exit 1; }
case "$SOURCE" in
  *[!A-Za-z0-9._-]* | "") echo "LEADS_BACKUP_SOURCE may hold only letters, digits, . _ -" >&2; exit 1 ;;
esac

day=$(date -u +%Y-%m-%d)
rel="$SOURCE/${day:0:7}/$day"
dir="$LOCAL/$rel"
mkdir -p "$dir"
chmod 700 "$LOCAL"
db="$dir/leads_${SOURCE}_$day.db"
receipt="$dir/RECEIPT_${SOURCE}_$day.txt"

# SQLite's online backup: a consistent copy even while the dashboard is writing to the list.
python3 - "$DATA/leads.db" "$db" <<'PY'
import sqlite3, sys
src = sqlite3.connect(sys.argv[1])
dst = sqlite3.connect(sys.argv[2])
src.backup(dst)
ok = dst.execute("PRAGMA quick_check").fetchone()[0]
leads = dst.execute("SELECT count(*) FROM leads").fetchone()[0]
dst.close()
src.close()
if ok != "ok":
    sys.exit("the copy failed its integrity check: " + ok)
print(leads, "leads copied")
PY

# The receipt: what this backup holds, read from the copy itself.
python3 - "$db" "$receipt" "$SOURCE" "$DATA" "$(git -c safe.directory='*' -C "$HERE" log -1 --format='%h %s' 2>/dev/null || echo unknown)" <<'PY'
import datetime, os, socket, sqlite3, sys
db, out, source, data, version = sys.argv[1:6]
d = sqlite3.connect(db)
one = lambda q: d.execute(q).fetchone()[0]
cols = {r[1] for r in d.execute("PRAGMA table_info(leads)")}
lines = [
    "AxioCRED lead list backup",
    "",
    "Source           " + source + " (host " + socket.gethostname() + ")",
    "Taken            " + datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d %H:%M UTC"),
    "From             " + os.path.join(data, "leads.db"),
    "Dashboard build  " + version,
    "",
    "Leads            " + str(one("SELECT count(*) FROM leads")),
    "  with email     " + str(one("SELECT count(*) FROM leads WHERE emails <> ''")),
    "  with phone     " + str(one("SELECT count(*) FROM leads WHERE phone <> ''")),
    "  sent to Saleshandy  " + str(one("SELECT count(*) FROM leads WHERE saleshandy_at > 0")),
]
if "wa_opt_in_at" in cols:
    lines.append("  WhatsApp opted in   " + str(one("SELECT count(*) FROM leads WHERE wa_opt_in_at > 0 AND wa_opt_out_at = 0")))
lines.append("Jobs that found them  " + str(one("SELECT count(DISTINCT job_id) FROM lead_jobs")))
lines += ["", "By status"]
lines += ["  %-16s %d" % r for r in d.execute("SELECT status, count(*) FROM leads GROUP BY status ORDER BY 2 DESC")]
lines += ["", "By source"]
lines += ["  %-16s %d" % r for r in d.execute("SELECT source, count(*) FROM leads GROUP BY source ORDER BY 2 DESC")]
d.close()
with open(out, "w") as f:
    f.write("\n".join(lines) + "\n")
PY

gzip -f "$db"

# The job queue (a typed-in campaign's city and trade lists have no other copy) and, once the
# campaign planner exists, its own state. Neither failure is fatal: leads.db is the one thing that
# cannot be re-created, so a damaged jobs.db or campaign.db is logged and skipped, not aborted on.
for extra in jobs campaign; do
  src="$DATA/$extra.db"
  [ -f "$src" ] || continue
  if backup_sqlite_copy "$src" "$dir/${extra}_${SOURCE}_$day.db"; then
    echo "$extra.db backed up" >>"$receipt"
  else
    echo "warning: $extra.db backup failed its integrity check; not included" >&2
  fi
done

if [ -f "$DATA/saleshandy.json" ]; then
  cp "$DATA/saleshandy.json" "$dir/saleshandy-config_${SOURCE}_$day.json"
fi

{
  echo
  echo "Files (sha256)"
  (cd "$dir" && sha256sum -- *.gz *.json 2>/dev/null | sed 's/^/  /')
  echo
  echo "Restore: gunzip leads_${SOURCE}_$day.db.gz, stop the dashboard, and put the file at"
  echo "$DATA/leads.db (see docs: axiocred/lead-engine)."
} >>"$receipt"

"${RCLONE[@]}" copy "$dir" "$DEST/$rel"
if [ "$PRUNE" = 1 ]; then
  "${RCLONE[@]}" delete "$DEST/$SOURCE" --min-age "${KEEP_DAYS}d"
  "${RCLONE[@]}" rmdirs "$DEST/$SOURCE" --leave-root
fi
# $LOCAL/csv keeps job CSVs for LEADS_CSV_KEEP_DAYS, not this 7 days; archive_job_csvs prunes it.
find "$LOCAL" -type f -mtime +7 -not -path "$LOCAL/csv/*" -delete
find "$LOCAL" -mindepth 1 -type d -empty -not -path "$LOCAL/csv" -delete

archive_job_csvs

echo "$(date -u +%FT%TZ) backed up $rel ($(du -h "$db.gz" | cut -f1)) to ${REMOTE:-gs://$BUCKET/leads}"
