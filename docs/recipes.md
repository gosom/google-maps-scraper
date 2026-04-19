# Google Maps Scraper Recipes

Common workflows for lead generation, research, and developer automation.

## Prepare Queries

```bash
cat > example-queries.txt <<'EOF'
dentists in Berlin Germany
plumbers in Austin Texas
coffee shops in Dublin Ireland
EOF
```

Use natural Google Maps searches. For most local lead-generation jobs, use the business type plus city, region, and country. For larger cities, split the city into neighborhoods if you need broader coverage.

## Basic CSV Lead Scrape

```bash
mkdir -p gmaps-output

docker run \
  -v gmaps-playwright-cache:/opt \
  -v "$PWD/example-queries.txt:/queries.txt:ro" \
  -v "$PWD/gmaps-output:/out" \
  gosom/google-maps-scraper \
  -input /queries.txt \
  -results /out/results.csv \
  -depth 1 \
  -exit-on-inactivity 3m
```

## Extract Emails

Add `-email`:

```bash
docker run \
  -v gmaps-playwright-cache:/opt \
  -v "$PWD/example-queries.txt:/queries.txt:ro" \
  -v "$PWD/gmaps-output:/out" \
  gosom/google-maps-scraper \
  -input /queries.txt \
  -results /out/results.csv \
  -depth 1 \
  -email \
  -exit-on-inactivity 3m
```

Email extraction visits business websites when available, so it is slower than a basic Maps scrape.

## JSON Output and Extra Reviews

```bash
docker run \
  -v gmaps-playwright-cache:/opt \
  -v "$PWD/example-queries.txt:/queries.txt:ro" \
  -v "$PWD/gmaps-output:/out" \
  gosom/google-maps-scraper \
  -input /queries.txt \
  -results /out/results.json \
  -json \
  -extra-reviews \
  -depth 1 \
  -exit-on-inactivity 3m
```

Use JSON when collecting extra reviews.

## Concurrency

`-c` controls how many scrape jobs run in parallel. Higher concurrency can finish large input files faster, but it also uses more CPU/RAM and can increase blocking or failures, especially without proxies. Start with the default for a first run. For larger jobs on a capable machine, try `-c 4`, `-c 8`, or `-c 16` and measure the result.

## Proxies

For proxy setup and current proxy sponsors, see [Proxy Sponsors](proxies.md).

## Grid Scraping

```bash
docker run \
  -v gmaps-playwright-cache:/opt \
  -v "$PWD/example-queries.txt:/queries.txt:ro" \
  -v "$PWD/gmaps-output:/out" \
  gosom/google-maps-scraper \
  -input /queries.txt \
  -results /out/results.csv \
  -depth 5 \
  -grid-bbox "52.34,13.09,52.68,13.76" \
  -grid-cell 1.0 \
  -exit-on-inactivity 3m
```

Grid scraping divides a bounding box into cells for broader area coverage. Smaller cells increase coverage and runtime.

## REST API Automation

Start the Web UI/API server:

```bash
mkdir -p gmapsdata

docker run \
  -v gmaps-playwright-cache:/opt \
  -v "$PWD/gmapsdata:/gmapsdata" \
  -p 8080:8080 \
  gosom/google-maps-scraper \
  -data-folder /gmapsdata
```

Open API docs at `http://localhost:8080/api/docs`.

Client examples are available in `examples/examples-api/`.

## Self-Hosted SaaS Platform

Use the SaaS edition when you need multiple users, API keys, an admin UI, job queue, workers, and cloud provisioning:

```bash
curl -fsSL https://raw.githubusercontent.com/gosom/google-maps-scraper/main/PROVISION | sh
```

See [SaaS documentation](saas.md).

## Docker Mount Notes

The examples mount an output directory:

```bash
-v "$PWD/gmaps-output:/out"
```

This is more reliable than mounting `results.csv` directly. If a host file does not exist, Docker can create it as a directory, which causes `open /results.csv: is a directory`.

The examples also mount a named volume at `/opt`:

```bash
-v gmaps-playwright-cache:/opt
```

This lets Docker reuse Playwright/browser files across runs.
