#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$HOME"

# Run scraper in background and get its PID
"$DIR/google_maps_scraper" > "$HOME/google_maps_scraper.log" 2>&1 &
SCRAPER_PID=$!

# Open browser
sleep 2
open http://localhost:8080

# Idle timeout in seconds
IDLE_TIMEOUT=6000

# Wait and kill
sleep $IDLE_TIMEOUT

# Check if still running, and kill it
if ps -p $SCRAPER_PID > /dev/null; then
  echo "Stopping scraper after $IDLE_TIMEOUT seconds of idle time."
  kill $SCRAPER_PID
fi