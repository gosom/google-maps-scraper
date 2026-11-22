#!/usr/bin/env bash
set -euo pipefail

# Standalone installer: no Node.js, Python, Go, or Docker required.
version="latest"
install_dir="${HOME}/.local/share/google-maps-scraper/bin"
while (($#)); do
  case "$1" in
    --version|--install-dir)
      if (($# < 2)) || [[ -z "$2" ]]; then echo "Missing value for $1" >&2; exit 2; fi
      if [[ "$1" == --version ]]; then version="$2"; else install_dir="$2"; fi
      shift 2 ;;
    --help|-h)
      echo "Usage: bash install.sh [--version vX.Y.Z] [--install-dir DIR]"; exit 0 ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done

case "$(uname -s)" in
  Linux) platform=linux ;;
  Darwin) platform=darwin ;;
  *) echo "Native setup supports macOS/Linux. Use WSL or Docker on other platforms." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) architecture=amd64 ;;
  arm64|aarch64) architecture=arm64 ;;
  *) echo "Unsupported CPU architecture; use a supported Docker host." >&2; exit 1 ;;
esac
command -v curl >/dev/null || { echo "Install curl and rerun setup." >&2; exit 1; }
if command -v sha256sum >/dev/null; then
  checksum() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null; then
  checksum() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  echo "Install sha256sum or shasum to verify the download." >&2; exit 1
fi

repository=https://github.com/gosom/google-maps-scraper
if [[ "$version" == latest ]]; then
  release_url=$(curl --proto '=https' --proto-redir '=https' -fsSL --connect-timeout 15 --max-time 60 -o /dev/null -w '%{url_effective}' "$repository/releases/latest")
  case "$release_url" in
    "$repository/releases/tag/"*) version=${release_url##*/} ;;
    *) echo "Unable to resolve the latest release." >&2; exit 1 ;;
  esac
fi
if [[ ! "$version" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]]; then echo "Expected a stable version such as v1.19.1." >&2; exit 2; fi
version="v${version#v}"
asset="google_maps_scraper-${version#v}-${platform}-${architecture}"
mkdir -p "$install_dir"
install_dir=$(cd "$install_dir" && pwd)
temporary=$(mktemp -d "$install_dir/.install-XXXXXX")
trap 'rm -rf "$temporary"' EXIT
download() { curl --proto '=https' --proto-redir '=https' -fsSL --connect-timeout 15 --max-time 180 --retry 2 "$repository/releases/download/$version/$1" -o "$2"; }
if ! download checksums.txt "$temporary/checksums.txt"; then
  echo "Release $version does not provide an accessible checksums.txt. Choose a release with verified native assets or use Docker." >&2; exit 1
fi
expected=$(awk -v asset="$asset" '$2 == asset || $2 == "*" asset {print $1}' "$temporary/checksums.txt")
if [[ ! "$expected" =~ ^[a-fA-F0-9]{64}$ ]]; then
  echo "Release $version has no checksum for $asset. Use another release or Docker." >&2; exit 1
fi
expected=$(printf '%s' "$expected" | tr 'A-F' 'a-f')
executable="$install_dir/$asset"
if [[ ! -f "$executable" ]] || [[ "$(checksum "$executable")" != "$expected" ]]; then
  download "$asset" "$temporary/scraper"
  if [[ "$(checksum "$temporary/scraper")" != "$expected" ]]; then echo "Checksum mismatch; installation stopped." >&2; exit 1; fi
  chmod 755 "$temporary/scraper"
  if [[ "$("$temporary/scraper" -version)" != "$version" ]]; then echo "Binary reports an unexpected version." >&2; exit 1; fi
  mv "$temporary/scraper" "$executable"
fi
chmod 755 "$executable"
if ! PLAYWRIGHT_INSTALL_ONLY=1 "$executable" >&2; then
  echo "Browser setup failed. Resolve the reported network/dependency error and rerun. Linux may need system libraries; Docker is an alternative." >&2; exit 1
fi
config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/google-maps-scraper"
mkdir -p "$config_dir"
umask 077
printf '%s\n' "$executable" > "$config_dir/native-executable"
printf '%s\n' "$executable"
echo "Installed $version. Run a small sample to verify browser startup and results." >&2
