For README.md

### [HasData](https://hasdata.com/scrapers/google-maps?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom) - No-code Google Maps Scraper & Email Extraction

[![HasData Google Maps Scraper](./img/hd-gm-banner.png)](https://hasdata.com/scrapers/google-maps?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom)

Extract business leads, emails, addresses, phones, reviews and more. [**Get 1,000 free credits →**](https://hasdata.com/scrapers/google-maps?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom)

---
================================================================

For hasdata.md


# HasData: Premium Google Maps & SERP API

![HasData](img/hd-gm-banner.png) 

**[HasData](https://hasdata.com/?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom)** is a professional web scraping infrastructure that handles browsers, rotating proxies, and anti-bot systems for you.

We provide a suite of APIs specifically designed for **Google Maps** data extraction at scale. Whether you need to scrape 10 local businesses or 10 million global listings, our infrastructure scales instantly.

### Why Developers Choose HasData?
* **99.9% Success Rate:** We handle CAPTCHAs and IP bans automatically.
* **High Concurrency:** Run thousands of requests per second.
* **Structured Data:** Get clean JSON output directly to your app.
* **Playground:** Test our APIs interactively before writing code.

---

## 🗺️ Google Maps API Suite

We offer a complete toolkit for extracting every data point available on Google Maps.

### 1. Google Maps Search API
Search for businesses by keywords (e.g., "Pizza in New York") or coordinates. Returns essential data like names, phones, addresses, and ratings.

[📄 Documentation](https://docs.hasdata.com/apis/google-maps/search) | [⚡ Live Demo](https://hasdata.com/apis/google-maps-search-api?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom)

```bash
curl --request GET \
  --url 'https://api.hasdata.com/scrape/google-maps/search?q=Pizza+in+New+York' \
  --header 'Content-Type: application/json' \
  --header 'x-api-key: <HASDATA_API_KEY>'
```

### 2. Google Maps Reviews API
Extract user reviews to analyze sentiment or gather feedback. Supports sorting and pagination.

[📄 Documentation](https://docs.hasdata.com/apis/google-maps/reviews) | [⚡ Live Demo](https://hasdata.com/apis/google-maps-reviews-api?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom)

```bash
curl --request GET \
  --url 'https://api.hasdata.com/scrape/google-maps/reviews?dataId=0x873312ae759b4d15:0x1f38a9bec9912029' \
  --header 'Content-Type: application/json' \
  --header 'x-api-key: <HASDATA_API_KEY>'
```

### 3. Place Details API
Get full details about a specific location using its placeId, including operating hours, amenities, and detailed descriptions.

[📄 Documentation](https://docs.hasdata.com/apis/google-maps/search) | [⚡ Live Demo](https://hasdata.com/apis/google-maps-search-api#google-maps-place-api?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom)

```bash
curl --request GET \
  --url 'https://api.hasdata.com/scrape/google-maps/place?placeId=ChIJFU2bda4SM4cRKSCRyb6pOB8' \
  --header 'Content-Type: application/json' \
  --header 'x-api-key: <HASDATA_API_KEY>'
```
 
### 4. Specialized Maps APIs
Dig deeper into specific entities:

[Contributor Reviews API](https://hasdata.com/apis/google-maps-reviews-api#google-maps-contributor-reviews-api?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom): Scrape all reviews left by a specific Google Maps user.

```bash
curl --request GET \
  --url 'https://api.hasdata.com/scrape/google-maps/contributor-reviews?contributorId=117472887966458832611' \
  --header 'x-api-key: <HASDATA_API_KEY>'
```

[Photos API](https://hasdata.com/apis/google-maps-photos-api?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom): Extract image URLs and metadata for any location.

```bash
curl --request GET \
  --url 'https://api.hasdata.com/scrape/google-maps/photos?dataId=0x80cc0654bd27e08d:0xb1c2554442d42e8d' \
  --header 'x-api-key: <HASDATA_API_KEY>'
```

## 📧 Bonus: Lead Generation & Email Finding
Google Maps often lacks email addresses. You can use our [Google SERP API](https://hasdata.com/apis/google-serp-api?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom) to find emails for businesses by leveraging advanced search operators (Dorks).

Strategy: Combine the business name or website + location with query operators.

```bash
curl --request GET \
  --url 'https://api.hasdata.com/scrape/google/serp?q=site%3Asitemane.com+email' \
  --header 'Content-Type: application/json' \
  --header 'x-api-key: <HASDATA_API_KEY>'
```
  
## 🛠️ Resources & Support
* **[API Documentation](https://docs.hasdata.com/introduction):** Full reference for all parameters.
* **[No-Code Dashboard](https://app.hasdata.com/dashboard):** Prefer a visual interface? Export data to CSV/Excel without coding.

### Ready to build?
Stop worrying about proxies and start scraping in minutes.
**[Get your Free API Key (1,000 Credits) →](https://hasdata.com/?utm_source=github&utm_medium=sponsorship&utm_campaign=gosom)**