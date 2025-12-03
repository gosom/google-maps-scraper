#!/bin/bash

set -e

if [ -z "$1" ]; then
  echo "Error: query file required"
  echo "Usage: ./scrape.sh <query-file> [options]"
  echo ""
  echo "Available query files in input/:"
  ls -1 input/*.txt 2>/dev/null | xargs -n 1 basename || echo "  (none found)"
  echo ""
  echo "Optional environment variables:"
  echo "  GEO          Geographic coordinates (default: none)"
  echo "  RADIUS       Search radius in meters (default: none)"
  echo "  DEPTH        Search depth (default: 1)"
  echo "  LANG         Language code (default: en)"
  echo "  CONCURRENCY  Number of threads (default: 2)"
  echo "  TIMEOUT      Inactivity timeout (default: 3m)"
  echo ""
  echo "Example:"
  echo "  GEO=\"45.4064,11.8768\" RADIUS=2500 LANG=it ./scrape.sh queries.txt"
  exit 1
fi

QUERY_FILE="$1"
INPUT_PATH="input/$QUERY_FILE"

if [ ! -f "$INPUT_PATH" ]; then
  echo "Error: file $INPUT_PATH not found"
  echo ""
  echo "Available query files in input/:"
  ls -1 input/*.txt 2>/dev/null | xargs -n 1 basename
  exit 1
fi

BASE_NAME=$(basename "$QUERY_FILE" .txt)
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
OUTPUT_FILE="output/${BASE_NAME}_${TIMESTAMP}.csv"

GEO_COORD="${GEO:-}"
SEARCH_RADIUS="${RADIUS:-}"
SEARCH_DEPTH="${DEPTH:-1}"
SEARCH_LANG="${LANG:-en}"
THREAD_COUNT="${CONCURRENCY:-2}"
INACTIVITY_TIMEOUT="${TIMEOUT:-3m}"

echo "Google Maps Scraper"
echo "==================="
echo ""
echo "Input:       $INPUT_PATH"
echo "Output:      $OUTPUT_FILE"
[ -n "$GEO_COORD" ] && echo "Geographic:  $GEO_COORD"
[ -n "$SEARCH_RADIUS" ] && echo "Radius:      ${SEARCH_RADIUS}m"
echo "Depth:       $SEARCH_DEPTH"
echo "Language:    $SEARCH_LANG"
echo "Concurrency: $THREAD_COUNT"
echo "Timeout:     $INACTIVITY_TIMEOUT"
echo ""

CMD_ARGS=(
  -input "$INPUT_PATH"
  -results "$OUTPUT_FILE"
  -depth "$SEARCH_DEPTH"
  -lang "$SEARCH_LANG"
  -c "$THREAD_COUNT"
  -exit-on-inactivity "$INACTIVITY_TIMEOUT"
)

[ -n "$GEO_COORD" ] && CMD_ARGS+=(-geo "$GEO_COORD")
[ -n "$SEARCH_RADIUS" ] && CMD_ARGS+=(-radius "$SEARCH_RADIUS")

./google-maps-scraper "${CMD_ARGS[@]}"

EXIT_CODE=$?

echo ""
if [ $EXIT_CODE -eq 0 ]; then
  echo "Scraping completed successfully"
  echo ""

  if [ -f "$OUTPUT_FILE" ]; then
    TOTAL_ROWS=$(wc -l < "$OUTPUT_FILE" 2>/dev/null || echo "0")
    DATA_ROWS=$((TOTAL_ROWS - 1))
    FILE_SIZE=$(du -h "$OUTPUT_FILE" 2>/dev/null | cut -f1 || echo "unknown")

    echo "Results:"
    echo "  File:    $OUTPUT_FILE"
    echo "  Records: $DATA_ROWS"
    echo "  Size:    $FILE_SIZE"
    echo ""
  fi
else
  echo "Scraping failed with exit code: $EXIT_CODE"
  exit $EXIT_CODE
fi
