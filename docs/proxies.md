# Proxy Sponsors

Google Maps scraping can trigger rate limits or blocking, especially with larger jobs or higher concurrency. Proxies can help, but they are not a guarantee. Proxy quality, geography, concurrency, query volume, and Google behavior all affect reliability.

This page lists current proxy sponsors and supporters of this project. Using these links helps fund maintenance.

## Configure Proxies

Use `-proxies` with a comma-separated list:

```bash
./google-maps-scraper \
  -input queries.txt \
  -results results.csv \
  -proxies "socks5://user:pass@host:port,http://host2:port2" \
  -depth 1
```

Supported protocols: `socks5`, `socks5h`, `http`, `https`.

## Docker Example

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
  -proxies "http://user:pass@host:port,socks5://host:port" \
  -exit-on-inactivity 3m
```

## Current Proxy Sponsors

| Provider | Notes | Link |
|---|---|---|
| RapidProxy | Residential proxy provider supporting this project | [Visit RapidProxy](https://www.rapidproxy.io/?ref=gosom) |
| Webshare | Proxy provider with HTTP and SOCKS5 support | [Visit Webshare](https://www.webshare.io/?referral_code=0q3l81eet8mp) |
| Legion Proxy | Residential proxy provider supporting this project | [Visit Legion Proxy](https://legionproxy.io/?utm_source=github&utm_campaign=gmaps) |
| Decodo | Proxy provider supporting this project | [Visit Decodo](https://visit.decodo.com/APVbbx) |
| Evomi | Proxy provider supporting this project | [Visit Evomi](https://evomi.com?utm_source=github&utm_medium=banner&utm_campaign=gosom-maps) |

## Practical Notes

- Test with a small input file before running a large job.
- Increase concurrency gradually.
- If results become less reliable, reduce `-c` before assuming the proxy provider is the only issue.
- Keep proxy URLs private. Do not commit credentials.
