#!/usr/bin/env bash

set -euo pipefail

config_root=${XDG_CONFIG_HOME:-"$HOME/.config"}
config_dir="$config_root/google-maps-scraper"
proxy_file="$config_dir/proxies.txt"

mkdir -p "$config_dir"
umask 077

temporary_file=$(mktemp "$config_dir/.proxies.XXXXXX")
cleanup() {
	rm -f "$temporary_file"
}
trap cleanup EXIT

proxy_count=0

while true; do
	printf 'Proxy URL (blank to finish): ' >&2
	if ! IFS= read -r -s proxy_url; then
		printf '\n' >&2
		break
	fi
	printf '\n' >&2

	if [[ -z "$proxy_url" ]]; then
		break
	fi

	case "$proxy_url" in
		http://* | https://* | socks5://* | socks5h://*) ;;
		*)
			echo "Proxy URL must use http, https, socks5, or socks5h." >&2
			exit 1
			;;
	esac

	printf '%s\n' "$proxy_url" >> "$temporary_file"
	proxy_count=$((proxy_count + 1))
done

if ((proxy_count == 0)); then
	echo "No proxy URLs were entered; the existing configuration was not changed." >&2
	exit 1
fi

chmod 600 "$temporary_file"
mv "$temporary_file" "$proxy_file"
trap - EXIT

printf 'Proxy configuration saved to %s\n' "$proxy_file"
printf 'Remove it with: rm %q\n' "$proxy_file"
