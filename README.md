# Google Maps Scraper

<p align="center">
  <a href="https://github.com/gosom/google-maps-scraper/stargazers"><img src="https://img.shields.io/github/stars/gosom/google-maps-scraper?style=social" alt="GitHub Stars"></a>
  <a href="https://github.com/gosom/google-maps-scraper/network/members"><img src="https://img.shields.io/github/forks/gosom/google-maps-scraper?style=social" alt="GitHub Forks"></a>
  <a href="https://twitter.com/intent/tweet?text=Powerful%20open-source%20Google%20Maps%20scraper%20-%20extract%20business%20data%20at%20scale%20with%20CLI%2C%20Web%20UI%2C%20or%20REST%20API&url=https%3A%2F%2Fgithub.com%2Fgosom%2Fgoogle-maps-scraper&hashtags=golang,webscraping,googlemaps,opensource"><img src="https://img.shields.io/twitter/url/http/shields.io.svg?style=social" alt="Tweet"></a>
</p>

[![Build Status](https://github.com/gosom/google-maps-scraper/actions/workflows/build.yml/badge.svg)](https://github.com/gosom/google-maps-scraper/actions/workflows/build.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/gosom/google-maps-scraper)](https://goreportcard.com/report/github.com/gosom/google-maps-scraper)
[![GoDoc](https://godoc.org/github.com/gosom/google-maps-scraper?status.svg)](https://godoc.org/github.com/gosom/google-maps-scraper)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Discord](https://img.shields.io/badge/Discord-Join%20Chat-7289DA?logo=discord&logoColor=white)](https://discord.gg/fpaAVhNCCu)

<p align="center">
  <a href="#quick-start">Quick start</a> ·
  <a href="#use-an-ai-agent">AI agent</a> ·
  <a href="#choose-a-mode">Modes & data</a> ·
  <a href="#documentation">Documentation</a> ·
  <a href="#sponsored-by">Sponsors</a> ·
  <a href="https://discord.gg/fpaAVhNCCu">Community</a>
</p>

**Turn Google Maps searches into business data you can use.**

Get names, phone numbers, websites, ratings, hours, and locations in CSV or JSON. Add website email extraction and reviews when you need more detail.

Free and open source under the MIT license. Run locally with a CLI, Web UI, REST API, or AI coding agent. No Google Maps API key required.

## Featured Sponsor

<table width="100%">
  <tr>
    <td align="left" valign="middle" width="67%">
      <a href="https://www.webshare.io/?referral_code=0q3l81eet8mp">
        <img src="./img/webshare-featured-banner.png" alt="Webshare — Premium proxies for scraping" width="600">
      </a>
    </td>
    <td align="left" valign="middle" width="33%">
      <p>The most affordable premium proxies across 195 countries &amp; 80+ million IPs, plus a FREE plan for new users.</p>
      <a href="https://www.webshare.io/?referral_code=0q3l81eet8mp">Visit Webshare →</a>
    </td>
  </tr>
</table>

## Quick Start

### Use an AI Agent

Paste this into Codex, Claude Code, Cursor, or another coding agent:

```text
Read https://raw.githubusercontent.com/gosom/google-maps-scraper/main/INSTALL_FOR_AGENTS.md and set up Google Maps Scraper and its agent skill. Prefer a compatible native binary. Ask me what businesses and location I want, then run a small sample and show me the CSV.
```

Your agent will help you install the scraper, choose a search, and check a small sample. [Setup details](INSTALL_FOR_AGENTS.md).

### Command Line

With Docker installed, paste these commands into a terminal on macOS, Linux, or Windows with WSL. Change `dentists in Berlin` to your own search.

```bash
mkdir -p gmaps-output
printf '%s\n' 'dentists in Berlin' > gmaps-queries.txt

docker run --rm \
  -v gmaps-playwright-cache:/opt \
  -v "$PWD/gmaps-queries.txt:/queries.txt:ro" \
  -v "$PWD/gmaps-output:/out" \
  gosom/google-maps-scraper \
  -input /queries.txt \
  -results /out/results.csv \
  -depth 1 \
  -exit-on-inactivity 3m
```

Open **`gmaps-output/results.csv`** in your spreadsheet app. The first run downloads the image and browser files. The command exits after three minutes without activity.

Add `-email` to collect emails from business websites. For JSON Lines output (one business per line), replace `-results /out/results.csv` with `-json -results /out/results.jsonl`. The file will be saved as `gmaps-output/results.jsonl`.

**No Docker?** [Download a release binary](https://github.com/gosom/google-maps-scraper/releases/latest) and follow the [binary instructions](#installation). No Go installation is needed.

### Web UI

With Docker installed:

```bash
mkdir -p gmapsdata

docker run --rm \
  -v gmaps-playwright-cache:/opt \
  -v "$PWD/gmapsdata:/gmapsdata" \
  -p 8080:8080 \
  gosom/google-maps-scraper \
  -data-folder /gmapsdata
```

Open **http://localhost:8080**, create a job, and download its results. Your jobs are saved in `gmapsdata`.

<a id="features"></a>

## Choose a Mode

| What you need | Use | How it works |
|---|---|---|
| Business details, with optional reviews and emails | **Default mode** | Opens individual Google Maps listings. Add `-email` to visit business websites or `-extra-reviews` for more reviews. |
| Business lists without opening a browser | **Fast mode** | Fetches search results over HTTP. Requires `-geo`; `-depth` controls result batches. |

Both modes extract names, categories, addresses, phone numbers, websites, hours, ratings, review totals, coordinates, place IDs, listing links, and CIDs when available.

Fast mode includes descriptions, amenities, and booking or ordering links when available. It includes ratings and review totals, but not individual reviews, star-by-star breakdowns, owner details, or emails. See [Fast Mode](#fast-mode) for an example.

Available fields and results depend on what Google returns. A search is not a complete directory of every business in an area.

<details>
<summary><strong>See the scraper in action</strong></summary>

![Google Maps Scraper demo](img/example.gif)

</details>

## Sponsored By

These sponsors help support development and maintenance of Google Maps Scraper.

<table width="100%">
  <tr>
    <td align="center" valign="middle" width="33%">
      <a href="https://www.coreclaw.com/?utm_source=github&amp;utm_medium=referral&amp;utm_campaign=gosom&amp;utm_term=&amp;utm_id=gosom">
        <img src="./img/sponsors/coreclaw.svg" alt="Coreclaw" width="250" height="100"><br>
        <strong>Coreclaw</strong>
      </a>
    </td>
    <td align="center" valign="middle" width="33%">
      <a href="https://evomi.com?utm_source=github&amp;utm_medium=banner&amp;utm_campaign=gosom-maps">
        <img src="./img/sponsors/evomi.svg" alt="Evomi" width="250" height="100"><br>
        <strong>Evomi</strong>
      </a>
    </td>
    <td align="center" valign="middle" width="33%">
      <a href="https://gmapsextractor.com?utm_source=github&amp;utm_medium=banner&amp;utm_campaign=gosom">
        <img src="./img/sponsors/g-maps-extractor.svg" alt="G Maps Extractor" width="250" height="100"><br>
        <strong>G Maps Extractor</strong>
      </a>
    </td>
  </tr>
  <tr>
    <td align="center" valign="middle" width="33%">
      <a href="https://hasdata.com/scrapers/google-maps?utm_source=github&amp;utm_medium=sponsorship&amp;utm_campaign=gosom">
        <img src="./img/sponsors/hasdata.svg" alt="HasData" width="250" height="100"><br>
        <strong>HasData</strong>
      </a>
    </td>
    <td align="center" valign="middle" width="33%">
      <a href="https://go.nodemaven.com/GoogleMapsScrapperoctober">
        <img src="./img/sponsors/nodemaven.svg" alt="NodeMaven" width="250" height="100"><br>
        <strong>NodeMaven</strong>
      </a>
    </td>
    <td align="center" valign="middle" width="33%">
      <a href="https://www.rapidproxy.io/?ref=gosom">
        <img src="./img/sponsors/rapidproxy.svg" alt="RapidProxy" width="250" height="100"><br>
        <strong>RapidProxy</strong>
      </a>
    </td>
  </tr>
  <tr>
    <td align="center" valign="middle" width="33%">
      <a href="https://scrap.io?utm_medium=ads&amp;utm_source=github_gosom_gmap_scraper">
        <img src="./img/sponsors/scrap-io.svg" alt="Scrap.io" width="250" height="100"><br>
        <strong>Scrap.io</strong>
      </a>
    </td>
    <td align="center" valign="middle" width="33%">
      <a href="https://www.searchapi.io/google-maps?via=gosom&amp;utm_source=github&amp;utm_medium=sponsorship&amp;utm_campaign=gosom">
        <img src="./img/sponsors/searchapi.svg" alt="SearchApi" width="250" height="100"><br>
        <strong>SearchApi</strong>
      </a>
    </td>
    <td align="center" valign="middle" width="33%">
      <a href="https://serpapi.com/?utm_source=google-maps-scraper">
        <img src="./img/sponsors/serpapi.svg" alt="SerpApi" width="250" height="100"><br>
        <strong>SerpApi</strong>
      </a>
    </td>
  </tr>
  <tr>
    <td align="center" valign="middle" width="33%">
      <a href="https://talordata.com/?campaignid=f01u8cHondg2qA47&amp;utm_source=github&amp;utm_term=googlemaps">
        <img src="./img/sponsors/talordata.svg" alt="TalorData" width="250" height="100"><br>
        <strong>TalorData</strong>
      </a>
    </td>
    <td align="center" valign="middle" width="33%"><a href="https://github.com/sponsors/gosom">Become a sponsor</a></td>
    <td width="33%"></td>
  </tr>
</table>

[View all sponsors and offers](SPONSORS.md) · [Become a sponsor](https://github.com/sponsors/gosom)

## Documentation

Open a topic below, or use the [recipes](docs/recipes.md) for common workflows.

<a id="installation"></a>

<details>
<summary><strong>Installation: native binary, Docker, or source</strong></summary>

### Download a Binary

1. Open the [latest release](https://github.com/gosom/google-maps-scraper/releases/latest) and expand **Assets**.
2. Download the file for your computer from the table below. Choose the executable, not “Source code”.
3. Put it in a folder for your scraping work. Rename it to `google-maps-scraper` on macOS/Linux or `google-maps-scraper.exe` on Windows.

| Your computer | Filename ends with |
|---|---|
| macOS — Apple Silicon (M1 or newer) | `darwin-arm64` |
| macOS — Intel | `darwin-amd64` |
| Linux — Intel/AMD 64-bit | `linux-amd64` |
| Linux — ARM64 | `linux-arm64` |
| Windows — Intel/AMD 64-bit | `windows-amd64.exe` |

Open a terminal in that folder. The commands below install the browser runtime and run a small search.

**macOS / Linux:**

```bash
chmod +x google-maps-scraper
PLAYWRIGHT_INSTALL_ONLY=1 ./google-maps-scraper
printf '%s\n' 'dentists in Berlin' > queries.txt
./google-maps-scraper -input queries.txt -results results.csv -depth 1 -exit-on-inactivity 3m
```

**Windows PowerShell:**

```powershell
$env:PLAYWRIGHT_INSTALL_ONLY = "1"
.\google-maps-scraper.exe
Remove-Item Env:PLAYWRIGHT_INSTALL_ONLY
'dentists in Berlin' | Set-Content -Encoding ascii queries.txt
.\google-maps-scraper.exe -input queries.txt -results results.csv -depth 1 -exit-on-inactivity 3m
```

Open `results.csv` in your spreadsheet app. To use the Web UI instead, run the executable without arguments and open **http://localhost:8080**.

Linux may need additional browser system libraries. The AI agent helpers require WSL on Windows; the commands above run the standalone Windows binary.

### Automatic Binary Installation (macOS/Linux)

Prefer to let the installer choose the right binary and verify its checksum?

Run these commands in a terminal. You need Bash and curl, plus `sha256sum` or `shasum`.

```bash
setup_dir=$(mktemp -d)
curl -fsSL https://raw.githubusercontent.com/gosom/google-maps-scraper/main/install.sh -o "$setup_dir/install.sh"
bash "$setup_dir/install.sh"
```

The installer downloads a compatible release, checks its SHA-256 checksum, and installs the browser runtime. It prints the executable path. Use that path in place of `./google-maps-scraper` in the examples below.

To choose a version or install folder, add `--version vX.Y.Z` or `--install-dir DIR` to the last command. Releases without matching binaries and checksums are not supported. Linux may need additional browser system libraries. See the [installation guide](INSTALL_FOR_AGENTS.md) for details.

### Using Docker

The published Docker image uses Playwright:

```bash
docker pull gosom/google-maps-scraper
```

### Build from Source

Requirements: Go 1.27.4+

```bash
git clone https://github.com/gosom/google-maps-scraper.git
cd google-maps-scraper
go mod download

go build
./google-maps-scraper -input example-queries.txt -results results.csv -exit-on-inactivity 3m
```

> First run downloads required browser libraries for Playwright.

</details>

<a id="rest-api"></a>

<details>
<summary><strong>REST API</strong></summary>

When running the web server, a full REST API is available:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/jobs` | POST | Create a new scraping job |
| `/api/v1/jobs` | GET | List all jobs |
| `/api/v1/jobs/{id}` | GET | Get job details |
| `/api/v1/jobs/{id}` | DELETE | Delete a job |
| `/api/v1/jobs/{id}/download` | GET | Download results as CSV |

Full OpenAPI 3.0.3 documentation available at http://localhost:8080/api/docs

</details>

<a id="saas-edition"></a>

<details>
<summary><strong>Self-hosted multi-user platform</strong></summary>

Need a multi-user platform with API keys, admin UI, job queue, workers, and cloud provisioning? Use the optional self-hosted SaaS edition:

```bash
curl -fsSL https://raw.githubusercontent.com/gosom/google-maps-scraper/main/PROVISION | sh
```

See [SaaS documentation](docs/saas.md) for deployment and operations details.
There is also a [5-minute deployment walkthrough](https://gosom.dev/deploy-your-own-maps-scraping-api-in-5-minutes/) and a [YouTube video walkthrough](https://www.youtube.com/watch?v=STG9mZw_nac).

More examples are available in [Recipes](docs/recipes.md). If you need proxies for larger jobs, see [Proxy Sponsors](docs/proxies.md).

</details>

<a id="ai-agent-skill"></a>

<details>
<summary><strong>AI agent skill: setup and workflow</strong></summary>

The AI Agent Skill turns a natural-language lead request into a guided local scraping workflow. It is designed for nontechnical users as well as developers and keeps you in control of the search scope, proxy choice, and output.

If you have not installed it yet:

```bash
npx skills add gosom/google-maps-scraper
```

Then just ask your agent:

> Find dentists in Berlin with their emails

The agent will:

1. Check setup and the latest skill; check the image when using Docker.
2. Infer sensible search defaults and ask only for missing essentials.
3. Let you use your own proxy, continue without one, or review one randomly selected proxy sponsor.
4. Collect proxy credentials through a masked local terminal prompt instead of chat.
5. Run a small validation scrape before the full job.
6. Start and monitor the native or Docker crawl in the background.
7. Present the results with options to save, filter, analyze, or export.

Proxy sponsor recommendations are clearly disclosed. One provider is selected per request: Webshare has a 50% chance, and the other active sponsors share the remaining 50% equally. If Webshare is inactive, active sponsors have equal chances; a sole active sponsor is always selected. Any discount or offer is displayed only when it is configured in the skill's [active sponsor registry](skills/google-maps-scraper/references/proxy-sponsors.json). You can always use another provider or no proxy.

The agent helpers require Node.js 20+ and either a native binary or Docker on macOS/Linux, or Windows through WSL. The standalone native installer does not require Node.js. See the [skill definition](skills/google-maps-scraper/SKILL.md) for details.

</details>

<a id="extracted-data-points"></a>

<details>
<summary><strong>Output fields: 37 CSV columns and JSON differences</strong></summary>

The table lists **CSV column names**. Fields may be empty when Google does not provide the data or the selected mode does not collect it.

With `-json`, output is **JSON Lines**: one business object per line, not a JSON array. JSON uses `web_site` instead of `website` and `description` instead of `descriptions`. It also includes `categories` and keeps the legacy `longtitude` key alongside `longitude`. Lists and objects remain structured JSON values; CSV stores them as text.

| # | CSV column | Description |
|---|-------|-------------|
| 1 | `input_id` | Internal identifier for the input query |
| 2 | `link` | Direct URL to the Google Maps listing |
| 3 | `title` | Business name |
| 4 | `category` | Business type (e.g., Restaurant, Hotel) |
| 5 | `address` | Street address |
| 6 | `open_hours` | Operating hours |
| 7 | `popular_times` | Visitor traffic patterns |
| 8 | `website` | Official business website |
| 9 | `phone` | Contact phone number |
| 10 | `plus_code` | Location shortcode |
| 11 | `review_count` | Total number of reviews |
| 12 | `review_rating` | Average star rating |
| 13 | `reviews_per_rating` | Breakdown by star rating |
| 14 | `latitude` | GPS latitude |
| 15 | `longitude` | GPS longitude |
| 16 | `cid` | Google Maps CID identifier |
| 17 | `status` | Business status (open/closed/temporary) |
| 18 | `descriptions` | Business description |
| 19 | `reviews_link` | Direct link to reviews |
| 20 | `thumbnail` | Thumbnail image URL |
| 21 | `timezone` | Business timezone |
| 22 | `price_range` | Price level ($, $$, $$$) |
| 23 | `data_id` | Internal Google Maps identifier |
| 24 | `street_view_url` | Street View URL |
| 25 | `place_id` | Google's unique place id |
| 26 | `images` | Associated image URLs |
| 27 | `reservations` | Reservation booking link |
| 28 | `order_online` | Online ordering link |
| 29 | `menu` | Menu link |
| 30 | `owner` | Owner profile ID, name, and link, when available |
| 31 | `complete_address` | Address parts: street, city, postal code, state, and country |
| 32 | `credit_cards_accepted` | Accepted credit card networks |
| 33 | `about` | Additional business info |
| 34 | `user_reviews` | Customer reviews (text, rating, timestamp) |
| 35 | `user_reviews_extended` | Additional anonymously available reviews (requires `-extra-reviews`) |
| 36 | `emails` | Extracted email addresses (requires `-email` flag) |
| 37 | `review_collection` | Collection status, method, collected/expected counts, and stopping reason (with `-extra-reviews`) |

With `-extra-reviews`, the scraper observes the anonymous browser's own review responses and uses visible review cards as a fallback. It does not sign in. Google may expose only a small sample; increasing scroll depth or retrying cannot guarantee the full reported review count.

`review_collection.status` is `complete` when the collected count reaches the reported total, `limited` when Google explicitly serves a limited view, and `partial` for other stopping conditions. The object includes `method` (`browser`, `dom`, or `none`), `collected`, `expected`, and a `reason` when incomplete. The JSON field is omitted unless extra reviews were requested; the CSV column is appended after `emails`.

Resuming an older CSV preserves its existing columns. Start a new output file to include `review_collection` if the older file does not have that column.

</details>

<details>
<summary><strong>Input formats: search queries, Maps URLs, and custom IDs</strong></summary>

Use one search query per line, such as `dentists in Berlin`.

**Custom Input IDs:** Add your own ID after `#!#`:

```text
Matsuhisa Athens #!#MyCustomID
```

**Direct Google Maps URLs:** Input lines can be regular search queries or direct Google Maps URLs. Supported URL formats include:

```text
https://www.google.com/maps/search/pizza
https://www.google.com/maps/place/Empire+State+Building/@40.7484405,-73.9856632
https://maps.google.com/maps?z=16&q=Empire+State+Building
maps.app.goo.gl/abc123
```

URLs on `google.com` subdomains must include a scheme (`http://` or `https://`) and a `/maps` path. Short `maps.app.goo.gl` links are also supported without a scheme.

</details>

<a id="configuration"></a>
<a id="command-line-options"></a>

<details>
<summary><strong>Command Line Options</strong></summary>

```
Usage: google-maps-scraper [options]

Core Options:
  -input string       Path to input file with queries (one per line)
  -results string     Output file path (default: stdout)
  -json              Output JSON Lines (one business per line) instead of CSV
  -resume            Resume a CLI file scrape by appending missing places
  -depth int         Max scrolls or fast-mode batches (default: 10)
  -c int             Concurrency level (default: half of CPU cores)

Email & Reviews:
  -email             Extract emails from business websites
  -extra-reviews     Collect anonymously available extra reviews and report status

Location Settings:
  -lang string       Language code, e.g., 'de' for German (default: "en")
  -geo string        Coordinates for search, e.g., '37.7749,-122.4194'
  -zoom int          Zoom level 0-21; fast mode requires 1-21 (default: 15)
  -radius float      Keep results within this distance in meters; fast mode only (default: 10000)
  -grid-bbox string  Bounding box for grid scraping, format: "minLat,minLon,maxLat,maxLon"
  -grid-cell float   Grid cell size in km (default: 1.0, used with -grid-bbox)
  -grid-strict       Keep only places whose coordinates are inside -grid-bbox

Web Server:
  -web               Run web server mode
  -addr string       Server address (default: ":8080")
  -data-folder       Data folder for web runner (default: "webdata")

Database:
  -dsn string        PostgreSQL connection string
  -produce           Produce seed jobs only (requires -dsn)

Proxy:
  -proxies string    Comma-separated proxy list
                     Format: protocol://user:pass@host:port
  -proxies-file      Path to a file containing one proxy URL per line

Advanced:
  -exit-on-inactivity duration    Exit after inactivity (e.g., '5m')
  -fast-mode                      Fetch search results without opening a browser
  -debug                          Show browser window
  -writer string                  Custom writer plugin (format: 'dir:pluginName')
  -browser-pool-size int          Number of browser processes to launch (default: 0, derived from -c and -pages-per-browser)
  -pages-per-browser int          Max concurrent pages per browser process (default: 1)

Notes:
  -grid-bbox requires a valid zoom level (1-21)
  -fast-mode cannot be used together with -grid-bbox
```

Run `./google-maps-scraper -h` for the complete list.

</details>
<a id="resuming-interrupted-cli-runs"></a>

<details>
<summary><strong>Resuming Interrupted CLI Runs</strong></summary>

Use `-resume` to continue a CLI file scrape after a crash or manual stop:

```bash
./google-maps-scraper \
  -resume \
  -input queries.txt \
  -results results.csv \
  -depth 10
```

Resume mode reads the existing CSV or JSONL results file, appends new results, and skips places that were already written. It also writes completed input queries to `<results>.resume.json`; future resume runs use that sidecar file to skip fully completed queries.

`-resume` is only supported with regular file output. It requires `-results` to be a file path and does not support `stdout`, `-writer`, or `-fast-mode`. Resume runs should use the same input and scrape options as the original run.

</details>
<a id="using-proxies"></a>

<details>
<summary><strong>Using Proxies</strong></summary>

For larger scraping jobs, proxies help avoid rate limiting. Here's how to configure them:

```bash
./google-maps-scraper \
  -input queries.txt \
  -results results.csv \
  -proxies 'socks5://user:pass@host:port,http://host2:port2' \
  -depth 1 -c 2
```

**Supported protocols:** `socks5`, `socks5h`, `http`, `https`

Current proxy sponsors are listed in [Proxy Sponsors](docs/proxies.md). Using those links helps fund project maintenance.

</details>
<a id="email-extraction"></a>

<details>
<summary><strong>Email Extraction</strong></summary>

Email extraction is **disabled by default**. When enabled, the scraper visits each business website to find email addresses.

```bash
./google-maps-scraper -input queries.txt -results results.csv -email
```

> **Note:** Email extraction increases processing time significantly.

</details>
<a id="fast-mode"></a>

<details>
<summary><strong>Fast Mode</strong></summary>

Fast mode gets business data without opening a browser. After [installing the native binary](#installation), run this example to search for cafés within 5 km of the given San Francisco coordinates:

```bash
printf '%s\n' 'cafes in San Francisco' > queries.txt

./google-maps-scraper \
  -input queries.txt \
  -results results.csv \
  -fast-mode \
  -depth 3 \
  -zoom 15 \
  -radius 5000 \
  -geo '37.7749,-122.4194'
```

- `-geo` sets the search center as latitude,longitude. It is required.
- `-radius` keeps results within the given distance in meters. It applies only to fast mode.
- `-depth 3` requests up to three batches of about 20 results each. The default is 10 batches. Google may return fewer results.
- `-zoom` must be between 1 and 21. The default is 15.

Fast mode includes ratings and review totals, but not individual reviews, star-by-star breakdowns, owner details, or emails. It does not visit business websites.

When available, it also includes descriptions, amenities, booking and ordering links, and opening-status text. A status such as “Closed · Opens 9 AM” describes opening hours; it does not mean the business has closed permanently.

Repeated businesses are removed within each query. Pagination stops when a batch is short, empty, or contains no new businesses. Each batch is sorted by distance; the full output is not globally sorted by distance.

</details>
<a id="grid-scraping-bbox"></a>

<details>
<summary><strong>Grid Scraping (BBox)</strong></summary>

Grid mode splits a bounding box into cells and runs one search per cell. This is useful when a single search does not return enough places.

`queries.txt` example:

```text
cafes in Peristeri, Greece
```

Command example:

```bash
./google-maps-scraper \
  -input queries.txt \
  -results peristeri-cafes.csv \
  -grid-bbox "38.0077,23.6719,38.0257,23.6947" \
  -grid-cell 0.5 \
  -zoom 16 \
  -depth 1 \
  -c 4
```

Notes:
- `-grid-bbox` guides where searches are launched from, but results are not strictly clipped to the box.
- Add `-grid-strict` to keep only places whose coordinates fall inside the box, including its edges. This applies to all output formats. Places outside the box are still fetched before filtering.
- When resuming, keep the same bounding box and strict-mode setting; use a fresh results file if you change either.
- For strict distance filtering, use `-fast-mode` with `-geo` + `-radius` (or post-filter by latitude/longitude).

</details>
<a id="browser-page-concurrency"></a>

<details>
<summary><strong>Browser Page Concurrency</strong></summary>

With the default `-pages-per-browser 1`, each concurrent job (`-c`) effectively uses its own browser process with a single page tab. This can be inefficient because browser pages can be CPU- and memory-heavy, and each browser process adds overhead.

The `-pages-per-browser` flag lets you run multiple page tabs inside the same browser process, reducing overhead. Leave `-browser-pool-size` at `0` to derive the browser count from concurrency and pages per browser, or set it explicitly to cap the number of browser processes independently of concurrency.

**How the flags interact:**

| Flag | What it controls |
|------|------------------|
| `-c` | Total number of concurrent scrape jobs |
| `-browser-pool-size` | Number of browser processes to launch (default: `0`, derived from `-c` and `-pages-per-browser`) |
| `-pages-per-browser` | Number of page tabs per browser process (default: 1) |

When `-pages-per-browser` is greater than 1, the scraper opens multiple tabs within each browser process and routes jobs through a shared page pool. This can significantly increase job throughput on the same hardware.

**Example — 8 concurrent jobs across 2 browsers with 4 tabs each:**

```bash
./google-maps-scraper \
  -c 8 \
  -browser-pool-size 2 \
  -pages-per-browser 4 \
  -input queries.txt \
  -results results.csv \
  -depth 1
```

**Tuning guidance:**

- Start with `-c 4 -browser-pool-size 1 -pages-per-browser 4` for a 4:1 page-to-browser ratio.
- Monitor CPU and RAM usage with `htop` or `docker stats`; browser resource use varies significantly by workload, Chromium version, and page count.
- If memory is the bottleneck, keep `-browser-pool-size` low and reduce `-c` or `-pages-per-browser`.
- If CPU is the bottleneck, reduce the total number of active pages by lowering `-c` or `-pages-per-browser`.
- The product `-browser-pool-size × -pages-per-browser` should roughly equal or exceed `-c` to keep all jobs busy.
- Setting an explicit `-browser-pool-size` is most useful in containerized environments (Docker, Kubernetes) where you want predictable resource usage.

</details>

<a id="advanced-usage"></a>

<details>
<summary><strong>Advanced usage: PostgreSQL, Kubernetes, and plugins</strong></summary>

### PostgreSQL Database Provider

For distributed scraping across multiple machines:

**1. Start PostgreSQL:**
```bash
docker-compose -f docker-compose.dev.yaml up -d
```

**2. Seed the jobs:**
```bash
./google-maps-scraper \
  -dsn "postgres://postgres:postgres@localhost:5432/postgres" \
  -produce \
  -input example-queries.txt \
  -lang en
```

**3. Run scrapers (on multiple machines):**
```bash
./google-maps-scraper \
  -c 2 \
  -depth 1 \
  -dsn "postgres://postgres:postgres@localhost:5432/postgres"
```

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: google-maps-scraper
spec:
  replicas: 3  # Adjust based on needs
  selector:
    matchLabels:
      app: google-maps-scraper
  template:
    metadata:
      labels:
        app: google-maps-scraper
    spec:
      containers:
      - name: google-maps-scraper
        image: gosom/google-maps-scraper:latest
        args: ["-c", "1", "-depth", "10", "-dsn", "postgres://user:pass@host:5432/db"]
        resources:
          requests:
            memory: "512Mi"
            cpu: "500m"
```

> **Note:** The headless browser requires significant CPU/memory resources.

### Custom Writer Plugins

Create custom output handlers using Go plugins:

**1. Write the plugin** (see `examples/plugins/example_writer.go`)

**2. Build:**
```bash
go build -buildmode=plugin -tags=plugin -o myplugin.so myplugin.go
```

**3. Run:**
```bash
./google-maps-scraper -writer ~/plugins:MyWriter -input queries.txt
```

</details>

## Performance

Measured on 2026-11-22 with `cafes in Rhodes`, depth 3, and concurrency 4. Three runs per mode on an Intel Core Ultra 7 155H with 31 GiB RAM, Ubuntu 24.04.5. No proxies, email extraction, or extra review collection.

| Mode | Median time | Time range | Businesses per run |
|---|---:|---:|---:|
| **Fast** | **5.2 seconds** | 4.9–6.0 seconds | 54–55 |
| **Detailed (default)** | **22.6 seconds** | 22.6–24.9 seconds | 28 |

Times include process startup through CSV output and shutdown. Detailed mode also returned review samples, per-star review counts, and owner information. The modes return different businesses and fields; this is not an identical-output comparison.

Results vary by search, network, and machine. See [benchmark commands, conditions, and individual results](docs/benchmarks/README.md).

For larger jobs, increase `-c` gradually and monitor CPU, memory, and failures. Use the [PostgreSQL provider](#advanced-usage) to distribute work across machines.

<details>
<summary><strong>Telemetry and opt-out</strong></summary>

Anonymous usage statistics are collected for improvement purposes. Opt out:
```bash
export DISABLE_TELEMETRY=1
```

</details>

## Support the Project

This project is free and open source. Stars, sponsorships, and sponsor referrals help fund maintenance.

- Star the repository: [github.com/gosom/google-maps-scraper](https://github.com/gosom/google-maps-scraper)
- Sponsor development: [GitHub Sponsors](https://github.com/sponsors/gosom)
- Paid setup and API integration: [Book a call with the maintainer](https://www.cal.eu/g-k-lsoknt/gosom)
- Need proxies? See [Proxy Sponsors](docs/proxies.md)
- Deploying the self-hosted platform? See [SaaS deployment options](docs/saas.md)

---

## Community

[![Discord](https://img.shields.io/badge/Discord-Join%20Our%20Server-7289DA?logo=discord&logoColor=white&style=for-the-badge)](https://discord.gg/fpaAVhNCCu)

Get setup help, share your results, and discuss ideas with other users.

---

## Contributing

Contributions are welcome! Please:

1. Open an issue to discuss your idea
2. Fork the repository
3. Create a pull request

See [AGENTS.md](AGENTS.md) for development guidelines.

---

<details>
<summary><strong>References</strong></summary>

- [How to Extract Data from Google Maps Using Golang](https://blog.gkomninos.com/how-to-extract-data-from-google-maps-using-golang)
- [Distributed Google Maps Scraping](https://blog.gkomninos.com/distributed-google-maps-scraping)
- [Deploy your own Maps scraping API in 5 minutes (includes video walkthrough)](https://gosom.dev/deploy-your-own-maps-scraping-api-in-5-minutes/)
- [Video walkthrough (YouTube)](https://www.youtube.com/watch?v=STG9mZw_nac)
- [scrapemate](https://github.com/gosom/scrapemate) - The underlying web crawling framework
- [omkarcloud/google-maps-scraper](https://github.com/omkarcloud/google-maps-scraper) - Inspiration for JS data extraction

</details>

## License

This project is licensed under the [MIT License](LICENSE).

---

<details>
<summary><strong>Star history</strong></summary>

<a href="https://www.star-history.com/?repos=gosom%2Fgoogle-maps-scraper&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=gosom/google-maps-scraper&type=date&theme=dark&legend=top-left&sealed_token=Yr-6oa5yTlmDhKxqKB2hmq1BmacSXwVftAnHxVCYmn7c4GiYaBtbLGZIZufr71ZfE4jkDpnt75NiAK4fBXP9FPuusVh3L0ofCigowkfI_OxlShvmkys9esorL4X5DRFPCCEVfdt0P0PV-52Qq61fiZSVkUKRjWYT8lhKUkwcXeCH8H8aIbc9NiLItTXz" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=gosom/google-maps-scraper&type=date&legend=top-left&sealed_token=Yr-6oa5yTlmDhKxqKB2hmq1BmacSXwVftAnHxVCYmn7c4GiYaBtbLGZIZufr71ZfE4jkDpnt75NiAK4fBXP9FPuusVh3L0ofCigowkfI_OxlShvmkys9esorL4X5DRFPCCEVfdt0P0PV-52Qq61fiZSVkUKRjWYT8lhKUkwcXeCH8H8aIbc9NiLItTXz" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=gosom/google-maps-scraper&type=date&legend=top-left&sealed_token=Yr-6oa5yTlmDhKxqKB2hmq1BmacSXwVftAnHxVCYmn7c4GiYaBtbLGZIZufr71ZfE4jkDpnt75NiAK4fBXP9FPuusVh3L0ofCigowkfI_OxlShvmkys9esorL4X5DRFPCCEVfdt0P0PV-52Qq61fiZSVkUKRjWYT8lhKUkwcXeCH8H8aIbc9NiLItTXz" />
 </picture>
</a>

</details>

## Legal Notice

Please use this scraper responsibly and in accordance with applicable laws and regulations. Unauthorized scraping may violate terms of service.

---

<p align="center">
  <sub><strong>SPONSOR DISCLAIMER:</strong> Sponsor listings and referral links do not constitute an endorsement. This project and its maintainers are not responsible for sponsors' products, services, representations, conduct, or any resulting loss or damage. Users engage with sponsors at their own risk.</sub>
</p>
