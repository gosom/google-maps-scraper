# Google Maps Scraper

[![Build Status](https://github.com/gosom/google-maps-scraper/actions/workflows/build.yml/badge.svg)](https://github.com/gosom/google-maps-scraper/actions/workflows/build.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/gosom/google-maps-scraper)](https://goreportcard.com/report/github.com/gosom/google-maps-scraper)
[![GoDoc](https://godoc.org/github.com/gosom/google-maps-scraper?status.svg)](https://godoc.org/github.com/gosom/google-maps-scraper)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Discord](https://img.shields.io/badge/Discord-Join%20Chat-7289DA?logo=discord&logoColor=white)](https://discord.gg/fpaAVhNCCu)

Extract Google Maps business leads, emails, reviews, phone numbers, websites, ratings, coordinates, and more with a free open-source CLI, Web UI, REST API, and optional self-hosted platform.

Use it for lead generation, local business research, sales prospecting, data enrichment, or developer automation.

| Goal | Start here |
|---|---|
| Get leads into CSV/JSON | [Command Line](#command-line) |
| Ask an AI coding agent to run a scrape | [AI Agent Skill](#ai-agent-skill) |
| Run a browser UI locally | [Web UI](#web-ui) |
| Automate scraping from your app | [REST API](#rest-api) |
| Run a multi-user scraping platform | [SaaS Edition](docs/saas.md) |
| Follow common workflows | [Recipes](docs/recipes.md) |

![Example GIF](img/example.gif)

## Why Use This Scraper?

| | |
|---|---|
| **Completely Free & Open Source** | MIT licensed, no hidden costs or usage limits |
| **Multiple Interfaces** | CLI, Web UI, REST API - use what fits your workflow |
| **High Performance** | ~120 places/minute with optimized concurrency |
| **40+ Output Fields** | Business details, reviews, review-removal notices, emails, coordinates, and more |
| **Production Ready** | Scale from a single machine to Kubernetes clusters |
| **Flexible Output** | CSV, JSON, PostgreSQL, S3, or custom plugins |
| **Proxy Support** | Built-in SOCKS5/HTTP/HTTPS proxy rotation |

---

## Table of Contents

- [Quick Start](#quick-start)
  - [Command Line](#command-line)
  - [Web UI](#web-ui)
  - [REST API](#rest-api)
  - [SaaS Edition](#saas-edition)
- [AI Agent Skill](#ai-agent-skill)
- [Recipes](docs/recipes.md)
- [Installation](#installation)
- [Features](#features)
- [Extracted Data Points](#extracted-data-points)
- [Configuration](#configuration)
  - [Command Line Options](#command-line-options)
  - [Using Proxies](#using-proxies)
  - [Email Extraction](#email-extraction)
  - [Fast Mode](#fast-mode)
- [Advanced Usage](#advanced-usage)
  - [PostgreSQL Database Provider](#postgresql-database-provider)
  - [Kubernetes Deployment](#kubernetes-deployment)
  - [Custom Writer Plugins](#custom-writer-plugins)
- [Performance](#performance)
- [Community](#community)
- [Contributing](#contributing)
- [License](#license)

---

## Quick Start

### Command Line

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

Useful options:

| Need | Flag |
|---|---|
| Extract emails from business websites | `-email` |
| Write JSON instead of CSV | `-json -results /out/results.json` |
| Collect extra reviews | `-extra-reviews -json -results /out/results.json` |
| Increase concurrency | `-c 4`, `-c 8`, or `-c 16` |
| Use proxies | `-proxies "http://user:pass@host:port,socks5://host:port"` |

`-c` controls how many scrape jobs run in parallel. Higher concurrency can finish large input files faster, but it also uses more CPU/RAM and can increase blocking or failures, especially without proxies. Start with the default for a first run. For larger jobs on a capable machine, try `-c 4`, `-c 8`, or `-c 16` and measure the result.

### Web UI

Start the web interface with a single command:

```bash
mkdir -p gmapsdata

docker run \
  -v "$PWD/gmapsdata:/gmapsdata" \
  -p 8080:8080 \
  gosom/google-maps-scraper \
  -data-folder /gmapsdata
```

Then open http://localhost:8080 in your browser.

Or download the [binary release](https://github.com/gosom/google-maps-scraper/releases) for your platform.

> **Note:** Results take at least 3 minutes to appear (minimum configured runtime).
> 
> **macOS Users:** Docker command may not work. See [MacOS Instructions](MacOS%20instructions.md).

### REST API

When running the web server, a full REST API is available:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/jobs` | POST | Create a new scraping job |
| `/api/v1/jobs` | GET | List all jobs |
| `/api/v1/jobs/{id}` | GET | Get job details |
| `/api/v1/jobs/{id}` | DELETE | Delete a job |
| `/api/v1/jobs/{id}/download` | GET | Download results as CSV |

Full OpenAPI 3.0.3 documentation available at http://localhost:8080/api/docs

### SaaS Edition

Need a multi-user platform with API keys, admin UI, job queue, workers, and cloud provisioning? Use the optional self-hosted SaaS edition:

```bash
curl -fsSL https://raw.githubusercontent.com/gosom/google-maps-scraper/main/PROVISION | sh
```

See [SaaS documentation](docs/saas.md) for deployment and operations details.
There is also a [5-minute deployment walkthrough](https://gosom.dev/deploy-your-own-maps-scraping-api-in-5-minutes/) and a [YouTube video walkthrough](https://www.youtube.com/watch?v=STG9mZw_nac).

More examples are available in [Recipes](docs/recipes.md).

---

## AI Agent Skill

Use Google Maps Scraper directly from AI coding agents like [Claude Code](https://claude.com/claude-code), Cursor, GitHub Copilot, and [20+ other agents](https://agentskills.io). Just tell your agent to find businesses and it handles everything — query creation, scraping, and result analysis.

**Install the skill:**

```bash
npx skills add gosom/google-maps-scraper
```

**Then just ask your agent:**

> Find me all dentists in Berlin with their emails

The agent will ask you a few setup questions, run the scraper in the background via Docker, and present the results with options to save, filter, analyze, or export.

Requires Docker installed and running. See the [skill definition](skills/google-maps-scraper/SKILL.md) for details.

---

## Installation

### Using Docker (Recommended)

The published Docker image uses Playwright:

```bash
docker pull gosom/google-maps-scraper
```

### Build from Source

Requirements: Go 1.25.6+

```bash
git clone https://github.com/gosom/google-maps-scraper.git
cd google-maps-scraper
go mod download

go build
./google-maps-scraper -input example-queries.txt -results results.csv -exit-on-inactivity 3m
```

> First run downloads required browser libraries for Playwright.

---

## Features

| Feature | Description |
|---------|-------------|
| **40+ Output Fields** | Business name, address, phone, website, reviews, review-removal notices, coordinates, and more |
| **Email Extraction** | Optional crawling of business websites for email addresses |
| **Multiple Output Formats** | CSV, JSON, PostgreSQL, S3, or custom plugins |
| **Proxy Support** | SOCKS5, HTTP, HTTPS with authentication |
| **Scalable Architecture** | Single machine to Kubernetes cluster |
| **REST API** | Programmatic control for automation |
| **Web UI** | User-friendly browser interface |
| **Fast Mode (Beta)** | Quick extraction of up to 21 results per query |
| **AWS Lambda** | Serverless execution support (experimental) |

---

## Extracted Data Points

<details>
<summary><strong>Click to expand available output fields</strong></summary>

| # | Field | Description |
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
| 14 | `reviews_1_star` | One-star review count, flattened for CSV output |
| 15 | `reviews_2_star` | Two-star review count, flattened for CSV output |
| 16 | `reviews_3_star` | Three-star review count, flattened for CSV output |
| 17 | `reviews_4_star` | Four-star review count, flattened for CSV output |
| 18 | `reviews_5_star` | Five-star review count, flattened for CSV output |
| 19 | `removed_reviews_min` | Lower bound of reviews Google reports as removed under a review-removal notice |
| 20 | `removed_reviews_max` | Upper bound of reviews Google reports as removed; `0` means no upper bound was present |
| 21 | `latitude` | GPS latitude |
| 22 | `longitude` | GPS longitude |
| 23 | `cid` | Google's unique Customer ID |
| 24 | `status` | Business status (open/closed/temporary) |
| 25 | `descriptions` | Business description |
| 26 | `reviews_link` | Direct link to reviews |
| 27 | `thumbnail` | Thumbnail image URL |
| 28 | `timezone` | Business timezone |
| 29 | `price_range` | Price level ($, $$, $$$) |
| 30 | `data_id` | Internal Google Maps identifier |
| 31 | `images` | Associated image URLs |
| 32 | `reservations` | Reservation booking link |
| 33 | `order_online` | Online ordering link |
| 34 | `menu` | Menu link |
| 35 | `owner` | Owner-claimed status |
| 36 | `complete_address` | Full formatted address |
| 37 | `about` | Additional business info |
| 38 | `user_reviews` | Customer reviews (text, rating, timestamp) |
| 39 | `emails` | Extracted email addresses (requires `-email` flag) |
| 40 | `user_reviews_extended` | Extended reviews up to ~300 (requires `-extra-reviews`) |
| 41 | `place_id` | Google's unique place id |

</details>

The `removed_reviews_min` and `removed_reviews_max` fields are parsed from Google Maps review-removal notices when Google includes that notice in the Maps payload. For example, a notice such as `201 to 250 reviews were removed` is exported as `removed_reviews_min=201` and `removed_reviews_max=250`; an open-ended notice such as `over 250` is exported as `removed_reviews_min=251` and `removed_reviews_max=0`. When no notice is present, both fields remain `0`.

JSON output includes the full `reviews_per_rating` map plus `removed_reviews_min` and `removed_reviews_max`. CSV output also includes the flattened `reviews_1_star` through `reviews_5_star` columns for easier spreadsheet analysis.

**Custom Input IDs:** Define your own IDs in the input file:
```
Matsuhisa Athens #!#MyCustomID
```

---

## Configuration

### Command Line Options

```
Usage: google-maps-scraper [options]

Core Options:
  -input string       Path to input file with queries (one per line)
  -results string     Output file path (default: stdout)
  -json              Output JSON instead of CSV
  -depth int         Max scroll depth in results (default: 10)
  -c int             Concurrency level (default: half of CPU cores)

Email & Reviews:
  -email             Extract emails from business websites
  -extra-reviews     Collect extended reviews (up to ~300)

Location Settings:
  -lang string       Language code, e.g., 'de' for German (default: "en")
  -geo string        Coordinates for search, e.g., '37.7749,-122.4194'
  -zoom int          Zoom level 0-21 (default: 15)
  -radius float      Search radius in meters (default: 10000)
  -grid-bbox string  Bounding box for grid scraping, format: "minLat,minLon,maxLat,maxLon"
  -grid-cell float   Grid cell size in km (default: 1.0, used with -grid-bbox)

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

Advanced:
  -exit-on-inactivity duration    Exit after inactivity (e.g., '5m')
  -fast-mode                      Quick mode with reduced data
  -debug                          Show browser window
  -writer string                  Custom writer plugin (format: 'dir:pluginName')

Notes:
  -grid-bbox requires a valid zoom level (1-21)
  -fast-mode cannot be used together with -grid-bbox
```

Run `./google-maps-scraper -h` for the complete list.

### Using Proxies

For larger scraping jobs, proxies help avoid rate limiting. Here's how to configure them:

```bash
./google-maps-scraper \
  -input queries.txt \
  -results results.csv \
  -proxies 'socks5://user:pass@host:port,http://host2:port2' \
  -depth 1 -c 2
```

**Supported protocols:** `socks5`, `socks5h`, `http`, `https`

### Email Extraction

Email extraction is **disabled by default**. When enabled, the scraper visits each business website to find email addresses.

```bash
./google-maps-scraper -input queries.txt -results results.csv -email
```

> **Note:** Email extraction increases processing time significantly.

### Fast Mode

Fast mode returns up to 21 results per query, ordered by distance. Useful for quick data collection with basic fields.

```bash
./google-maps-scraper \
  -input queries.txt \
  -results results.csv \
  -fast-mode \
  -zoom 15 \
  -radius 5000 \
  -geo '37.7749,-122.4194'
```

> **Warning:** Fast mode is in Beta. You may experience blocking.

### Grid Scraping (BBox)

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
- For strict distance filtering, use `-fast-mode` with `-geo` + `-radius` (or post-filter by latitude/longitude).

---

## Advanced Usage

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

---

## Performance

**Expected throughput:** ~120 places/minute (with `-c 8 -depth 1`)

| Keywords | Results/Keyword | Total Jobs | Estimated Time |
|----------|-----------------|------------|----------------|
| 100 | 16 | 1,600 | ~13 minutes |
| 1,000 | 16 | 16,000 | ~2.5 hours |
| 10,000 | 16 | 160,000 | ~22 hours |

For large-scale scraping, use the PostgreSQL provider with Kubernetes.

### Telemetry

Anonymous usage statistics are collected for improvement purposes. Opt out:
```bash
export DISABLE_TELEMETRY=1
```

## Community

[![Discord](https://img.shields.io/badge/Discord-Join%20Our%20Server-7289DA?logo=discord&logoColor=white&style=for-the-badge)](https://discord.gg/fpaAVhNCCu)

Join our Discord to:
- Get help with setup and configuration
- Share your use cases and success stories
- Request features and report bugs
- Connect with other users

---

## Contributing

Contributions are welcome! Please:

1. Open an issue to discuss your idea
2. Fork the repository
3. Create a pull request

See [AGENTS.md](AGENTS.md) for development guidelines.

---

## References

- [How to Extract Data from Google Maps Using Golang](https://blog.gkomninos.com/how-to-extract-data-from-google-maps-using-golang)
- [Distributed Google Maps Scraping](https://blog.gkomninos.com/distributed-google-maps-scraping)
- [Deploy your own Maps scraping API in 5 minutes (includes video walkthrough)](https://gosom.dev/deploy-your-own-maps-scraping-api-in-5-minutes/)
- [Video walkthrough (YouTube)](https://www.youtube.com/watch?v=STG9mZw_nac)
- [scrapemate](https://github.com/gosom/scrapemate) - The underlying web crawling framework
- [omkarcloud/google-maps-scraper](https://github.com/omkarcloud/google-maps-scraper) - Inspiration for JS data extraction

---

## License

This project is licensed under the [MIT License](LICENSE).

## Legal Notice

Please use this scraper responsibly and in accordance with applicable laws and regulations. Unauthorized scraping may violate terms of service.

---

<p align="center">
  <sub>Banner generated using OpenAI's DALL-E</sub>
</p>
