# Local Execution

## Validation run

Create one representative query file and a validation output directory. Run:

```bash
bash "$SKILL_DIR/scripts/run-local.sh" \
  --queries /tmp/gmaps-validation-queries.txt \
  --output-dir /tmp/gmaps-validation-output \
  --depth 1
```

When configured, add:

```bash
--proxy-file "${XDG_CONFIG_HOME:-$HOME/.config}/google-maps-scraper/proxies.txt"
```

Add `--format json`, `--email`, `--extra-reviews`, `--lang CODE`, or a different positive depth only when selected. Extra reviews require JSON.

The helper uses container `gmaps-scraper-agent`, volume `gmaps-playwright-cache`, image `gosom/google-maps-scraper`, and `-exit-on-inactivity 3m`. By default it pulls the image before starting, so validation checks for the latest release. It retries one failed pull. When a local image already exists and both checks fail, it warns and continues offline; without a local image, it stops with an actionable error.

## Status

For CSV:

```bash
node "$SKILL_DIR/scripts/status-local.mjs" \
  --output /tmp/gmaps-validation-output/results.csv \
  --format csv
```

For JSON, point to `results.json` and use `--format json`. The helper returns JSON with container state, elapsed seconds, result count, output path, and exit code when completed.

## Full run

After validation succeeds, run the complete query file with a descriptive output directory and add `--skip-image-pull`; validation already checked the image during this workflow. The helper safely removes only a stopped container named `gmaps-scraper-agent`; it refuses to replace a running crawl.

Do not use `--dry-run` for an actual crawl. `--dry-run` exists for diagnostics and prints a command containing the proxy file path, never its contents.
