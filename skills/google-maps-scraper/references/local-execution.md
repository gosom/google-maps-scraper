# Local Execution

## Setup

Agent helpers require Node.js 20+. Prefer a native binary on macOS/Linux; use WSL on Windows. The standalone scraper installer itself needs only Bash, curl, and a SHA-256 utility.

Use the repository's [agent installation guide](https://github.com/gosom/google-maps-scraper/blob/main/INSTALL_FOR_AGENTS.md) when setup is missing. From a checkout, run `bash install.sh`. The installer stores the verified executable path in `${XDG_CONFIG_HOME:-$HOME/.config}/google-maps-scraper/native-executable`.

The run helper defaults to `--backend auto`: use the configured native executable when present, otherwise Docker. Supply `--backend native --executable /absolute/path/to/binary` for an existing binary, or `--backend docker` for an explicit Docker choice. An invalid native configuration is reported rather than silently changing backends.

## Validation run

Create a representative query file. Choose an output directory that does not exist, with an existing parent directory:

```bash
node "$SKILL_DIR/scripts/run-local.mjs" \
  --queries /tmp/gmaps-validation-queries.txt \
  --output-dir /tmp/gmaps-validation-UNIQUE \
  --depth 1
```

Use a distinct name for each run. The helper refuses existing directories unless `--resume` is explicitly supplied. It copies the query file into the run directory and writes `run.json`, private `crawl.log`, results, and resume state there. Never print raw logs without checking for secrets.

When configured, add `--proxy-file "${XDG_CONFIG_HOME:-$HOME/.config}/google-maps-scraper/proxies.txt"`. Credentials are passed by file, never inline. Add `--format json`, `--email`, `--extra-reviews`, `--lang CODE`, `--concurrency NUMBER`, or a positive `--depth` as needed. Extra reviews require JSON.

Native runs use the configured executable. Docker runs use unique container names, host-created private output files, and the locally resolved image ID with its bundled browser runtime. The container retains its default user so Playwright can update browser metadata; opening the existing results file preserves host ownership. The default pulls the image first; `--skip-image-pull` explicitly reuses the local image, including offline. Both backends use conservative concurrency 1, inactivity timeout 3m, and checkpoint support from the first run.

## Status

```bash
node "$SKILL_DIR/scripts/status-local.mjs" --run-dir /tmp/gmaps-validation-UNIQUE
```

The JSON response reports backend, run state, elapsed seconds, complete result count, output path, and exit code when exited. A successful exit with zero results is not successful validation. Inspect a few records for category/location relevance before expanding. Counts exclude unfinished final records while output is being written.

## Full run and recovery

Use a different unused output directory for the full query set. For Docker, add `--skip-image-pull` when validation already checked the image. Native execution needs no image check.

To resume an exited run, repeat its original command with `--resume`; preserve the original options and query file contents. The helper appends missing places using the scraper's resume state. Do not delete results to retry. A run directory lock prevents overlapping retries. If the worker was forcibly killed, inspect the process/container and preserve the directory; do not remove the lock until you have established that the old crawl and supervisor are no longer running. Then remove only the stale `.agent-lock` directory and repeat the original command with `--resume --recover-interrupted`. This flag confirms interrupted-run recovery; known live processes or active containers still block it.

Docker containers are retained for diagnosis after exit. Once inspected, an agent may remove the specific stopped container named in that run's manifest. Never remove a running crawl. Completed runs remain inspectable and resumable after container cleanup using the persisted completion state; Docker connection errors are still reported.

The older `run-local.sh` and `status-local.mjs --output ...` interface remains available for legacy Docker workflows; new workflows should use the run-directory interface above.
