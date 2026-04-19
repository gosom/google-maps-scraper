# Google Maps Scraper Recipes

Common workflows for lead generation, research, and developer automation.

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

For proxy setup and current proxy sponsors, see [Proxy Sponsors](proxies.md).

More recipes will be expanded in the dedicated recipes task.
