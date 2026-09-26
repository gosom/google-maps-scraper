#!/usr/bin/env bash
# Collect the places AxioIntel is watching with the Axio-CRED collector, and send them back.
#
# Runs on the collector's own machine, which must have no link to AxioIntel's Google Cloud
# project: its own provider account, its own IP addresses. fetch_targets.py asks AxioIntel which
# places to collect; a list kept here instead goes stale the moment a workspace watches something
# new. For each place, with the same arguments Axio-CRED's app uses for an exact Place ID, the
# collector opens the public Google Maps listing, collects its reviews, and writes JSON lines.
# push_native.py signs what came back and posts it to /api/ingest/native. Both programs are
# AxioIntel's, fetched from Axio-Backend's scripts/ so they cannot drift from the endpoints they
# talk to. Nothing here reports, flags or appeals anything.
#
# Since 27 Sep 2026 (owner's decision) Apify collects a place's whole review list -- its first pull
# and a weekly re-pull -- and this collector keeps it current in between: every two hours it reads
# the reviews the public listing shows sorted by Newest (`-review-newest`). Google shows signed-out
# visitors a limited view of five to eight reviews, so that is all a collection here can read; a
# read whose Newest sort cannot be confirmed is not sent. AxioIntel asks for a place by marking it
# requested or due; the daemon below polls every two minutes, so a person's "Pull now" is answered
# in minutes.
#
# Modes:
#   lab    (default) collect the places file given, keep every result on this machine, and add one
#          line per place to <work>/lab/summary.csv. AxioIntel is neither asked nor sent anything,
#          and no secret is needed.
#   send   ask AxioIntel what to collect, send each collection back (and write the same summary line).
#
# Usage: collect.sh [places-file]      one pass: the file given, or (send mode) AxioIntel's list
#        collect.sh daemon             send mode, forever: poll for requested and due places and
#                                      collect up to COLLECTOR_PARALLEL of them at once
#        collect.sh install-daemon     install and start the systemd service that runs `daemon`
#        collect.sh status             the service, and the last 20 summary lines
#
# Configuration, from /etc/axiointel/collector.env (root-owned, mode 600) or the environment:
#   COLLECTOR_MODE                   lab (default) or send; `daemon` requires send
#   AXIOINTEL_NATIVE_INGEST_SECRET   the shared secret; required in send mode only
#   AXIOINTEL_BASE_URL               which deployment to ask and send to; default
#                                    https://axiointel.com/api -- point it at staging for a first run
#   AXIOINTEL_TARGETS_URL            overrides only where the list comes from
#   AXIOINTEL_INGEST_URL             overrides only where collections are sent
#   COLLECTOR_PLACES_FILE            collect this list instead of asking AxioIntel (one pass only)
#   COLLECTOR_IMAGE                  docker image built from this repository; default axio-cred-collector
#   COLLECTOR_PROXIES_FILE           optional; one proxy URL per line, passed as -proxies-file
#   COLLECTOR_REVIEW_MODE            newest (default): only the Newest-sorted reviews the listing shows;
#                                    full: page through the whole list, as before 27 Sep 2026 (lab use)
#   COLLECTOR_TIMEOUT_SECONDS        per place; default 240 in newest mode, 900 in full. The
#                                    collector's own review clock (-review-budget) is set under it, so a
#                                    slow place is written out partial rather than killed and lost.
#   COLLECTOR_PARALLEL               daemon: places collected at once; default 3
#   COLLECTOR_POLL_SECONDS           daemon: how often AxioIntel is asked; default 120
#   COLLECTOR_MIN_INTERVAL_HOURS     daemon: a place that is due but not requested is collected at
#                                    most this often; default 1 (AxioIntel marks a place due every
#                                    two hours)
#   COLLECTOR_RETRY_MINUTES          daemon: a place whose last try failed waits this long; default 10
#   COLLECTOR_WORK_DIR               default /var/lib/axiointel-collector
#   COLLECTOR_KEEP_DAYS              local results kept this long; default 14
#
# Exit status (one pass): 0 when every place was collected and sent; 1 when the list could not be
# fetched, or when any place was not.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SELF="$HERE/$(basename "$0")"
ENV_FILE="${AXIOINTEL_COLLECTOR_ENV:-/etc/axiointel/collector.env}"
if [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi

COMMAND=run
case "${1:-}" in
  daemon|install-daemon|status) COMMAND="$1"; shift ;;
