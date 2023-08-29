# Google maps scraper
![build](https://github.com/gosom/google-maps-scraper/actions/workflows/build.yml/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/gosom/google-maps-scraper)](https://goreportcard.com/report/github.com/gosom/google-maps-scraper)

A command line google maps parser build using 

[scrapemate](https://github.com/gosom/scrapemate) web crawling framework.

You can use this repository either as is, or you can use it's code as a base and
customize it to your needs

## **Maintainers wanted**

Google frequentyl changes the layout of the pages and the CSS selectors needs to be adjusted and I would like some help. 

Please report if the tool is broken or even better make a Pull Request with the fix.

A small request please. If you use or like the program please ⭐ the repository, it may help to find some maintainers. 

Thanks


## Quickstart

### Using docker:

```
touch results.csv && docker run -v $PWD/example-queries.txt:/example-queries -v $PWD/results.csv:/results.csv gosom/google-maps-scraper -depth 1 -input /example-queries -results /results.csv
```

file `results.csv` will contain the parsed results.


### On your host

(tested only on Ubuntu 22.04)


```
git clone https://github.com/gosom/google-maps-scraper.git
cd google-maps-scraper
go mod download
go build
./google-maps-scraper -input example-queries.txt -results restaurants-in-cyprus.csv
```

Be a little bit patient. In the first run it downloads required libraries.

The results are written when they arrive in the `results` file you specified

### Command line options

try `./google-maps-scraper -h` to see the command line options available:

```
  -c int
        concurrency (default 4)
  -cache string
        cache directory (default "cache")
  -debug
        debug
  -depth int
        max depth (default 10)
  -input string
        input file (default "stdin")
  -lang string
        language code (default "en")
  -results string
        results file (default "stdout")
```

`-c`: sets the concurrency. By default it uses half of the number of the CPUs detected


`-cache`: sets the cache directory (no effect for the moment)

`-debug`: Uses this to perform a headfull crawl (it will open a browser in your host)

`-depth`: is how much you allow the scraper to scroll in the search results. 
Experiment with that value a bit

`-input`: the input file with the keywords to search (see example-queries.txt)

`-lang`: is the language code to use for google (the `hl` urlparam). Default is `en`. For example use `de` for German or `el` for Greek.

`-results`: is the path to write the results


## Extracted Data

- Title: the title of the business
- Category: the category of the business
- Address: the address of the business
- OpenHours: the opening hours of the business
- WebSite: the website of the business
- Phone: the phone number of the business
- PlusCode: the plus code of the business
- ReviewCount: the number of reviews for the business
- ReviewRating: the rating of the business
- Latitude: the latitude of the business
- Longtitude: the longitude of the business

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

At the moment when you run it with concurrency 4 and with depth 10 it takes around:

around 30 seconds to scrape each search

and around 8 seconds to scrape each business listing

So assuming that depth 10 contains 100 listings then for each search keyword 
we expect: 30 + 100*8 = 330 seconds. 

If we have 1000 keywords to search we expect in total: 1000 *330 = 330000 seconds ~ 92 hours ~ 4 days

If you want to scrape multiple keywords then it's better to use the Database Provider in
combination with Kubernetes for convenience

## References

For more instruction you may also read the following links

- https://blog.gkomninos.com/how-to-extract-data-from-google-maps-using-golang
- https://blog.gkomninos.com/distributed-google-maps-scraping


## Licence

This code is licenced under the MIT Licence


## Contributing

Please open an ISSUE or make a Pull Request


## Notes

Please use this scraper responsibly

