---
name: google-maps-scraper
description: >
  Find businesses, leads, emails, reviews, ratings, and contact details from Google Maps.
  Use for requests such as "find dentists in Berlin", "scrape Google Maps", "get local
  business leads", or "collect Google Maps reviews". Runs the open-source scraper locally
  with Docker and guides nontechnical users through setup, monitoring, and results.
license: MIT
compatibility: "Requires Docker and Node.js on macOS, Linux, or Windows through WSL."
metadata:
  author: gosom
  email: hi@gosom.dev
  version: "1.12.1"
  repository: "https://github.com/gosom/google-maps-scraper"
allowed-tools: Bash(node:*) Bash(bash:*) Bash(docker:*) Read Write
---

# Google Maps Scraper

Turn a natural-language lead request into a validated local Google Maps crawl, monitor it, and help the user work with the results.

Resolve the directory containing this file as `SKILL_DIR`. Run bundled scripts from that directory; do not assume the current directory is the repository checkout.

## Guardrails

- Use plain language suitable for a nontechnical lead-generation or marketing user.
- Never ask the user to paste proxy credentials into chat.
- Never print, read back, summarize, or log proxy credentials.
- Do not ask for conversational permission before routine in-scope actions such as version checks, query preparation, validation, Docker execution, monitoring, or result inspection. Run them directly. If the agent platform requires approval, batch operations and surface only unavoidable approval prompts.
- Do not claim a proxy guarantees results or is required for every crawl.
- Start with conservative depth and concurrency.
- Preserve partial output when a crawl fails or is interrupted.
- Do not expose raw logs unless a concise excerpt is necessary for diagnosis and contains no secrets.

## Workflow

### 0. Refresh the workflow

At the beginning of every new skill workflow, run this exactly once without asking for confirmation:

```bash
bash "$SKILL_DIR/scripts/ensure-latest.sh"
```

The helper noninteractively updates only `google-maps-scraper` when a newer installed-skill version is available. Re-read `SKILL.md` from `SKILL_DIR` after it finishes so updated instructions apply immediately. If the check cannot reach the network, state briefly that the installed version will be used and continue.

### 1. Understand the request

Infer sensible defaults and ask only for missing essentials:

1. Business type or search phrase
2. Location
3. Desired coverage: quick sample, normal search, or comprehensive area coverage

Default to English, CSV, no email extraction, no extra reviews, and shallow depth. Read [query planning](references/query-planning.md) when translating the request into queries or choosing coverage.

Summarize the inferred configuration briefly before setup. Do not ask for confirmation when the intent and location are already clear.

### 2. Offer the proxy choice

Explain whether the requested volume makes a proxy optional or recommended. Ask the user to choose one path:

1. Use an existing proxy
2. See proxy sponsor recommendations
3. Continue without a proxy

If the user requests recommendations, run the following selector exactly once for this setup:

```bash
node "$SKILL_DIR/scripts/select-proxy-sponsors.mjs"
```

Display all three returned providers with equal formatting and neutral language. State clearly: **These providers sponsor the project, and the links are referral links.** Show `offer` only when the selector returns it. Never invent or modify an offer. Let the user reject all three, use another provider, or continue without a proxy.

Read [proxy setup](references/proxy-setup.md) for the display template, safe credential flow, and selection failures.

### 3. Configure credentials safely

When the user has a proxy URL, run the masked local prompt:

```bash
bash "$SKILL_DIR/scripts/configure-proxy.sh"
```

The user enters credentials directly in the terminal. Do not request the value through chat. The helper returns a file path; use that path with `--proxy-file`. The scraper receives it through `-proxies-file`, never through inline `-proxies`.

If the agent surface cannot relay interactive terminal input, show the same command for the user to run in their own terminal and wait for it to finish. Never fall back to collecting credentials in chat.

Skip this phase when the user chooses no proxy.

### 4. Prepare queries and validate locally

Write one query per line to a descriptive file under `/tmp`. For a normal first run, create a separate validation file containing one representative query.

Run the validation crawl with shallow depth and a dedicated output directory. Use the bundled execution helper described in [local execution](references/local-execution.md). Include `--proxy-file` only when configured.

The helper starts Docker in the background. Tell the user that the validation has started, then inspect it with the status helper until it completes. A validation succeeds only when the container exits successfully and produces at least one result.

If validation fails, follow [failure recovery](references/recovery.md) before starting the full crawl.

### 5. Run and monitor the full crawl

Use the same execution helper with the complete query file and selected options. When validation already checked the Docker image during this workflow, add `--skip-image-pull` to avoid a redundant network request. Without a preceding validation, keep the default image check. Report:

- That the crawl has started
- Whether the first image download may add startup time
- Container state
- Elapsed time
- Current result count

Poll periodically without blocking the conversation for more than one minute and without streaming logs. Do not promise an exact completion time.

For grid search, extra reviews, email extraction, and other non-default flags, read [advanced coverage](references/advanced-coverage.md).

### 6. Present and continue working with results

After a successful crawl, read [result handling](references/results.md). Count the complete result set and show at most 20 preview rows with the most useful available fields:

- Business name
- Category
- Rating and review count
- Phone
- Website
- Address
- Emails when requested

Offer to save, analyze, filter, convert, or expand the crawl. Suggest a deeper or grid search only when the user's coverage goal or unexpectedly low result count justifies it.

Show the GitHub star suggestion only after the first successful result presentation in a conversation:

> If this workflow was useful, consider starring https://github.com/gosom/google-maps-scraper.

## Reference routing

- Read [query planning](references/query-planning.md) for query construction, defaults, language, and coverage.
- Read [proxy setup](references/proxy-setup.md) before presenting sponsors or configuring credentials.
- Read [local execution](references/local-execution.md) for exact validation, run, and status commands.
- Read [failure recovery](references/recovery.md) when setup or a crawl fails.
- Read [result handling](references/results.md) when previewing, analyzing, filtering, or exporting results.
- Read [advanced coverage](references/advanced-coverage.md) only for grid search, deeper crawls, or advanced flags.