esac

WORK="${COLLECTOR_WORK_DIR:-/var/lib/axiointel-collector}"

install_daemon() {
  sudo tee /etc/systemd/system/axio-collector.service >/dev/null <<EOF
[Unit]
Description=Axio-CRED review collector for AxioIntel (send mode, polls for requests)
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
ExecStart=$SELF daemon
Restart=always
RestartSec=30
KillMode=mixed
TimeoutStopSec=60

[Install]
WantedBy=multi-user.target
EOF
  sudo systemctl daemon-reload
  sudo systemctl enable --now axio-collector.service
  systemctl --no-pager status axio-collector.service | head -5
}

case "$COMMAND" in
  install-daemon) install_daemon; exit 0 ;;
  status)
    systemctl --no-pager status axio-collector.service 2>/dev/null | head -5 || true
    [ -f "$WORK/lab/summary.csv" ] && tail -n 20 "$WORK/lab/summary.csv"
    exit 0 ;;
esac

MODE="${COLLECTOR_MODE:-lab}"
case "$MODE" in
  lab) ;;
  send)
    : "${AXIOINTEL_NATIVE_INGEST_SECRET:?set AXIOINTEL_NATIVE_INGEST_SECRET in $ENV_FILE}"
    export AXIOINTEL_NATIVE_INGEST_SECRET ;;
  *) echo "COLLECTOR_MODE must be lab or send, not $MODE" >&2; exit 1 ;;
esac
if [ "$COMMAND" = daemon ] && [ "$MODE" != send ]; then
  echo "the daemon sends to AxioIntel: set COLLECTOR_MODE=send in $ENV_FILE" >&2
  exit 1
fi

IMAGE="${COLLECTOR_IMAGE:-axio-cred-collector}"
REVIEW_MODE="${COLLECTOR_REVIEW_MODE:-newest}"
case "$REVIEW_MODE" in
  newest) TIMEOUT="${COLLECTOR_TIMEOUT_SECONDS:-240}" ;;
  full) TIMEOUT="${COLLECTOR_TIMEOUT_SECONDS:-900}" ;;
  *) echo "COLLECTOR_REVIEW_MODE must be newest or full, not $REVIEW_MODE" >&2; exit 1 ;;
esac
PARALLEL="${COLLECTOR_PARALLEL:-3}"
POLL="${COLLECTOR_POLL_SECONDS:-120}"
MIN_INTERVAL_S=$(( ${COLLECTOR_MIN_INTERVAL_HOURS:-1} * 3600 ))
RETRY_S=$(( ${COLLECTOR_RETRY_MINUTES:-10} * 60 ))
PUSHER="${AXIOINTEL_PUSHER:-$HERE/push_native.py}"
FETCHER="${AXIOINTEL_FETCHER:-$HERE/fetch_targets.py}"
KEEP_DAYS="${COLLECTOR_KEEP_DAYS:-14}"
BASE_URL="${AXIOINTEL_BASE_URL:-https://axiointel.com/api}"
BASE_URL="${BASE_URL%/}"
TARGETS_URL="${AXIOINTEL_TARGETS_URL:-$BASE_URL/collector/targets}"
INGEST_URL="${AXIOINTEL_INGEST_URL:-$BASE_URL/ingest/native}"
SUMMARY="$WORK/lab/summary.csv"

# A list named here is collected as given; otherwise AxioIntel is asked, below, once this run
# holds the lock. An answered list belongs to the run that asked for it, not to this folder.
PLACES="${1:-${COLLECTOR_PLACES_FILE:-}}"

if [ "$MODE" = lab ]; then
  [ -n "$PLACES" ] || { echo "lab mode collects a list of its own: pass a places file or set COLLECTOR_PLACES_FILE" >&2; exit 1; }
else
  [ -f "$PUSHER" ] || { echo "no sender at $PUSHER; copy scripts/push_native.py from Axio-Backend" >&2; exit 1; }
fi
mkdir -p "$WORK/lab"
echo "$(date -u +%FT%TZ) $COMMAND, mode $MODE"

# One pass or one daemon at a time: two would each think they own the machine's browser budget.
# A place is never collected twice at once either (a per-place lock, in collect_place).
exec 9>"$WORK/.lock"
flock -n 9 || { echo "a collection run is already going; not starting another"; exit 0; }

