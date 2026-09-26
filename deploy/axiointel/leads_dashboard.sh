#!/usr/bin/env bash
# The lead-finding dashboard: Axio-CRED's own web UI (the collector's -web mode) on the collector's
# machine: Scrape, Leads (with Saleshandy) and the AxioIntel spec.
#
# It listens on this machine's localhost only and has no login, so it is never reachable from the
# internet: open it through an SSH tunnel from your own computer --
#     ssh -L 8080:localhost:8080 axio-collector      then browse to http://localhost:8080
# Nothing here talks to AxioIntel.
#
# Usage: leads_dashboard.sh start|stop|restart|update|status|logs
#   update   pull the repository, rebuild the image, and restart the dashboard on it
#
# Configuration (environment):
#   LEADS_IMAGE      default axio-cred-collector
#   LEADS_DATA_DIR   jobs, results and the dashboard's database; default /var/lib/axio-leads
#   LEADS_PORT       default 8080 (bound to 127.0.0.1 only)
#   AXIO_CRED_DIR    the repository checkout; default /opt/axio-cred
#   LEADS_ENV_FILE   secrets for the dashboard; default /etc/axiointel/leads.env (root:docker, 640)
#                    SALESHANDY_API_KEY=...   turns on "Send to Saleshandy"
#                    LEADS_PAUSE_WINDOW="08:00-20:00 America/New_York"  optional: no lead job runs in
#                                             this daily window, and one running when it opens is
#                                             stopped and queued again. Unset since 27 Sep 2026: lead
#                                             jobs run all day at low priority instead (below).
#                    LEADS_YIELD_FILE   optional; default $LEADS_DATA_DIR/YIELD, which the AxioIntel
#                                       collector daemon (collect.sh, COLLECTOR_YIELD_FILE) writes
#                                       the instant a customer's pull is waiting and removes once
#                                       none is. While it is present and fresh, no lead job starts
#                                       and a running one is stopped and requeued (27 Sep 2026).
#                    WHATSAPP_TOKEN=...                 with the two ids below, turns on WhatsApp
#                    WHATSAPP_PHONE_NUMBER_ID=...       template sending (Meta WhatsApp Cloud API):
#                    WHATSAPP_BUSINESS_ACCOUNT_ID=...   a system user's token, the sending number's id
#                                                       and the WABA id. Opted-in leads only.
#   LEADS_CONCURRENCY listings worked on at once; default 3
#   LEADS_CPU_SHARES  the dashboard's CPU weight against AxioIntel's review collection (Docker's
#                    default, 1024); default 256, so collection gets four times the CPU when both
#                    want it and lead jobs use whatever it leaves (owner's decision, 27 Sep 2026)
#   LEADS_MEMORY     the most memory lead jobs may take; default 2g of the server's 4 GB, and under
#                    memory pressure the kernel stops a lead job before a review collection
#   LEADS_PROXIES    proxies for Google; default /etc/axiointel/proxies.txt (root:docker, 640), one
#                    http://user:pass@host:port per line. When present, every job's Google traffic
#                    goes through them; business websites (emails) are always fetched directly.
#
# Which Saleshandy campaigns leads may go into is $LEADS_DATA_DIR/saleshandy.json:
#     {"sequences": ["<campaign title>", ...], "routes": {"<category word>": "<campaign title>"}}
# Nothing can be sent until it lists at least one. After changing either file: restart.
set -euo pipefail

IMAGE="${LEADS_IMAGE:-axio-cred-collector}"
DATA="${LEADS_DATA_DIR:-/var/lib/axio-leads}"
PORT="${LEADS_PORT:-8080}"
REPO="${AXIO_CRED_DIR:-/opt/axio-cred}"
NAME=axio-leads
ENV_FILE="${LEADS_ENV_FILE:-/etc/axiointel/leads.env}"
PROXIES="${LEADS_PROXIES:-/etc/axiointel/proxies.txt}"
# Listings worked on at once. Each is a browser tab (~200-300 MB); 3 suits the 2-vCPU, 4 GB server.
CONCURRENCY="${LEADS_CONCURRENCY:-3}"
# AxioIntel's review collection comes first; lead jobs never stop for it, they just get less.
CPU_SHARES="${LEADS_CPU_SHARES:-256}"
MEMORY="${LEADS_MEMORY:-2g}"

start() {
  sudo mkdir -p "$DATA"
  if docker ps -a --format '{{.Names}}' | grep -qx "$NAME"; then
    docker start "$NAME" >/dev/null
  else
    env_args=()
    [ -r "$ENV_FILE" ] && env_args=(--env-file "$ENV_FILE")
    proxy_mount=()
    proxy_flag=()
    if [ -r "$PROXIES" ] && [ -s "$PROXIES" ]; then
      proxy_mount=(-v "$PROXIES:/run/proxies.txt:ro")
      proxy_flag=(-proxies-file /run/proxies.txt)
    fi
    docker run -d --name "$NAME" --restart unless-stopped \
      --cpu-shares "$CPU_SHARES" --memory "$MEMORY" --oom-score-adj 500 \
      --log-opt max-size=50m --log-opt max-file=3 \
      -e DISABLE_TELEMETRY=1 ${env_args[@]+"${env_args[@]}"} \
      -p "127.0.0.1:$PORT:8080" \
      -v "$DATA:/data" ${proxy_mount[@]+"${proxy_mount[@]}"} \
      "$IMAGE" -web -data-folder /data -addr :8080 -c "$CONCURRENCY" ${proxy_flag[@]+"${proxy_flag[@]}"} >/dev/null
    [ ${#proxy_flag[@]} -gt 0 ] && echo "using $(grep -c . "$PROXIES") proxies for Google"
  fi
  echo "dashboard running on this machine's localhost:$PORT"
  echo "from your computer: ssh -L $PORT:localhost:$PORT axio-collector   then open http://localhost:$PORT"
}

stop() {
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  echo "dashboard stopped (jobs and results kept in $DATA)"
}

case "${1:-status}" in
  start) start ;;
  stop) stop ;;
  restart) stop; start ;;
  update)
    git -C "$REPO" pull --ff-only
    (cd "$REPO" && docker build -t "$IMAGE" .)
    stop
    start ;;
  status) docker ps --filter "name=^$NAME$" --format '{{.Names}}  {{.Status}}  {{.Ports}}' ;;
  logs) docker logs --tail 100 -f "$NAME" ;;
  *) echo "usage: $0 start|stop|restart|update|status|logs" >&2; exit 1 ;;
esac
