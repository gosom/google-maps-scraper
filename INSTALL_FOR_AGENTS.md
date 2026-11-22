# Install Google Maps Scraper for an agent

Set up the scraper, install its skill, and produce a small useful sample. Prefer a native release binary on macOS/Linux; respect an explicit Docker preference. Native Windows agent execution is not supported yet; use WSL.

## Install the scraper

The standalone installer needs Bash, curl, and `sha256sum` or `shasum`. It does not need Node.js, Go, or Docker.

From a checkout:

```bash
bash install.sh
```

Without a checkout, download the installer to a temporary directory and inspect it before running:

```bash
setup_dir=$(mktemp -d)
curl -fsSL https://raw.githubusercontent.com/gosom/google-maps-scraper/main/install.sh -o "$setup_dir/install.sh"
bash "$setup_dir/install.sh"
```

Use `--version vX.Y.Z` to select a stable release or `--install-dir DIR` to choose the destination. The installer resolves the release, selects the platform asset listed in `checksums.txt`, verifies its SHA-256 digest, and installs Playwright/Chromium. It prints the executable path and saves it in `${XDG_CONFIG_HOME:-$HOME/.config}/google-maps-scraper/native-executable`.

Re-running reuses a binary whose checksum matches. Existing releases without the matching binary and checksums cannot use this installer; explain the limitation and offer Docker. Do not skip checksum verification. Linux browser startup may require system packages; explain the specific missing dependencies and respect the environment's approval requirements for system changes. Browser download success alone does not prove the runtime works.

## Install the agent skill

The scraper installer is independent of Node.js. The current agent helpers and skill installer require Node.js 20 or newer:

```bash
npx skills add gosom/google-maps-scraper
```

Follow the installer's agent selection, locate the installed `google-maps-scraper/SKILL.md`, and read it. Do not assume an agent-specific installation path. If Node.js is missing, explain that it is needed for these helpers; a user can still run the native scraper directly.

When the current agent is known, target it explicitly to avoid installing into unrelated agent directories. For Codex, run this from the user's intended project directory:

```bash
npx --yes skills add gosom/google-maps-scraper --skill google-maps-scraper --agent codex --yes --copy
```

For a local branch test, replace `gosom/google-maps-scraper` with the checkout's absolute path and run from an isolated temporary working directory.

## Verify with real results

Ask only for the missing business type and location. Use a shallow sample and an unused output directory. Invoke `scripts/run-local.mjs` from the installed skill with `--backend native`, or `--backend docker` when selected. Monitor with `scripts/status-local.mjs --run-dir DIR`.

Inspect a few records for the requested category and location. Report the record count, requested field availability, and CSV path. If the request was only a sample, finish there. For a larger request, use the validated setup to continue under the skill workflow.

For branch testing, use the checkout's installer and skill together; do not refresh the installed skill from main during that test.

## Maintainer: publish a release

Update `VERSION` in the Makefile and commit it with the release changes. In GitHub Actions, select **Release → Run workflow** on that commit's branch. There is no version input and pushing a tag does not trigger this workflow.

The workflow reads the Makefile version, runs tests, builds five platform binaries and checksums, creates the matching `vX.Y.Z` tag and a draft release with generated notes, publishes the Docker images from that tag, and publishes the release after both images succeed. Existing Docker Hub credentials are reused. A tag pointing to another commit or an already published release is rejected. If a run fails after preparing a draft, rerun failed jobs for the same commit; the draft and assets are retained for retry.

For a manual release, `make release-checksums VERSION=X.Y.Z` generates `bin/checksums.txt` from existing binaries. Upload it with those exact binaries. This local command does not publish anything.
