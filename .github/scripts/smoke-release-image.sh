#!/usr/bin/env bash
set -euo pipefail

kind=$1
image=$2
architecture=$3
docker pull --platform "linux/$architecture" "$image"
[[ "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" == "linux/$architecture" ]]

case "$kind" in
  scraper)
    docker run --rm --network none "$image" -version
    # Exercise the bundled driver and browser together, without downloading anything.
    docker run --rm --network none --entrypoint /bin/sh "$image" -ec '
      "$PLAYWRIGHT_DRIVER_PATH/node" "$PLAYWRIGHT_DRIVER_PATH/package/cli.js" \
        screenshot --browser chromium about:blank /tmp/smoke.png
      test -s /tmp/smoke.png
    '
    ;;
  saas)
    docker run --rm --network none "$image" --help
    ;;
  *)
    echo "Unknown image kind: $kind" >&2
    exit 1
    ;;
esac
