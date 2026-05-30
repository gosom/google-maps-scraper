# NetNut: Google Maps Places API & SERP Scraper API

[![NetNut](https://github.com/gosom/google-maps-scraper/raw/main/img/netnut-banner.png)](https://netnut.io/?ref=y2fmmzz)

If your product depends on public web data collection, you know the challenge - getting reliable, structured data at scale isn't simple when anti-bot systems keep evolving with CAPTCHAs and IP bans.

**[NetNut](https://netnut.io/?ref=y2fmmzz)** is built to make web data collection limitless. We provide the infrastructure behind large-scale data pipelines - powering SaaS platforms, AI models, and data-driven products - as part of Alarum Technologies (NASDAQ: ALAR).

At the core is a network of **85M+ residential IPs across 195+ countries**, fully owned and operated by NetNut. It's designed to behave like real users, and it powers everything we ship - from Website Unblocker and scraping APIs to ready-to-use datasets. What really sets us apart is **24/7 integration support**: dedicated account managers and anti-blocking experts working with you from setup to scale. Trusted by enterprise teams and AI companies, including Lenovo, Apify, Sterling Check, and Import.io.

For Google Maps data specifically, our **SERP Scraper API** has a dedicated **Google Maps Places** endpoint that returns clean, structured JSON - no browser, no proxy setup, no CAPTCHA handling on your end. Just send a request and get results.

### Why Developers Choose NetNut?

- **We Own the Network - Not a Reseller:** Direct accountability, consistent quality, no middlemen.
- **Direct ISP Connectivity:** One-hop architecture through ISP partnerships, not peer-to-peer.
- **85M+ Residential IPs** across 195+ countries.
- **Structured JSON Output** from the SERP API - HTML available when you need it.
- **No Proxy or CAPTCHA Setup:** The SERP API handles rotation, retries, and anti-bot bypass for you.
- **24/7 Integration Support** with dedicated account managers and anti-blocking experts.

---

## 🗺️ Google Maps Places API

The NetNut **Google Maps Places API** retrieves Google Maps Places search results and returns structured JSON. Search for businesses, points of interest, and venues by keyword, with filters for language, country, and precise geographic targeting.

[📄 Full Documentation](https://help.netnut.io/netnut-documentation/netnut-scraper-apis/serp-api/google-scraper/google-places) | [⚡ Start Free Trial](https://netnut.io/?ref=y2fmmzz)

### Endpoint

```
https://serp-api.netnut.io/search
```

### Authentication

HTTP Basic Auth with your NetNut credentials:

```
Authorization: Basic <base64(username:password)>
```

### Quick Example - Search Coffee Shops in the US

```bash
curl -X GET "https://serp-api.netnut.io/search?engine=google_places&q=coffee&hl=en&gl=us" \
  -u "username:password"
```

### Python Example

```python
import requests
from base64 import b64encode

headers = {
    "Authorization": "Basic " + b64encode(b"username:password").decode()
}

params = {
    "engine": "google_places",
    "q": "coffee",
    "gl": "us",
    "hl": "en"
}

response = requests.get("https://serp-api.netnut.io/search", headers=headers, params=params)
print(response.json())
```

### JavaScript Example

```javascript
const params = new URLSearchParams({
  engine: "google_places",
  q: "coffee",
  gl: "us",
  hl: "en"
});

const response = await fetch(`https://serp-api.netnut.io/search?${params}`, {
  method: "GET",
  headers: {
    "Authorization": "Basic " + Buffer.from("username:password").toString("base64")
  }
});

const data = await response.json();
console.log(data);
```

### Request Parameters

| Parameter | Type | Description |
| --- | --- | --- |
| `engine` | string | Required. Use `google_places`. |
| `q` | string | Required. Search query or place type (e.g. `coffee`, `restaurants`, `pharmacies`). |
| `hl` | string | Language of the Google Places results (e.g. `en`). |
| `gl` | string | Geographic location / country for the search (e.g. `us`). |
| `uule` | string | Encoded location parameter used to set a precise geographic context. Overrides general country-level targeting set by `gl`. |
| `location` | string | Plain-text location string used to target results to a specific city or region (e.g. `New York, NY`). |
| `udm` | integer | Google search mode parameter. Set to `1` to enable the Places results mode. |
| `rawHtml` | integer | Controls whether raw HTML is returned. `1` = return parsed JSON + HTML, `2` = return HTML only. |

[See full parameter reference and use cases →](https://help.netnut.io/netnut-documentation/netnut-scraper-apis/serp-api/google-scraper/google-places/features)

### Combined Example

You can combine multiple parameters to create a more precise Google Places search:

```
https://serp-api.netnut.io/search?engine=google_places&q=coffee&hl=en&gl=us&uule=w+CAIQICINVW5pdGVkIFN0YXRlcw&udm=1
```

This request searches Google Places for `coffee`, returns English results for the US market, applies precise location targeting via `uule`, and activates Places results mode.

### Response Fields

The API returns structured JSON with these top-level fields:

| Field | Type | Description |
| --- | --- | --- |
| `url` | string | Final Google Places search URL used to retrieve the results. |
| `general` | object | Metadata about the search environment and request configuration. |
| `input` | object | Information about the original request processed by the API. |
| `localResults` | array | List of local place result objects extracted from Google Places. |
| `pagination` | object | Pagination metadata for the response. |
| `html` | string | Raw HTML of the Google Search results page. Only present when `rawHtml=1` or `rawHtml=2`. |

Each object inside `localResults` contains fields such as `place_id`, `cid`, `name`, `sponsored`, `image`, `rating`, `reviews_cnt`, `reviews_link`, `price`, `type`, `open_state`, `latitude`, `longitude`, `top_review`, `address`, `phone`, `tags`, `rank`, and `global_rank`.

[See full response schema with examples →](https://help.netnut.io/netnut-documentation/netnut-scraper-apis/serp-api/google-scraper/google-places/response-fields)

[See more example requests →](https://help.netnut.io/netnut-documentation/netnut-scraper-apis/serp-api/google-scraper/google-places/example-requests)

---

## 🛠️ Resources & Support

- **[Google Maps Places API Docs](https://help.netnut.io/netnut-documentation/netnut-scraper-apis/serp-api/google-scraper/google-places)** - Endpoint reference, features, response fields, and examples
- **[Full Documentation](https://help.netnut.io/netnut-documentation/)** - All NetNut products
- **[Dashboard](https://dashboard.netnut.io/auth/login/)** - Manage credentials, monitor usage, view logs
- **Dedicated Account Manager** - Included on all SERP Scraper API plans

---

### Ready to start scraping?

Sign up today to speak with one of our integration experts and get a free trial, or use our self-service checkout to get started instantly.

**[Start your free trial →](https://netnut.io/?ref=y2fmmzz)**
