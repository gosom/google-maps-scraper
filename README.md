# Enhanced Google Maps Scraper

![build](https://github.com/ptncafe/google-maps-scraper/actions/workflows/build.yml/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/ptncafe/google-maps-scraper)](https://goreportcard.com/report/github.com/ptncafe/google-maps-scraper)

---

A powerful command-line Google Maps scraper built upon the original [gosom/google-maps-scraper](https://github.com/gosom/google-maps-scraper), enhanced with additional features for more versatile scraping tasks.

## 🚀 New Features

- **Json Input for Polygon to H3 Conversion**: Import geographic shapes directly in JSON format and convert them to H3 indices for precise location searches.
- **Proxy Support**: Enable scraping through proxies to bypass IP restrictions and enhance anonymity.
- **Search by Latitude/Longitude**: Perform searches using specific coordinates, allowing for targeted data collection based on geographical locations.
- **Bug Fixes**: Addressed issues related to saving entries to PostgreSQL databases, ensuring reliable data storage.
- **Click Reject Find Home**: Implement a mechanism to automatically reject homepages during scraping, focusing on relevant search results.
- **Refactoring and API Enhancements**: Streamlined codebase for improved performance and introduced new APIs for extended customization and control over scraping operations.

## 🛠️ Installation

### Clone the Repository

`
git clone https://github.com/ptncafe/google-maps-scraper.git cd google-maps-scraper
`

### Build and Run

Ensure Go is installed on your system. Then, build and run the scraper:

`
go build ./google-maps-scraper -input example-queries.json -results results.csv
`


## 📁 Example Queries

Create a file named `example-queries.json` with the following content to test the polygon to H3 conversion feature:
json [ { "query": "Phở", "polygon": [ [-122.4194, 37.7749], [-122.4194, 37.7799], [-122.4294, 37.7799], [-122.4294, 37.7749] ] } ]

This example demonstrates how to search for "Phở" within a specific area defined by a polygon.

## 📝 License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a pull request or open an issue if you encounter any problems