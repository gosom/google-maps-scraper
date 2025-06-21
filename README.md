# Google maps scraper
![build](https://github.com/gosom/google-maps-scraper/actions/workflows/build.yml/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/gosom/google-maps-scraper)](https://goreportcard.com/report/github.com/gosom/google-maps-scraper)
[![Discord](https://img.shields.io/badge/Discord-Join%20Chat-7289DA?logo=discord&logoColor=white)](https://discord.gg/fpaAVhNCCu)

> A free and open-source Google Maps scraper with both command line and web UI options. This tool is easy to use and allows you to extract data from Google Maps efficiently.

## Join Our Community

[![Discord](https://img.shields.io/badge/Discord-Join%20Chat-7289DA?logo=discord&logoColor=white)](https://discord.gg/fpaAVhNCCu)

Join our Discord server to get help, share ideas, and connect with other users of the Google Maps Scraper!

## 🚀 Take Your Lead Generation to the Next Level

**Tired of manual data collection? Meet LeadsDB - an API service that integrates with this scraper.**

[LeadsDB](https://gm.thexos.dev/) enhances your lead generation with:
- ✅ Cloud storage for all your search results
- ✅ Visualization dashboard
- ✅ Advanced filtering & export options
- ✅ Simple API integration with this scraper
- ✅ Expose your data to other services via a REST API

Plans start at just €4.99/month

**[Join the waitlist →](https://gm.thexos.dev/)**

_Continue using this free scraper and supercharge your workflow with LeadsDB._

## Sponsors


### Supported by the Community

This project relies on the support of its users and sponsors to stay alive and improve. If you find it useful, here’s how you can help:

- ⭐ **Star the repository** to show your support and help others discover it.
- ❤️ **Sponsor the project** to contribute directly to its development. [Become a sponsor →](https://github.com/sponsors/gosom)
- 🤝 **Use the services of our sponsors** to support the project while benefiting from their offerings.

Your support ensures the project remains maintained and continues to grow. Thank you!

### Premium Sponsors

**No time for code? Extract ALL Google Maps listings at country-scale in 2 clicks, without keywords or limits** 👉 [Try it now for free](https://scrap.io?utm_medium=ads&utm_source=github_gosom_gmap_scraper)

[![Extract ALL Google Maps Listings](./img/premium_scrap_io.png)](https://scrap.io?utm_medium=ads&utm_source=github_gosom_gmap_scraper)

<hr>

<table>
<tr>
<td><img src="./img/SerpApi-logo-w.png" alt="SerpApi Logo" width="100"></td>
<td>
<b>At SerpApi, we scrape public data from Google Maps and other top search engines.</b>

You can find the full list of our APIs here: [https://serpapi.com/search-api](https://serpapi.com/search-api)
</td>
</tr>
</table>

[![SerpApi Banner](./img/SerpApi-banner.png)](https://serpapi.com/?utm_source=google-maps-scraper)

<hr>

**G Maps Extractor**  
A no-code Google Maps scraper that pulls business leads from Google Maps in one click.

- 📇 **Includes** emails, social profiles, phone numbers, addresses, reviews, images and more.
- 📥 **Export** to CSV · Excel · JSON  
- 🎁 **Free**: Get your first **1,000 leads** today  
[Get Started for Free](https://gmapsextractor.com?utm_source=github&utm_medium=banner&utm_campaign=gosom)

[![Gmaps Extractor](./img/gmaps-extractor-banner.png)](https://gmapsextractor.com?utm_source=github&utm_medium=banner&utm_campaign=gosom)

</hr>

### Special Thanks to:

[![Google Maps API for easy SERP scraping](https://www.searchapi.io/press/v1/svg/searchapi_logo_black_h.svg)](https://www.searchapi.io/google-maps?via=gosom)
**Google Maps API for easy SERP scraping**

<hr>

[Evomi](https://evomi.com?utm_source=github&utm_medium=banner&utm_campaign=gosom-maps) is your Swiss Quality Proxy Provider, starting at **$0.49/GB**

[![Evomi Banner](https://my.evomi.com/images/brand/cta.png)](https://evomi.com?utm_source=github&utm_medium=banner&utm_campaign=gosom-maps)

<hr>

[Scrapeless](https://www.scrapeless.com/): One-click to scrape Google search results, supporting 15+ SERP scenarios such as Google Maps/Scholars/Jobs, $0.1/thousand queries, 0.2s response.

**[👉 Free Trial](https://app.scrapeless.com/passport/login?utm_source=gosom&utm_campaign=google-maps)**

![Scrapeless](./img/scrapeless_dark.png#gh-dark-mode-only)

![Scrapeless](./img/scrapeless_light.png#gh-light-mode-only)



<hr>


## What Google maps scraper does

A command line and web based google maps scraper build using 

[scrapemate](https://github.com/gosom/scrapemate) web crawling framework.

You can use this repository either as is, or you can use its code as a base and
customize it to your needs

![Example GIF](img/example.gif)

### Web UI:

```
mkdir -p gmapsdata && docker run -v $PWD/gmapsdata:/gmapsdata -p 8080:8080 gosom/google-maps-scraper -data-folder /gmapsdata
```

Or dowload the [binary](https://github.com/gosom/google-maps-scraper/releases) for your platform and run it.

Note: The results will take at least 3 minutes to appear, even if you add only one keyword. This is the minimum configured runtime.

Note: for MacOS the docker command should not work. **HELP REQUIRED**


### Command line:

```
touch results.csv && docker run -v $PWD/example-queries.txt:/example-queries -v $PWD/results.csv:/results.csv gosom/google-maps-scraper -depth 1 -input /example-queries -results /results.csv -exit-on-inactivity 3m
```

file `results.csv` will contain the parsed results.

**If you want emails use additionally the `-email` parameter*

### REST API
The Google Maps Scraper provides a RESTful API for programmatic management of scraping tasks.

### Key Endpoints

- POST /api/v1/jobs: Create a new scraping job
- GET /api/v1/jobs: List all jobs
- GET /api/v1/jobs/{id}: Get details of a specific job
- DELETE /api/v1/jobs/{id}: Delete a job
- GET /api/v1/jobs/{id}/download: Download job results as CSV

For detailed API documentation, refer to the OpenAPI 3.0.3 specification available through Swagger UI or Redoc when running the app https://localhost:8080/api/docs


## 🌟 Support the Project!

If you find this tool useful, consider giving it a **star** on GitHub. 
Feel free to check out the **Sponsor** button on this repository to see how you can further support the development of this project. 
Your support helps ensure continued improvement and maintenance.


## Features

- Extracts many data points from google maps
- Exports the data to CSV, JSON or PostgreSQL 
- Performance about 120 urls per minute (-depth 1 -c 8)
- Extendable to write your own exporter
- Dockerized for easy run in multiple platforms
- Scalable in multiple machines
- Optionally extracts emails from the website of the business
- SOCKS5/HTTP/HTTPS proxy support
- Serverless execution via AWS Lambda functions (experimental & no documentation yet)
- Fast Mode (BETA)

## Notes on email extraction

By default email extraction is disabled. 

If you enable email extraction (see quickstart) then the scraper will visit the 
website of the business (if exists) and it will try to extract the emails from the
page.

For the moment it only checks only one page of the website (the one that is registered in Gmaps). At some point, it will be added support to try to extract from other pages like about, contact, impressum etc. 


Keep in mind that enabling email extraction results to larger processing time, since more
pages are scraped. 

## Fast Mode

Fast mode returns you at most 21 search results per query ordered by distance from the **latitude** and **longitude** provided.
All the results are within the specified **radius**

It does not contain all the data points but basic ones. 
However it provides the ability to extract data really fast. 

When you use the fast mode ensure that you have provided:
- zoom
- radius (in meters)
- latitude
- longitude


**Fast mode is Beta, you may experience blocking**

## Extracted Data Points

#### 1. `input_id`
- Internal identifier for the input query.

#### 2. `link`
- Direct URL to the business listing on Google Maps.

#### 3. `title`
- Name of the business.

#### 4. `category`
- Business type or category (e.g., Restaurant, Hotel).

#### 5. `address`
- Street address of the business.

#### 6. `open_hours`
- Business operating hours.

#### 7. `popular_times`
- Estimated visitor traffic at different times of the day.

#### 8. `website`
- Official business website.

#### 9. `phone`
- Business contact phone number.

#### 10. `plus_code`
- Shortcode representing the precise location of the business.

#### 11. `review_count`
- Total number of customer reviews.

#### 12. `review_rating`
- Average star rating based on reviews.

#### 13. `reviews_per_rating`
- Breakdown of reviews by each star rating (e.g., number of 5-star, 4-star reviews).

#### 14. `latitude`
- Latitude coordinate of the business location.

#### 15. `longitude`
- Longitude coordinate of the business location.

#### 16. `cid`
- **Customer ID** (CID) used by Google Maps to uniquely identify a business listing. This ID remains stable across updates and can be used in URLs.
- **Example:** `3D3174616216150310598`

#### 17. `status`
- Business status (e.g., open, closed, temporarily closed).

#### 18. `descriptions`
- Brief description of the business.

#### 19. `reviews_link`
- Direct link to the reviews section of the business listing.

#### 20. `thumbnail`
- URL to a thumbnail image of the business.

#### 21. `timezone`
- Time zone of the business location.

#### 22. `price_range`
- Price range of the business (`$`, `$$`, `$$$`).

#### 23. `data_id`
- An internal Google Maps identifier composed of two hexadecimal values separated by a colon.
- **Structure:** `<spatial_hex>:<listing_hex>`
- **Example:** `0x3eb33fecd7dfa167:0x2c0e80a0f5d57ec6`
- **Note:** This value may change if the listing is updated and should not be used for permanent identification.

#### 24. `images`
- Links to images associated with the business.

#### 25. `reservations`
- Link to book reservations (if available).

#### 26. `order_online`
- Link to place online orders.

#### 27. `menu`
- Link to the menu (for applicable businesses).

#### 28. `owner`
- Indicates whether the business listing is claimed by the owner.

#### 29. `complete_address`
- Fully formatted address of the business.

#### 30. `about`
- Additional information about the business.

#### 31. `user_reviews`
- Collection of customer reviews, including text, rating, and timestamp.

#### 32. `emails`
- Email addresses associated with the business, if available.

#### 33. `user_reviews_extended`
- Collection of customer reviews, including text, rating, and timestamp. This includes all the
  reviews that can be extracted (up to around 300)

**Note**: email is empty by default (see Usage)

**Note**: Input id is an ID that you can define per query. By default it's a UUID
In order to define it you can have an input file like:

**Note**: user_reviews_extended is empty by default. You need to start the program with the
`-extra-reviews` command line flag to enabled this (see Usage)

```
Matsuhisa Athens #!#MyIDentifier
```

## Quickstart

### Using docker:

```
touch results.csv && docker run -v $PWD/example-queries.txt:/example-queries -v $PWD/results.csv:/results.csv gosom/google-maps-scraper -depth 1 -input /example-queries -results /results.csv -exit-on-inactivity 3m
```

file `results.csv` will contain the parsed results.

**If you want emails use additionally the `-email` parameter**

**All Reviews**
You can fetch up to around 300 reviews instead of the first 8 by using the 
command line parameter `--extra-reviews`. If you do that I recommend you use JSON
output instead of CSV.


### On your host

(tested only on Ubuntu 22.04)

**make sure you use go version 1.24.3**


```
git clone https://github.com/gosom/google-maps-scraper.git
cd google-maps-scraper
go mod download
go build
./google-maps-scraper -input example-queries.txt -results restaurants-in-cyprus.csv -exit-on-inactivity 3m
```

Be a little bit patient. In the first run it downloads required libraries.

The results are written when they arrive in the `results` file you specified

**If you want emails use additionally the `-email` parameter**

### Command line options

try `./google-maps-scraper -h` to see the command line options available:
```
  -addr string
        address to listen on for web server (default ":8080")
  -aws-access-key string
        AWS access key
  -aws-lambda
        run as AWS Lambda function
  -aws-lambda-chunk-size int
        AWS Lambda chunk size (default 100)
  -aws-lambda-invoker
        run as AWS Lambda invoker
  -aws-region string
        AWS region
  -aws-secret-key string
        AWS secret key
  -c int
        sets the concurrency [default: half of CPU cores] (default 1)
  -cache string
        sets the cache directory [no effect at the moment] (default "cache")
  -data-folder string
        data folder for web runner (default "webdata")
  -debug
        enable headful crawl (opens browser window) [default: false]
  -depth int
        maximum scroll depth in search results [default: 10] (default 10)
  -disable-page-reuse
        disable page reuse in playwright
  -dsn string
        database connection string [only valid with database provider]
  -email
        extract emails from websites
  -exit-on-inactivity duration
        exit after inactivity duration (e.g., '5m')
  -extra-reviews
        enable extra reviews collection
  -fast-mode
        fast mode (reduced data collection)
  -function-name string
        AWS Lambda function name
  -geo string
        set geo coordinates for search (e.g., '37.7749,-122.4194')
  -input string
        path to the input file with queries (one per line) [default: empty]
  -json
        produce JSON output instead of CSV
  -lang string
        language code for Google (e.g., 'de' for German) [default: en] (default "en")
  -produce
        produce seed jobs only (requires dsn)
  -proxies string
        comma separated list of proxies to use in the format protocol://user:pass@host:port example: socks5://localhost:9050 or http://user:pass@localhost:9050
  -radius float
        search radius in meters. Default is 10000 meters (default 10000)
  -results string
        path to the results file [default: stdout] (default "stdout")
  -s3-bucket string
        S3 bucket name
  -web
        run web server instead of crawling
  -writer string
        use custom writer plugin (format: 'dir:pluginName')
  -zoom int
        set zoom level (0-21) for search (default 15)
```

## Using a custom writer

In cases the results need to be written in a custom format or in another system like a db a message queue or basically anything the Go plugin system can be utilized.

Write a Go plugin (see an example in examples/plugins/example_writeR.go) 

Compile it using (for Linux):

```
go build -buildmode=plugin -tags=plugin -o ~/mytest/plugins/example_writer.so examples/plugins/example_writer.go
```

and then run the program using the `-writer` argument. 

See an example:

1. Write your plugin (use the examples/plugins/example_writer.go as a reference)
2. Build your plugin `go build -buildmode=plugin -tags=plugin -o ~/myplugins/example_writer.so plugins/example_writer.go`
3. Download the lastes [release](https://github.com/gosom/google-maps-scraper/releases/) or build the program
4. Run the program like `./google-maps-scraper -writer ~/myplugins:DummyPrinter -input example-queries.txt`


### Plugins and Docker

It is possible to use the docker image and use tha plugins.
In such case make sure that the shared library is build using a compatible GLIB version with the docker image.
otherwise you will encounter an error like:

```
/lib/x86_64-linux-gnu/libc.so.6: version `GLIBC_2.32' not found (required by /plugins/example_writer.so)
```


## Using Database Provider (postgreSQL)

For running in your local machine:

```
docker-compose -f docker-compose.dev.yaml up -d
```

The above starts a PostgreSQL container and creates the required tables

to access db:

```
psql -h localhost -U postgres -d postgres
```

Password is `postgres`

Then from your host run:

```
go run main.go -dsn "postgres://postgres:postgres@localhost:5432/postgres" -produce -input example-queries.txt --lang el
```

(configure your queries and the desired language)

This will populate the table `gmaps_jobs` . 

you may run the scraper using:

```
go run main.go -c 2 -depth 1 -dsn "postgres://postgres:postgres@localhost:5432/postgres"
```

If you have a database server and several machines you can start multiple instances of the scraper as above.

### Kubernetes

You may run the scraper in a kubernetes cluster. This helps to scale it easier.

Assuming you have a kubernetes cluster and a database that is accessible from the cluster:

1. First populate the database as shown above
2. Create a deployment file `scraper.deployment`

```
apiVersion: apps/v1
kind: Deployment
metadata:
  name: google-maps-scraper
spec:
  selector:
    matchLabels:
      app: goohttps://www.scrapeless.com/gle-maps-scraper
  replicas: {NUM_OF_REPLICAS}
  template:
    metadata:
      labels:
        app: google-maps-scraper
    spec:
      containers:
      - name: google-maps-scraper
        image: gosom/google-maps-scraper:v0.9.3
        imagePullPolicy: IfNotPresent
        args: ["-c", "1", "-depth", "10", "-dsn", "postgres://{DBUSER}:{DBPASSWD@DBHOST}:{DBPORT}/{DBNAME}", "-lang", "{LANGUAGE_CODE}"]
```

Please replace the values or the command args accordingly 

Note: Keep in mind that because the application starts a headless browser it requires CPU and memory. 
Use an appropriate kubernetes cluster

## Telemetry

Anonymous usage statistics are collected for debug and improvement reasons. 
You can opt out by setting the env variable `DISABLE_TELEMETRY=1`

## Performance

Expected speed with concurrency of 8 and depth 1 is 120 jobs/per minute.
Each search is 1 job + the number or results it contains.

Based on the above: 
if we have 1000 keywords to search with each contains 16 results => 1000 * 16 = 16000 jobs.

We expect this to take about 16000/120 ~ 133 minutes ~ 2.5 hours

If you want to scrape many keywords then it's better to use the Database Provider in
combination with Kubernetes for convenience and start multiple scrapers in more than 1 machines.

## References

For more instruction you may also read the following links

- https://blog.gkomninos.com/how-to-extract-data-from-google-maps-using-golang
- https://blog.gkomninos.com/distributed-google-maps-scraping
- https://github.com/omkarcloud/google-maps-scraper/tree/master (also a nice project) [many thanks for the idea to extract the data by utilizing the JS objects]


## Licence

This code is licensed under the MIT License


## Contributing

Please open an ISSUE or make a Pull Request


Thank you for considering support for the project. Every bit of assistance helps maintain momentum and enhances the scraper’s capabilities!




## Sponsors

### Special Thanks to:

[Scrapeless](https://www.scrapeless.com/): One-click to scrape Google search results, supporting 15+ SERP scenarios such as Google Maps/Scholars/Jobs, $0.1/thousand queries, 0.2s response.

**[👉 Free Trial](https://app.scrapeless.com/passport/login?utm_source=gosom&utm_campaign=google-maps)**

![Scrapeless](./img/scrapeless_dark.png#gh-dark-mode-only)

![Scrapeless](./img/scrapeless_light.png#gh-light-mode-only)


<br>

[Evomi](https://evomi.com?utm_source=github&utm_medium=banner&utm_campaign=gosom-maps) is your Swiss Quality Proxy Provider, starting at **$0.49/GB**

- 👩‍💻 **$0.49 per GB Residential Proxies**: Our price is unbeatable
- 👩‍💻 **24/7 Expert Support**: We will join your Slack Channel
- 🌍 **Global Presence**: Available in 150+ Countries
- ⚡ **Low Latency**
- 🔒 **Swiss Quality and Privacy**
- 🎁 **Free Trial**
- 🛡️ **99.9% Uptime**
- 🤝 **Special IP Pool selection**: Optimize for fast, quality or quantity of ips
- 🔧 **Easy Integration**: Compatible with most software and programming languages

[![Evomi Banner](https://my.evomi.com/images/brand/cta.png)](https://evomi.com?utm_source=github&utm_medium=banner&utm_campaign=gosom-maps)

<br>

[![Google Maps API for easy SERP scraping](https://www.searchapi.io/press/v1/svg/searchapi_logo_black_h.svg)](https://www.searchapi.io/google-maps?via=gosom)
**Google Maps API for easy SERP scraping**



### Premium Sponsors

<table>
<tr>
<td>
<a href="https://gmapsextractor.com?utm_source=github&utm_medium=banner&utm_campaign=gosom">
<img src="img/gmaps-extractor-logo.png" alt="G Maps Extractor Logo" width="100">
</a>
</td>
<td>
<b>G Maps Extractor</b>  
A no-code Google Maps scraper that pulls business leads from Google Maps in one click.

- 📇 **Includes** emails, social profiles, phone numbers, addresses, reviews, images and more.
- 📥 **Export** to CSV · Excel · JSON  
- 🎁 **Free**: Get your first **1,000 leads** today  
<a href="https://gmapsextractor.com?utm_source=github&utm_medium=banner&utm_campaign=gosom">Get Started for Free</a>
</td>
</tr>
</table>
<hr>

<table>
<tr>
<td><img src="./img/SerpApi-logo-w.png" alt="SerpApi Logo" width="100"></td>
<td>
<b>At SerpApi, we scrape public data from Google Maps and other top search engines.</b>

You can find the full list of our APIs here: [https://serpapi.com/search-api](https://serpapi.com/search-api)
</td>
</tr>
</table>

For more information, see [document](serpapi.md).


<hr>

**No time for code? Extract ALL Google Maps listings at country-scale in 2 clicks, without keywords or limits** 👉 [Try it now for free](https://scrap.io?utm_medium=ads&utm_source=github_gosom_gmap_scraper)

[![Extract ALL Google Maps Listings](./img/premium_scrap_io.png)](https://scrap.io?utm_medium=ads&utm_source=github_gosom_gmap_scraper)

For more information, see [scrap.io demo](scrap_io.md).


### Supported by the Community

[Supported by the community](https://github.com/sponsors/gosom)


## Notes

Please use this scraper responsibly and in accordance with all applicable laws and regulations. Unauthorized scraping of data may violate the terms of service of the website being scraped.

banner is generated using OpenAI's DALL-E
> **Note:** If you register via the links on my page, I may get a commission. This is another way to support my work