# The collector's review clock sits under this script's kill, so a slow place ends as a partial
# collection that is still written out, rather than a killed container that wrote nothing. Only an
# image built with the review collector knows the flag; an older one is run as before.
REVIEW_FLAGS=()
# Read the whole help first: under pipefail, `docker run | grep -q` fails whenever grep's early
# exit cuts docker off mid-write, and the flag would silently never be passed.
image_help=$(docker run --rm "$IMAGE" -h 2>&1 || true)
if grep -q -- -review-budget <<<"$image_help"; then
  if [ "$TIMEOUT" -gt 300 ]; then
    REVIEW_FLAGS=(-review-budget "$((TIMEOUT - 240))s")
  else
    REVIEW_FLAGS=(-review-budget "$((TIMEOUT * 4 / 5))s")
  fi
fi
if [ "$REVIEW_MODE" = newest ]; then
  # An image without the flag would page the whole list under a newest-mode clock: refuse it.
  grep -q -- -review-newest <<<"$image_help" || {
    echo "the image $IMAGE predates -review-newest; rebuild it, or set COLLECTOR_REVIEW_MODE=full" >&2
    exit 1
  }
  REVIEW_FLAGS+=(-review-newest)
fi

# The sender's flags this copy knows; older copies do not know --collected-seconds.
SENDER_TIMING=0
if [ "$MODE" = send ] && python3 "$PUSHER" --help 2>/dev/null | grep -q -- --collected-seconds; then
  SENDER_TIMING=1
fi

prune_old_results() {
  # Results hold reviewers' names and words. They are needed only until AxioIntel has them.
  find "$WORK" -mindepth 2 -maxdepth 2 -type d -name '20*' -mtime "+$KEEP_DAYS" -exec rm -rf {} + 2>/dev/null
}

summary_line() { # finished_at place purpose exit elapsed state collected reported reason stage rotations blocks sent results
  [ -s "$SUMMARY" ] || echo "finished_at,place_id,purpose,exit,elapsed_s,state,collected,reported,stop_reason,stop_stage,rotations,blocks,sent,results" > "$SUMMARY"
  local IFS=,
  echo "$*" >> "$SUMMARY"
}

# collect_place <place> <purpose>: one place, start to finish -- collect, read the coverage
# report, and in send mode push. Returns 0 when it was collected (and sent), 1 otherwise. Safe to
# run in the background: everything it writes is under the place's own folder, or one line
# appended to the summary.
collect_place() {
  local dir="$WORK/$1" lock rc
  mkdir -p "$dir"
  exec {lock}>"$dir/.lock"
  if ! flock -n "$lock"; then
    echo "$(date -u +%FT%TZ) $1 is already being collected"
    exec {lock}>&-
    return 0
  fi
  date +%s > "$dir/.last_attempt"
  collect_locked_place "$1" "${2:-owned}"
  rc=$?
  exec {lock}>&-
  return "$rc"
}

