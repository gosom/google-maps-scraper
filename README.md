# Google maps scraper

## What Google maps scraper does

A command line and web based google maps scraper build using 

[scrapemate](https://github.com/gosom/scrapemate) web crawling framework.

You can use this repository either as is, or you can use its code as a base and
customize it to your needs

![Example GIF](img/example.gif)

### Web UI:

#### Option 1: Using Docker (Recommended for beginners)

**Prerequisites:**
- Install [Docker Desktop](https://www.docker.com/products/docker-desktop/) for your platform
- Make sure Docker is running

**Quick Start:**
```bash
# Create a directory for your data
mkdir -p gmapsdata

# Run the scraper with Docker
docker run -v $PWD/gmapsdata:/gmapsdata -p 8080:8080 gosom/google-maps-scraper -data-folder /gmapsdata
```

**What this does:**
- `-v $PWD/gmapsdata:/gmapsdata`: Mounts your local `gmapsdata` folder into the container
- `-p 8080:8080`: Maps port 8080 from the container to your local machine
- `-data-folder /gmapsdata`: Tells the scraper where to save results

**Access the web interface:**
Open your browser and go to: `http://localhost:8080`

#### Option 2: Using Docker Compose (For advanced users)

If you have a PostgreSQL database running on your machine:

```bash
# Clone the repository
git clone https://github.com/gosom/google-maps-scraper.git
cd google-maps-scraper

# Build and run with Docker Compose
docker compose -f docker-compose.staging.yaml up --build -d

# Access the web interface
open http://localhost:8080
```

#### Option 3: Download Binary

Download the [binary](https://github.com/gosom/google-maps-scraper/releases) for your platform and run it directly.

**Note:** The results will take at least 3 minutes to appear, even if you add only one keyword. This is the minimum configured runtime.


### Command Line:

#### Using Docker (Recommended)

**Prerequisites:**
- Install [Docker Desktop](https://www.docker.com/products/docker-desktop/)
- Create a file with your search queries (one per line)

**Quick Start:**
```bash
# Create your queries file
echo "restaurants in New York" > example-queries.txt
echo "coffee shops in San Francisco" >> example-queries.txt

# Create an empty results file
touch results.csv

# Run the scraper
docker run -v $PWD/example-queries.txt:/example-queries \
           -v $PWD/results.csv:/results.csv \
           gosom/google-maps-scraper \
           -depth 1 \
           -input /example-queries \
           -results /results.csv \
           -exit-on-inactivity 3m
```

**What each parameter does:**
- `-depth 1`: Scrape only the first page of results (faster)
- `-input /example-queries`: Use your queries file
- `-results /results.csv`: Save results to CSV file
- `-exit-on-inactivity 3m`: Stop after 3 minutes of no activity

**For email extraction, add the `-email` parameter:**
```bash
docker run -v $PWD/example-queries.txt:/example-queries \
           -v $PWD/results.csv:/results.csv \
           gosom/google-maps-scraper \
           -depth 1 \
           -input /example-queries \
           -results /results.csv \
           -exit-on-inactivity 3m \
           -email
```

**Results:**
The `results.csv` file will contain all the parsed data from Google Maps.

### REST API
The Google Maps Scraper provides a RESTful API for programmatic management of scraping tasks.

#### Getting Started with the API

**Start the API server:**
```bash
# Using Docker (recommended)
docker run -p 8080:8080 gosom/google-maps-scraper -web

# Or with database support
docker run -p 8080:8080 -e DSN="postgres://user:pass@host:5432/dbname" gosom/google-maps-scraper -web
```

**Access API documentation:**
Open your browser and go to: `http://localhost:8080/api/docs`

#### Key Endpoints

- **POST /api/v1/jobs**: Create a new scraping job
- **GET /api/v1/jobs**: List all jobs
- **GET /api/v1/jobs/{id}**: Get details of a specific job
- **DELETE /api/v1/jobs/{id}**: Delete a job
- **GET /api/v1/jobs/{id}/download**: Download job results as CSV

#### Example API Usage

```bash
# Create a new scraping job
curl -X POST http://localhost:8080/api/v1/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Restaurants in NYC",
    "keywords": ["restaurants", "food"],
    "lang": "en",
    "depth": 5
  }'

# List all jobs
curl http://localhost:8080/api/v1/jobs

# Get job details
curl http://localhost:8080/api/v1/jobs/{job-id}
```

For detailed API documentation, refer to the OpenAPI 3.0.3 specification available through Swagger UI or Redoc when running the app at `http://localhost:8080/api/docs`

## Brezel.ai Staging API

This project serves as the backend API for Brezel.ai staging environment.

### New Endpoints Added:
- **GET /health** - Health check for infrastructure monitoring
- **GET /api/v1/status** - API status with feature information

### Staging Deployment:
- **URL**: https://staging.brezel.ai
- **Health**: https://staging.brezel.ai/health  
- **Status**: https://staging.brezel.ai/api/v1/status
- **API Docs**: https://staging.brezel.ai/api/docs

### Features:
- ✅ Google Maps scraping functionality
- ✅ REST API with authentication
- ✅ PostgreSQL database integration
- ✅ Usage limiting and monitoring
- ✅ Docker containerization
- ✅ GitHub Actions CI/CD

### Staging Setup for Developers:

**Prerequisites:**
- Docker and Docker Compose installed
- PostgreSQL running on the host machine
- Git access to the repository

**Quick Start:**
```bash
# Clone the repository
git clone https://github.com/gosom/google-maps-scraper.git
cd google-maps-scraper

# Build and run with Docker Compose
docker compose -f docker-compose.staging.yaml up --build -d

# Verify the API is running
curl http://localhost:8080/health
curl http://localhost:8080/api/v1/status
```

**Database Configuration:**
The staging setup connects to a PostgreSQL database running outside Docker. Update the DSN in `docker-compose.staging.yaml`:

```yaml
environment:
  - DSN=postgres://username:password@host.docker.internal:5432/database_name?sslmode=disable
```

**For Linux servers:** If `host.docker.internal` is not available, use the host's IP address instead.

## Troubleshooting

### Common Docker Issues

**"Cannot connect to the Docker daemon"**
- Make sure Docker Desktop is running
- On Linux, you might need to add your user to the docker group: `sudo usermod -aG docker $USER`

**"Port already in use"**
- Stop any existing containers: `docker stop $(docker ps -q)`
- Or use a different port: `docker run -p 8081:8080 ...`

**"Permission denied" on mounted volumes**
- On Linux/Mac, ensure the directory has proper permissions
- Try: `chmod 755 gmapsdata`

**Container exits immediately**
- Check logs: `docker logs <container-name>`
- Ensure all required parameters are provided
- For API mode, make sure the database connection string is correct

**Database connection issues**
- Verify PostgreSQL is running: `pg_isready -h localhost -p 5432`
- Check firewall settings
- Ensure the database user has proper permissions

### Getting Help

- Check the [Issues](https://github.com/gosom/google-maps-scraper/issues) page
- Review the logs: `docker logs <container-name>`
- Test database connectivity from inside the container:
  ```bash
  docker run --rm postgres:15 psql -h host.docker.internal -U your_user -d your_database
  ```


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

### Setting up the Database

Before using PostgreSQL with this tool, you need to set up the database with proper permissions:

```bash
# Run the database setup script as a superuser (e.g., postgres)
psql -U postgres -f scripts/setup_db.sql
```

This script creates:
- The `google_maps_scraper` database
- The `scraper` user with proper permissions
- All necessary tables and migrations

See [DATABASE.md](DATABASE.md) for detailed setup instructions.

### Running with PostgreSQL

Once the database is set up, you can run the scraper with PostgreSQL:

```bash
# Run the web interface with PostgreSQL
./google-maps-scraper -web -dsn "postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable"

# Or produce jobs to process
./google-maps-scraper -dsn "postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable" -produce -input example-queries.txt --lang en

# Run workers to process the jobs
./google-maps-scraper -c 2 -depth 1 -dsn "postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable"
```

### Using Docker Compose (Development)

For development, you can use the included docker-compose file:

```bash
docker-compose -f docker-compose.dev.yaml up -d
```

This starts a PostgreSQL container with the required tables.

```bash
# Access the development database
psql -h localhost -U postgres -d postgres
# Password is postgres
```

If you have a database server and several machines, you can start multiple instances of the scraper as above.

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

## Security

### Automated Security Scanning

This project includes automated security scanning with [gosec](https://github.com/securego/gosec). 

**Quick security check:**
```bash
# Check for critical issues (SQL injection, XSS, hardcoded secrets)
make sec-critical

# Full security scan
make sec

# Generate HTML report
make sec-html
```

## References

For more instruction you may also read the following links

- https://blog.gkomninos.com/how-to-extract-data-from-google-maps-using-golang
- https://blog.gkomninos.com/distributed-google-maps-scraping
- https://github.com/omkarcloud/google-maps-scraper/tree/master (also a nice project) [many thanks for the idea to extract the data by utilizing the JS objects]


## Licence

This code is licensed under the MIT License





