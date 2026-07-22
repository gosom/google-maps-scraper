#!/usr/bin/env bash

set -uo pipefail

if ! command -v npx >/dev/null 2>&1; then
	echo "Could not check for Agent Skill updates because npx is unavailable. Continuing with the installed version." >&2
	exit 0
fi

if npx --yes skills update google-maps-scraper --yes >/dev/null; then
	echo "Agent Skill version check complete."
	exit 0
fi

echo "Could not check for Agent Skill updates. Continuing with the installed version." >&2