collect_locked_place() {
  local place="$1" purpose="$2"
  local dir="$WORK/$place"

  local run="$dir/$(date -u +%Y%m%dT%H%M%SZ)"
  mkdir -p "$run"
  printf 'https://www.google.com/maps/search/?api=1&query=Google&query_place_id=%s\n' "$place" \
    > "$run/query.txt"

  local proxy=() proxy_flag=()
  if [ -n "${COLLECTOR_PROXIES_FILE:-}" ]; then
    proxy=(-v "$COLLECTOR_PROXIES_FILE:/run/proxies.txt:ro")
    proxy_flag=(-proxies-file /run/proxies.txt)
  fi

  local name="axiointel-collect-$$-${place:0:40}"
  echo "$(date -u +%FT%TZ) collecting $place ($purpose)"
  local started code elapsed
  started=$(date +%s)
  timeout --kill-after=30 "$TIMEOUT" \
    docker run --rm --name "$name" -e DISABLE_TELEMETRY=1 -v "$run:/work" \
      ${proxy[@]+"${proxy[@]}"} "$IMAGE" \
      -input /work/query.txt -results /work/results.jsonl -json -lang en -depth 1 -zoom 15 \
      -c 1 -browser-pool-size 1 -pages-per-browser 1 -extra-reviews \
      ${REVIEW_FLAGS[@]+"${REVIEW_FLAGS[@]}"} ${proxy_flag[@]+"${proxy_flag[@]}"} \
    > "$run/collector.log" 2>&1
  code=$?
  elapsed=$(( $(date +%s) - started ))
  echo "$(date -u +%FT%TZ) collector finished $place in ${elapsed}s (exit $code)"
  # A timed-out docker client does not always take its container with it.
  if [ "$code" -eq 124 ] || [ "$code" -eq 137 ]; then docker rm -f "$name" >/dev/null 2>&1; fi
  # Never keep proxy credentials a collector echoed into its log.
  sed -i -E 's#(https?|socks5)://[^[:space:]/@]+:[^[:space:]/@]+@#\1://[redacted]@#g' \
    "$run/collector.log"

  if [ ! -s "$run/results.jsonl" ]; then
    echo "$(date -u +%FT%TZ) no results for $place (collector exit $code); see $run/collector.log" >&2
    summary_line "$(date -u +%FT%TZ)" "$place" "$purpose" "$code" "$elapsed" missing "" "" "" "" "" "" no "$run/results.jsonl"
    return 1
  fi

  local stopped=()
  if [ "$code" -ne 0 ]; then
    # A collection cut off by the time budget or an error still holds real reviews, but it is
    # not the whole list, and AxioIntel must not treat the reviews it lacks as removed.
    stopped=(--incomplete)
    echo "$(date -u +%FT%TZ) collector exit $code for $place; sending what it collected as incomplete" >&2
  fi

  # What the collector says about its own coverage (`review_collection` in the results). A
  # collection it does not call complete -- blocked, out of budget, a listing that ran short, a
  # count it could not read -- is sent as incomplete even when the container exited cleanly: the
  # sender's own check compares against the listing's count, and cannot see an unread one.
  local coverage cov_state cov_collected cov_reported cov_reason cov_stage cov_rotations cov_blocks
  coverage=$(python3 - "$run/results.jsonl" <<'PY'
import json, sys
found = None
with open(sys.argv[1], encoding="utf-8", errors="replace") as fh:
    for line in fh:
        try:
            entry = json.loads(line)
        except ValueError:
            continue
        report = entry.get("review_collection") if isinstance(entry, dict) else None
        if isinstance(report, dict):
            found = report
if found is None:
    print("missing")
else:
    print(" ".join(str(v) for v in (
        "complete" if found.get("complete") else "partial",
        found.get("collected", 0), found.get("reported", 0),
        found.get("stop_reason") or "-", found.get("stop_stage") or "-",
        found.get("identity_rotations", 0), found.get("blocks", 0))))
PY
)
  read -r cov_state cov_collected cov_reported cov_reason cov_stage cov_rotations cov_blocks \
    <<<"$coverage"
  if [ "$cov_state" = "missing" ] || [ -z "$cov_state" ]; then
    echo "$(date -u +%FT%TZ) $place: no coverage report (an image built before the review collector)"
  else
    echo "$(date -u +%FT%TZ) $place: $cov_collected/$cov_reported reviews, $cov_state" \
      "($cov_reason, stage $cov_stage, $cov_rotations rotation(s), $cov_blocks block(s))"
    if [ "$cov_state" = "partial" ] && [ ${#stopped[@]} -eq 0 ]; then
      stopped=(--incomplete)
    fi
  fi

  local sent=no rc=0
  if [ "$cov_reason" = unsorted ]; then
    # The Newest sort did not take: what the listing showed may be its "most relevant" reviews,
    # and AxioIntel would read them as the newest. Nothing is sent; the next pass tries again.
    echo "$(date -u +%FT%TZ) $place: the Newest sort could not be confirmed; not sent" >&2
    rc=1
  elif [ "$MODE" = send ]; then
    # AxioIntel measures this collector's speed from what it receives: reviews per second of
    # collecting. Older copies of the sender do not know the flag.
    local timing=()
    [ "$SENDER_TIMING" = 1 ] && timing=(--collected-seconds "$elapsed")
    if python3 "$PUSHER" "$run/results.jsonl" --place-id "$place" --purpose "$purpose" \
         --url "$INGEST_URL" ${stopped[@]+"${stopped[@]}"} ${timing[@]+"${timing[@]}"}; then
      sent=yes
      date +%s > "$dir/.last_sent"
    else
      rc=1
    fi
  fi
  # One line per place, sent or not: the collector's own record of its speed and coverage.
  summary_line "$(date -u +%FT%TZ)" "$place" "$purpose" "$code" "$elapsed" "${cov_state:-missing}" \
    "${cov_collected:-}" "${cov_reported:-}" "${cov_reason:-}" "${cov_stage:-}" \
    "${cov_rotations:-}" "${cov_blocks:-}" "$sent" "$run/results.jsonl"
  return "$rc"
}

valid_place() { # place purpose -> 0 when both are acceptable
  if ! [[ "$1" =~ ^[A-Za-z0-9_-]{10,255}$ ]]; then
    echo "skipping a line that is not a place ID: $1" >&2
    return 1
  fi
  case "$2" in owned|competitor) return 0 ;; esac
  echo "skipping $1: purpose must be owned or competitor, not $2" >&2
  return 1
}

fetch_targets() { # file -> 0 when AxioIntel answered
  [ -f "$FETCHER" ] || { echo "no target fetcher at $FETCHER; copy scripts/fetch_targets.py from Axio-Backend, or set COLLECTOR_PLACES_FILE" >&2; return 1; }
  if ! python3 "$FETCHER" "$1" --url "$TARGETS_URL"; then
    echo "$(date -u +%FT%TZ) could not get the list of places to collect from $TARGETS_URL" >&2
    return 1
  fi
}

age_of() { # file -> seconds since the timestamp it holds, or a very large number
  if [ -f "$1" ]; then echo $(( $(date +%s) - $(cat "$1" 2>/dev/null || echo 0) )); else echo 999999999; fi
}

run_daemon() {
  local list="$WORK/targets.txt"
  # Stopping the service stops the containers too: a killed shell does not take docker's with it.
  trap 'echo "$(date -u +%FT%TZ) stopping"; docker ps -q --filter "name=axiointel-collect-$$-" | xargs -r docker rm -f >/dev/null; exit 0' TERM INT
  local last_prune=0
  while :; do
    if [ $(( $(date +%s) - last_prune )) -gt 3600 ]; then prune_old_results; last_prune=$(date +%s); fi
    if fetch_targets "$list"; then
      # Requested places first (a person may be waiting), then due ones, in AxioIntel's order.
      # Older fetchers write only "place purpose"; such a line is treated as due.
      local ordered
      ordered=$(awk '!/^#/ && NF { r = ($3 == "" ? 0 : $3); d = ($4 == "" ? 1 : $4); print (r == 1 ? 0 : (d == 1 ? 1 : 2)), NR, $1, $2, r }' "$list" | sort -k1,1n -k2,2n)
      local rank nr place purpose requested
      while read -r rank nr place purpose requested; do
        [ -n "${place:-}" ] || continue
        [ "$rank" -le 1 ] || continue               # neither requested nor due
        valid_place "$place" "${purpose:-owned}" || continue
        [ "$(age_of "$WORK/$place/.last_attempt")" -ge "$RETRY_S" ] || continue
        if [ "$requested" != 1 ] && [ "$(age_of "$WORK/$place/.last_sent")" -lt "$MIN_INTERVAL_S" ]; then
          continue
        fi
        # Wait for a free slot, then start this place in the background.
        while [ "$(jobs -rp | wc -l)" -ge "$PARALLEL" ]; do wait -n 2>/dev/null || true; done
        collect_place "$place" "${purpose:-owned}" &
      done <<<"$ordered"
    fi
    sleep "$POLL" &
    wait $! 2>/dev/null || true
  done
}

if [ "$COMMAND" = daemon ]; then
  run_daemon
  exit 0
fi

prune_old_results

# What to collect. Asked for now rather than read from a file edited by hand here, so a place a
# workspace started watching today is collected tonight. Asking starts nothing: AxioIntel never
# runs, schedules or calls this collector, and the answer is a list.
if [ -z "$PLACES" ]; then
  PLACES="$WORK/targets.txt"
  trap 'rm -f "$WORK/targets.txt"' EXIT
  fetch_targets "$PLACES" || exit 1
fi
[ -f "$PLACES" ] || { echo "no places file at $PLACES" >&2; exit 1; }

failures=0
while read -r place purpose _; do
  case "$place" in ''|\#*) continue ;; esac
  valid_place "$place" "${purpose:-owned}" || { failures=$((failures + 1)); continue; }
  collect_place "$place" "${purpose:-owned}" || failures=$((failures + 1))
done < "$PLACES"

if [ "$failures" -gt 0 ]; then
  echo "$(date -u +%FT%TZ) finished with $failures place(s) not collected or not sent" >&2
  exit 1
fi
echo "$(date -u +%FT%TZ) finished"
