# CLI benchmark

This benchmark compares fast mode with default (detailed) mode on one live Google Maps search. It measures a small local job, not sustained throughput or complete business coverage.

## Run it

Requirements: Go matching `go.mod`, Python 3.10+, and the browser system libraries required by Playwright. Run from the repository root:

```bash
go build -o /tmp/google-maps-scraper-benchmark .
PLAYWRIGHT_INSTALL_ONLY=1 /tmp/google-maps-scraper-benchmark
python3 docs/benchmarks/run.py \
  --binary /tmp/google-maps-scraper-benchmark \
  --output /tmp/google-maps-benchmark-results
```

Choose a new output directory for each benchmark. The script keeps each CSV, process log, command, timing, and exit status. `results.json` contains the machine information, source fingerprints, and summary. A failed, empty, timed-out, or error-logged run stops the benchmark for investigation.

## Test conditions

- Query: `cafes in Rhodes`.
- Search center: `36.4341,28.2176`; zoom 15; language English.
- Depth 3; concurrency 4; default browser pool settings.
- Fast mode radius: 10 km. Default mode does not apply this radius filter.
- Three fresh processes per mode, run sequentially in this order: fast, detailed, detailed, fast, fast, detailed.
- No configured proxies, email extraction, or extra review collection. Telemetry disabled.
- Wall time runs from process launch to exit, including runtime checks, browser startup, requests, parsing, CSV writing, and shutdown. Compilation and explicit browser installation are outside the timer.
- The 60-second inactivity limit is a fallback. Inspect logs to confirm work completed rather than treating an inactivity exit as success. Each process also has a 10-minute timeout.

Depth is not an equal-work control: fast mode requests up to three HTTP batches; detailed mode scrolls search results and visits individual listings. Counts, businesses, and fields can differ. Default mode may include a small review sample even without `-extra-reviews`. Compare the times as examples of two workflows, not an identical-output speed test.

## Recorded run: 2026-11-22

See [machine details and individual measurements](results-2026-11-22.json).

The benchmark includes fast-mode field extraction and pagination. The report records the tested binary and source hashes, along with the base commit, to identify the measured implementation.

Host: Ubuntu 24.04.5 LTS, Intel Core Ultra 7 155H, 22 logical CPUs, approximately 31 GiB RAM. This was a local machine, not an isolated benchmark server. Browser runtime checks were included in the measured process time.

All six runs completed their queued jobs, with no error-level log entries or duplicate place IDs. Google results may change between runs. The sandbox-blocked preflight was not included.
