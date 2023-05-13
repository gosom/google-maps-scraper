# Google maps scraper
![build](https://github.com/gosom/google-maps-scraper/actions/workflows/build.yml/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/gosom/google-maps-scraper)](https://goreportcard.com/report/github.com/gosom/google-maps-scraper)

A command line google maps parser build using 

[scrapemate](https://github.com/gosom/scrapemate) web crawling framework.

You can use this repository either as is, or you can use it's code as a base and
customize it to your needs

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
- ReviewCount: the number of reviews
- ReviewRating: the rating of the results

## Perfomance

At the moment when you run it with concurrency 4 and with depth 10 it takes around:

around 30 seconds to scrape each search

and around 8 seconds to scrape each business listing

So assuming that depth 10 contains 100 listings then for each search keyword 
we expect: 30 + 100*8 = 330 seconds. 

If we have 1000 keywords to search we expect in total: 1000 *330 = 330000 seconds ~ 92 hours ~ 4 days

One way to speedup is to split your keywords and deploy this in multiple machines for each keyword set. 

It is planned that scrapemate can autoscale at some point in the future. 
If you like to help here please create an issue so we work together on this


## Licence

This code is licenced under the MIT Licence


## Contributing

Please open an ISSUE or make a Pull Request


## Notes

Please use this scraper responsibly

