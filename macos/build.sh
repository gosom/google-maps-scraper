#!/bin/bash
# Builds "NNH Maps Scraper.app" (native WebKit window) + NNH-Maps-Scraper-arm64.dmg in the repo root.
# Usage: macos/build.sh            -> reuse existing scraper binary if present, else build it
#        REBUILD_GO=1 macos/build.sh -> force rebuild of the Go binary
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP="$ROOT/NNH Maps Scraper.app"
DMG="$ROOT/NNH-Maps-Scraper-arm64.dmg"
BIN_CACHE="$ROOT/macos/.build/google_maps_scraper"
mkdir -p "$ROOT/macos/.build"

if [ "${REBUILD_GO:-0}" = "1" ] || [ ! -x "$BIN_CACHE" ]; then
  echo "==> building Go scraper (darwin/arm64)"
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
