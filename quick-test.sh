#!/bin/bash

set -e

if [ -z "$1" ]; then
  echo "Error: query file required"
  echo "Usage: ./quick-test.sh <query-file>"
  echo ""
  echo "Available query files in input/:"
  ls -1 input/*.txt 2>/dev/null | xargs -n 1 basename || echo "  (none found)"
  exit 1
fi

QUERY_FILE="$1"
INPUT_PATH="input/$QUERY_FILE"

if [ ! -f "$INPUT_PATH" ]; then
  echo "Error: file $INPUT_PATH not found"
  exit 1
fi

BASE_NAME=$(basename "$QUERY_FILE" .txt)
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
OUTPUT_FILE="output/${BASE_NAME}_test_${TIMESTAMP}.csv"

echo "Quick Test Mode"
echo "==============="
echo ""
echo "Input:  $INPUT_PATH"
echo "Output: $OUTPUT_FILE"
echo ""

./google-maps-scraper \
  -input "$INPUT_PATH" \
  -results "$OUTPUT_FILE" \
  -depth 1 \
  -c 1 \
  -exit-on-inactivity 1m

EXIT_CODE=$?

echo ""
if [ $EXIT_CODE -eq 0 ]; then
  echo "Test completed"
  [ -f "$OUTPUT_FILE" ] && echo "Results: $OUTPUT_FILE"
else
  echo "Test failed with exit code: $EXIT_CODE"
  exit $EXIT_CODE
fi
