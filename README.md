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

**A powerful, free, and open-source Google Maps scraper** for extracting business data at scale. Available as CLI, Web UI, REST API, or deployable to Kubernetes/AWS Lambda.

![Example GIF](img/example.gif)

> **Love this project?** A star helps others discover it and motivates continued development. [Become a sponsor](https://github.com/sponsors/gosom) to directly support new features and maintenance.

---

## Sponsored By

<p align="center"><i>This project is made possible by our amazing sponsors</i></p>

### [Scrap.io](https://scrap.io?utm_medium=ads&utm_source=github_gosom_gmap_scraper) - Extract ALL Google Maps listings at country-scale

[![Scrap.io - Extract ALL Google Maps Listings](./img/premium_scrap_io.png)](https://scrap.io?utm_medium=ads&utm_source=github_gosom_gmap_scraper)

No keywords needed. No limits. Export millions of businesses in 2 clicks. [**Try it free →**](https://scrap.io?utm_medium=ads&utm_source=github_gosom_gmap_scraper)

---

### [G Maps Extractor](https://gmapsextractor.com?utm_source=github&utm_medium=banner&utm_campaign=gosom) - No-code Google Maps scraper

[![G Maps Extractor](./img/gmaps-extractor-banner.png)](https://gmapsextractor.com?utm_source=github&utm_medium=banner&utm_campaign=gosom)

Chrome extension that extracts emails, social profiles, phone numbers, reviews & more. [**Get 1,000 free leads →**](https://gmapsextractor.com?utm_source=github&utm_medium=banner&utm_campaign=gosom)

---

### [SerpApi](https://serpapi.com/?utm_source=google-maps-scraper) - Google Maps API and 30+ search engine APIs

[![SerpApi](./img/SerpApi-banner.png)](https://serpapi.com/?utm_source=google-maps-scraper)

Fast, reliable, and scalable. Used by Fortune 500 companies. [**View all APIs →**](https://serpapi.com/search-api)

---

### [HasData](https://hasdata.com/scrapers/google-maps?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom) - No-code Google Maps Scraper & Email Extraction

[![HasData Google Maps Scraper](./img/hd-gm-banner.png)](https://hasdata.com/scrapers/google-maps?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom)

Extract business leads, emails, addresses, phones, reviews and more. [**Get 1,000 free credits →**](https://hasdata.com/scrapers/google-maps?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom)

---

<p align="center">
  <a href="#sponsors">View all sponsors</a> | <a href="https://github.com/sponsors/gosom">Become a sponsor</a>
</p>

---

## Why Use This Scraper?

| | |
|---|---|
| **Completely Free & Open Source** | MIT licensed, no hidden costs or usage limits |
| **Multiple Interfaces** | CLI, Web UI, REST API - use what fits your workflow |
| **High Performance** | ~120 places/minute with optimized concurrency |
| **33+ Data Points** | Business details, reviews, emails, coordinates, and more |
| **Production Ready** | Scale from a single machine to Kubernetes clusters |
| **Flexible Output** | CSV, JSON, PostgreSQL, S3, LeadsDB, or custom plugins |
| **Proxy Support** | Built-in SOCKS5/HTTP/HTTPS proxy rotation |

---

## What's Next After Scraping?

Once you've collected your data, you'll need to manage, deduplicate, and work with your leads. **[LeadsDB](https://getleadsdb.com/)** is a companion tool designed exactly for this:

- **Automatic Deduplication** - Import from multiple scrapes without worrying about duplicates
- **AI Agent Ready** - Query and manage leads with natural language via MCP
- **Advanced Filtering** - Combine filters with AND/OR logic on any field
- **Export Anywhere** - CSV, JSON, or use the REST API

The scraper has [built-in LeadsDB integration](#export-to-leadsdb) - just add your API key and leads flow directly into your database.

**[Start free with 500 leads](https://getleadsdb.com/)**

---

## Table of Contents

- [Quick Start](#quick-start)
  - [Web UI](#web-ui)
  - [Command Line](#command-line)
  - [REST API](#rest-api)
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
  - [Export to LeadsDB](#export-to-leadsdb)
- [Performance](#performance)
- [Support the Project](#support-the-project)
- [Sponsors](#sponsors)
- [Community](#community)
- [Contributing](#contributing)
- [License](#license)

---

## Quick Start

### Web UI

Start the web interface with a single command:

```bash
mkdir -p gmapsdata && docker run -v $PWD/gmapsdata:/gmapsdata -p 8080:8080 gosom/google-maps-scraper -data-folder /gmapsdata
```

Then open http://localhost:8080 in your browser.

Or download the [binary release](https://github.com/gosom/google-maps-scraper/releases) for your platform.

> **Note:** Results take at least 3 minutes to appear (minimum configured runtime).
> 
> **macOS Users:** Docker command may not work. See [MacOS Instructions](MacOS%20instructions.md).

### Command Line

```bash
touch results.csv && docker run \
  -v $PWD/example-queries.txt:/example-queries \
  -v $PWD/results.csv:/results.csv \
  gosom/google-maps-scraper \
  -depth 1 \
  -input /example-queries \
  -results /results.csv \
  -exit-on-inactivity 3m
```

> **Tip:** Use `gosom/google-maps-scraper:latest-rod` for the Rod version with faster container startup.

**Want emails?** Add the `-email` flag.

**Want all reviews (up to ~300)?** Add `--extra-reviews` and use `-json` output.

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

---

## Installation

### Using Docker (Recommended)

Two Docker image variants are available:

| Image | Tag | Browser Engine | Best For |
|-------|-----|----------------|----------|
| Playwright (default) | `latest`, `vX.X.X` | Playwright | Most users, better stability |
| Rod | `latest-rod`, `vX.X.X-rod` | Rod/Chromium | Lightweight, faster startup |

```bash
# Playwright version (default)
docker pull gosom/google-maps-scraper

# Rod version (alternative)
docker pull gosom/google-maps-scraper:latest-rod
```

### Build from Source

Requirements: Go 1.25.6+

```bash
git clone https://github.com/gosom/google-maps-scraper.git
cd google-maps-scraper
go mod download

# Playwright version (default)
go build
./google-maps-scraper -input example-queries.txt -results results.csv -exit-on-inactivity 3m

# Rod version (alternative)
go build -tags rod
./google-maps-scraper -input example-queries.txt -results results.csv -exit-on-inactivity 3m
```

> First run downloads required browser libraries (Playwright or Chromium depending on version).

---

## Features

| Feature | Description |
|---------|-------------|
| **33+ Data Points** | Business name, address, phone, website, reviews, coordinates, and more |
| **Email Extraction** | Optional crawling of business websites for email addresses |
| **Multiple Output Formats** | CSV, JSON, PostgreSQL, S3, LeadsDB, or custom plugins |
| **Proxy Support** | SOCKS5, HTTP, HTTPS with authentication |
| **Scalable Architecture** | Single machine to Kubernetes cluster |
| **REST API** | Programmatic control for automation |
| **Web UI** | User-friendly browser interface |
| **Fast Mode (Beta)** | Quick extraction of up to 21 results per query |
| **AWS Lambda** | Serverless execution support (experimental) |

---

## Extracted Data Points

<details>
<summary><strong>Click to expand all 33 data points</strong></summary>

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
| 14 | `latitude` | GPS latitude |
| 15 | `longitude` | GPS longitude |
| 16 | `cid` | Google's unique Customer ID |
| 17 | `status` | Business status (open/closed/temporary) |
| 18 | `descriptions` | Business description |
| 19 | `reviews_link` | Direct link to reviews |
| 20 | `thumbnail` | Thumbnail image URL |
| 21 | `timezone` | Business timezone |
| 22 | `price_range` | Price level ($, $$, $$$) |
| 23 | `data_id` | Internal Google Maps identifier |
| 24 | `images` | Associated image URLs |
| 25 | `reservations` | Reservation booking link |
| 26 | `order_online` | Online ordering link |
| 27 | `menu` | Menu link |
| 28 | `owner` | Owner-claimed status |
| 29 | `complete_address` | Full formatted address |
| 30 | `about` | Additional business info |
| 31 | `user_reviews` | Customer reviews (text, rating, timestamp) |
| 32 | `emails` | Extracted email addresses (requires `-email` flag) |
| 33 | `user_reviews_extended` | Extended reviews up to ~300 (requires `-extra-reviews`) |
| 34 | `place_id` | Google's unique place id |

</details>

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

Export:
  -leadsdb-api-key   Export directly to LeadsDB (get key at getleadsdb.com)

Advanced:
  -exit-on-inactivity duration    Exit after inactivity (e.g., '5m')
  -fast-mode                      Quick mode with reduced data
  -debug                          Show browser window
  -writer string                  Custom writer plugin (format: 'dir:pluginName')
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

#### Proxy Providers

If you need reliable proxies, these providers support this project as sponsors:

| Provider | Highlight | Offer |
|----------|-----------|-------|
| [Decodo](https://visit.decodo.com/APVbbx) | #1 response time, 125M+ IPs | [3-day free trial](https://visit.decodo.com/APVbbx) |
| [Evomi](https://evomi.com?utm_source=github&utm_medium=banner&utm_campaign=gosom-maps) | Swiss quality, 150+ countries | From $0.49/GB |

Using their services helps fund continued development of this scraper.

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

### Export to LeadsDB

Skip the CSV files and send leads directly to a managed database. [LeadsDB](https://getleadsdb.com/) handles deduplication, filtering, and provides an API for your applications.

```bash
./google-maps-scraper \
  -input queries.txt \
  -leadsdb-api-key "your-api-key" \
  -exit-on-inactivity 3m
```

Or via environment variable:
```bash
export LEADSDB_API_KEY="your-api-key"
./google-maps-scraper -input queries.txt -exit-on-inactivity 3m
```

<details>
<summary><strong>Field Mapping</strong></summary>

| Google Maps | LeadsDB |
|-------------|---------|
| Title | Name |
| Category | Category |
| Categories | Tags |
| Phone | Phone |
| Website | Website |
| Address | Address, City, State, Country, PostalCode |
| Latitude/Longitude | Coordinates |
| Review Rating | Rating |
| Review Count | ReviewCount |
| Emails | Email |
| Thumbnail | LogoURL |
| CID | SourceID |

Additional fields (Google Maps link, plus code, price range, etc.) are stored as custom attributes.

</details>

Get your API key at [getleadsdb.com/settings](https://getleadsdb.com/settings) after signing up.

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

---

## Support the Project

This project is **free and open source**, maintained in my spare time. If it's useful to you, here's how you can help it grow:

### Quick Ways to Help

| Action | Impact |
|--------|--------|
| **[Star this repo](https://github.com/gosom/google-maps-scraper)** | Helps others discover the project |
| **[Sponsor on GitHub](https://github.com/sponsors/gosom)** | Directly funds development time |
| **Share your success** | Tweet or blog about how you use it |
| **Report bugs & contribute** | Help improve the codebase |

### Use Sponsor Services

When you need proxies, APIs, or cloud services, consider using our sponsors. You get quality tools, and the referral helps fund this project:

- **Need proxies?** [Decodo](https://visit.decodo.com/APVbbx) or [Evomi](https://evomi.com?utm_source=github&utm_medium=banner&utm_campaign=gosom-maps)
- **Prefer an API?** [SerpApi](https://serpapi.com/?utm_source=google-maps-scraper) or [SearchAPI](https://www.searchapi.io/google-maps?via=gosom)
- **No-code solution?** [Scrap.io](https://scrap.io?utm_medium=ads&utm_source=github_gosom_gmap_scraper) or [G Maps Extractor](https://gmapsextractor.com?utm_source=github&utm_medium=banner&utm_campaign=gosom)
- **Cloud hosting?** [DigitalOcean](https://www.digitalocean.com/?refcode=c11136c4693c&utm_campaign=Referral_Invite&utm_medium=Referral_Program&utm_source=badge) ($200 credit) or [Hetzner](https://hetzner.cloud/?ref=ihtQPa0cT18n) (EUR 20 credit)

---

## Sponsors

A huge thank you to the sponsors who make this project possible!

---

### Premium Sponsors

---

#### [Scrap.io](https://scrap.io?utm_medium=ads&utm_source=github_gosom_gmap_scraper)

[![Scrap.io - Extract ALL Google Maps Listings](./img/premium_scrap_io.png)](https://scrap.io?utm_medium=ads&utm_source=github_gosom_gmap_scraper)

**Extract ALL Google Maps listings at country-scale in 2 clicks.** No keywords needed. No limits. Export millions of businesses instantly.

[**Try it free →**](https://scrap.io?utm_medium=ads&utm_source=github_gosom_gmap_scraper) · [See demo](scrap_io.md)

---

#### [G Maps Extractor](https://gmapsextractor.com?utm_source=github&utm_medium=banner&utm_campaign=gosom)

[![G Maps Extractor](./img/gmaps-extractor-banner.png)](https://gmapsextractor.com?utm_source=github&utm_medium=banner&utm_campaign=gosom)

**No-code Google Maps scraper Chrome extension.** Extract emails, social profiles, phone numbers, reviews, images & more with one click.

[**Get 1,000 free leads →**](https://gmapsextractor.com?utm_source=github&utm_medium=banner&utm_campaign=gosom) · [API docs](https://gmapsextractor.com/google-maps-api?utm_source=github&utm_medium=banner&utm_campaign=gosom)

---

#### [SerpApi](https://serpapi.com/?utm_source=google-maps-scraper)

[![SerpApi](./img/SerpApi-banner.png)](https://serpapi.com/?utm_source=google-maps-scraper)

**Google Maps API and 30+ search engine APIs.** Fast, reliable, and scalable. Used by Fortune 500 companies.

[**View all APIs →**](https://serpapi.com/search-api) · [Learn more](serpapi.md)

---

#### [HasData](https://hasdata.com/scrapers/google-maps?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom)

[![HasData Google Maps Scraper](./img/hd-gm-banner.png)](https://hasdata.com/scrapers/google-maps?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom)

**No-code Google Maps Scraper & Email Extraction.** Extract business leads, emails, addresses, phones, reviews and more.

[**Get 1,000 free credits →**](https://hasdata.com/scrapers/google-maps?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom) · [Learn more](sponsors/hasdata.md)

---

### More Sponsors

<table>
<tr>
<td align="center" width="50%">

[![Evomi](https://my.evomi.com/images/brand/cta.png)](https://evomi.com?utm_source=github&utm_medium=banner&utm_campaign=gosom-maps)

**[Evomi](https://evomi.com?utm_source=github&utm_medium=banner&utm_campaign=gosom-maps)**

Swiss quality proxies from $0.49/GB<br>150+ countries · 24/7 support · 99.9% uptime

</td>
<td align="center" width="50%">

[![SearchAPI](https://www.searchapi.io/press/v1/svg/searchapi_logo_black_h.svg)](https://www.searchapi.io/google-maps?via=gosom)

**[SearchAPI](https://www.searchapi.io/google-maps?via=gosom)**

Google Maps API for easy SERP scraping<br>Real-time data · Simple integration

</td>
</tr>
</table>

---

<p align="center">
  <a href="https://github.com/sponsors/gosom"><strong>Become a sponsor</strong></a> to get your logo here and support open source development.
</p>

---

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
- [scrapemate](https://github.com/gosom/scrapemate) - The underlying web crawling framework
- [omkarcloud/google-maps-scraper](https://github.com/omkarcloud/google-maps-scraper) - Inspiration for JS data extraction

---

## License

This project is licensed under the [MIT License](LICENSE).

---

## Star History

<a href="https://star-history.com/#gosom/google-maps-scraper&Date">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=gosom/google-maps-scraper&type=Date&theme=dark" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=gosom/google-maps-scraper&type=Date" />
   <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=gosom/google-maps-scraper&type=Date" />
 </picture>
</a>

---

## Legal Notice

Please use this scraper responsibly and in accordance with applicable laws and regulations. Unauthorized scraping may violate terms of service.

---

<p align="center">
  <sub>Banner generated using OpenAI's DALL-E</sub>
</p>

<p align="center">
  <a href="https://github.com/gosom/google-maps-scraper/stargazers">
    <img src="https://img.shields.io/github/stars/gosom/google-maps-scraper?style=social" alt="Star on GitHub">
  </a>
</p>

<p align="center">
  <b>If this project saved you time, consider <a href="https://github.com/gosom/google-maps-scraper">starring it</a> or <a href="https://github.com/sponsors/gosom">sponsoring</a> its development!</b>
</p>
