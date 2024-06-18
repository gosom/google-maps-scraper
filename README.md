# Google maps scraper
![build](https://github.com/gosom/google-maps-scraper/actions/workflows/build.yml/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/gosom/google-maps-scraper)](https://goreportcard.com/report/github.com/gosom/google-maps-scraper)

> A command line google maps scraper

---

<div align="center">
	<p>
		<p>
			<sup>
				<a href="https://github.com/sponsors/gosom">supported by the community</a>
			</sup>
		</p>
		<sup>Special thanks to:</sup>
		<br>
		<br>
		<a href="https://www.searchapi.io/google-maps?via=gosom" rel="nofollow">
            <div>
              <img src="https://www.searchapi.io/press/v1/svg/searchapi_logo_black_h.svg" width="180" alt="Google Maps API for easy SERP scraping"/>
            </div>
            <b>Google Maps API for easy SERP scraping</b>
		</a>
	</p>
</div>

---

![Google maps scraper](https://github.com/gosom/google-maps-scraper/blob/main/banner.png)

## 🚀 Please [vote](https://github.com/gosom/google-maps-scraper/discussions/61) for the next features

A command line google maps scraper build using 

[scrapemate](https://github.com/gosom/scrapemate) web crawling framework.

You can use this repository either as is, or you can use it's code as a base and
customize it to your needs

**Update** Added email extraction from business website support

## Try it

```
touch results.csv && docker run -v $PWD/example-queries.txt:/example-queries -v $PWD/results.csv:/results.csv gosom/google-maps-scraper -depth 1 -input /example-queries -results /results.csv -exit-on-inactivity 3m
```

file `results.csv` will contain the parsed results.

**If you want emails use additionally the `-email` parameter**


## 🌟 Support the Project!

If you find this tool useful, consider giving it a **star** on GitHub. 
Feel free to check out the **Sponsor** button on this repository to see how you can further support the development of this project. 
Your support helps ensure continued improvement and maintenance.


## Features

- Extracts many data points from google maps
- Exports the data to CSV, JSON or PostgreSQL 
- Perfomance about 120 urls per minute (-depth 1 -c 8)
- Extendable to write your own exporter
- Dockerized for easy run in multiple platforms
- Scalable in multiple machines
- Optionally extracts emails from the website of the business

## Notes on email extraction

By defaul email extraction is disabled. 

If you enable email extraction (see quickstart) then the scraper will visit the 
website of the business (if exists) and it will try to extract the emails from the
page.

For the moment it only checks only one page of the website (the one that is registered in Gmaps). At some point, it will be added support to try to extract from other pages like about, contact, impressum etc. 


Keep in mind that enabling email extraction results to larger processing time, since more
pages are scraped. 


## Extracted Data Points

```
input_id
link
title
category
address
open_hours
popular_times
website
phone
plus_code
review_count
review_rating
reviews_per_rating
latitude
longitude
cid
status
descriptions
reviews_link
thumbnail
timezone
price_range
data_id
images
reservations
order_online
menu
owner
complete_address
about
user_reviews
emails
```

**Note**: email is empty by default (see Usage)

**Note**: Input id is an ID that you can define per query. By default its a UUID
In order to define it you can have an input file like:

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


### On your host

(tested only on Ubuntu 22.04)


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
  -c int
        sets the concurrency. By default it is set to half of the number of CPUs (default 8)
  -cache string
        sets the cache directory (no effect at the moment) (default "cache")
  -debug
        Use this to perform a headfull crawl (it will open a browser window) [only when using without docker]
  -depth int
        is how much you allow the scraper to scroll in the search results. Experiment with that value (default 10)
  -dsn string
        Use this if you want to use a database provider
  -email
        Use this to extract emails from the websites
  -exit-on-inactivity duration
        program exits after this duration of inactivity(example value '5m')
  -input string
        is the path to the file where the queries are stored (one query per line). By default it reads from stdin (default "stdin")
  -json
        Use this to produce a json file instead of csv (not available when using db)
  -lang string
        is the languate code to use for google (the hl urlparam).Default is en . For example use de for German or el for Greek (default "en")
  -produce
        produce seed jobs only (only valid with dsn)
  -results string
        is the path to the file where the results will be written (default "stdout")
```


## Using Database Provider (postgreSQL)

For running in your local machine:

```
docker-compose -f docker-compose.dev.yaml up -d
```

The above starts a PostgreSQL contains and creates the required tables

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
      app: google-maps-scraper
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


## Perfomance

Expected speed with concurrency of 8 and depth 1 is 120 jobs/per minute.
Each search is 1 job + the number or results it contains.

Based on the above: 
if we have 1000 keywords to search with each contains 16 results => 1000 * 16 = 16000 jobs.

We expect this to take about 16000/120 ~ 133 minutes ~ 2.5 hours

If you want to scrape many keywords then it's better to use the Database Provider in
combination with Kubernetes for convenience and start multipe scrapers in more than 1 machines.

## References

For more instruction you may also read the following links

- https://blog.gkomninos.com/how-to-extract-data-from-google-maps-using-golang
- https://blog.gkomninos.com/distributed-google-maps-scraping
- https://github.com/omkarcloud/google-maps-scraper/tree/master (also a nice project) [many thanks for the idea to extract the data by utilizing the JS objects]


## Licence

This code is licenced under the MIT Licence


## Contributing

Please open an ISSUE or make a Pull Request


Thank you for considering support for the project. Every bit of assistance helps maintain momentum and enhances the scraper’s capabilities!



## Notes

Please use this scraper responsibly

banner is generated using OpenAI's DALE

## Sponsors

<a href="https://www.searchapi.io/?via=gosom" rel="nofollow"> searchapi.com</a> sponsors this project via Github sponsors.

If you register via the links on my page I get a commission. This is another way to support my work

