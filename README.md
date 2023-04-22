# Google maps scraper

A command line google maps parser build using [scrapemate](https://github.com/gosom/scrapemate)

You can use this repository either as is, or you can use it's code as a base and
customize it to your needs

## Quickstart

```
git clone
cd google-maps-scraper
go mod download
go build
./google-maps-scraper -input example-queries.txt -results restaurants-in-cyprus.csv
```

Be a little bit patient. In the first run it downloads required libraries.

In general perfomance can be better. Right, now it requires between 8-20 seconds to fetch and parse 
a place page.

For the initial searches it requires between 20-70 seconds. 

The results are written when they arrive in the `results` file you specified

try `./google-maps-scraper -h` to see the command line options available:

```
  -c int
        concurrency (default 4)
  -cache string
        cache directory (default "cache")
  -depth int
        max depth (default 10)
  -input string
        input file (default "stdin")
  -results string
        results file (default "stdout")
```

`depth` is how much you allow the scraper to scroll in the search results. 
Experiment with that value a bit


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

## Licence

This code is licenced under the MIT Licence


## Contributing

Please open an ISSUE or make a Pull Request


## Notes

Please use this scraper responsibly

