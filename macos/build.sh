#!/bin/bash
# Builds "NNH Maps Scraper.app" (native WebKit window) + NNH-Maps-Scraper-arm64.dmg in the repo root.
# Usage: macos/build.sh            -> build the scraper binary if needed, then package it.
#                                     The cached binary at macos/.build/google_maps_scraper is
#                                     reused only when it is newer than every Go source in the
#                                     repo (and than go.mod / go.sum); otherwise it is rebuilt.
#        REBUILD_GO=1 macos/build.sh -> force rebuild of the Go binary
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP="$ROOT/NNH Maps Scraper.app"
DMG="$ROOT/NNH-Maps-Scraper-arm64.dmg"
BIN_CACHE="$ROOT/macos/.build/google_maps_scraper"
mkdir -p "$ROOT/macos/.build"

# Staleness check: the cached binary must be newer than every Go source file in
# the repo plus go.mod/go.sum. `|| true` guards the find|head pipeline against
# set -o pipefail aborting on SIGPIPE / no matches; a missing BIN_CACHE is
# handled below by the `! -x` branch, which rebuilds anyway.
BIN_STALE=0
if [ -x "$BIN_CACHE" ]; then
  NEWER_SRC="$(find "$ROOT" -path "$ROOT/macos/.build" -prune -o -type f -name '*.go' -newer "$BIN_CACHE" -print 2>/dev/null | head -n 1 || true)"
  for dep in "$ROOT/go.mod" "$ROOT/go.sum"; do
    if [ -z "$NEWER_SRC" ] && [ -f "$dep" ] && [ "$dep" -nt "$BIN_CACHE" ]; then
      NEWER_SRC="$dep"
    fi
  done
  if [ -n "$NEWER_SRC" ]; then
    BIN_STALE=1
  fi
fi

if [ "${REBUILD_GO:-0}" = "1" ] || [ ! -x "$BIN_CACHE" ] || [ "$BIN_STALE" = "1" ]; then
  if [ "${REBUILD_GO:-0}" = "1" ]; then
    echo "==> REBUILD_GO=1 set; building Go scraper (darwin/arm64)"
  elif [ ! -x "$BIN_CACHE" ]; then
    echo "==> no cached Go scraper binary; building (darwin/arm64)"
  else
    echo "==> cached binary is older than the Go sources; rebuilding"
  fi
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$BIN_CACHE" .)
fi

echo "==> assembling app bundle"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$ROOT/macos/Info.plist" "$APP/Contents/Info.plist"
cp "$ROOT/macos/theme.css" "$APP/Contents/Resources/theme.css"
cp "$ROOT/macos/assets/AppIcon.icns" "$APP/Contents/Resources/AppIcon.icns"
cp "$BIN_CACHE" "$APP/Contents/MacOS/google_maps_scraper"
xcrun swiftc -O "$ROOT/macos/Launcher.swift" -o "$APP/Contents/MacOS/GoogleMapsScraper" -framework Cocoa -framework WebKit

echo "==> signing (ad-hoc)"
codesign --force --sign - "$APP/Contents/MacOS/google_maps_scraper"
codesign --force --sign - "$APP/Contents/MacOS/GoogleMapsScraper"
codesign --force --sign - "$APP"
codesign --verify --deep --strict "$APP"

echo "==> building DMG"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
ditto "$APP" "$STAGE/NNH Maps Scraper.app"
ln -s /Applications "$STAGE/Applications"
rm -f "$DMG"
hdiutil create -volname "NNH Maps Scraper" -srcfolder "$STAGE" -ov -format UDZO "$DMG" >/dev/null
codesign --force --sign - "$DMG"
hdiutil verify "$DMG" >/dev/null
echo "OK: $APP"
echo "OK: $DMG"
