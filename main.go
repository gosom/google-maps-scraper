package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"flag"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
	"github.com/playwright-community/playwright-go"

	"github.com/gosom/google-maps-scraper/gmaps"
)

func main() {
	// just install playwrighy
	if os.Getenv("PLAYWRIGHT_INSTALL_ONLY") == "1" {
		if err := installPlaywright(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	if err := run(); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
		return
	}
	os.Exit(0)
}

func run() error {
	var (
		concurrency int
		cacheDir    string
		maxDepth    int
		inputFile   string
		resultsFile string
	)
	flag.IntVar(&concurrency, "c", runtime.NumCPU()/2, "concurrency")
	flag.StringVar(&cacheDir, "cache", "cache", "cache directory")
	flag.IntVar(&maxDepth, "depth", 10, "max depth")
	flag.StringVar(&resultsFile, "results", "stdout", "results file")
	flag.StringVar(&inputFile, "input", "stdin", "input file")
	flag.Parse()

	var input io.Reader
	switch inputFile {
	case "stdin":
		input = os.Stdin
	default:
		f, err := os.Open(inputFile)
		if err != nil {
			return err
		}
		defer f.Close()
		input = f
	}

	var resultsWriter io.Writer
	switch resultsFile {
	case "stdout":
		resultsWriter = os.Stdout
	default:
		f, err := os.Create(resultsFile)
		if err != nil {
			return err
		}
		defer f.Close()
		resultsWriter = f
	}

	csvWriter := csvwriter.NewCsvWriter(csv.NewWriter(resultsWriter))

	writers := []scrapemate.ResultWriter{
		csvWriter,
	}

	cfg, err := scrapemateapp.NewConfig(
		writers,
		scrapemateapp.WithCache("leveldb", "cache"),
		scrapemateapp.WithConcurrency(concurrency),
		scrapemateapp.WithJS(),
	)
	if err != nil {
		return err
	}
	app, err := scrapemateapp.NewScrapeMateApp(cfg)
	if err != nil {
		return err
	}

	seedJobs, err := createSeedJobs(input, maxDepth)
	if err != nil {
		return err
	}
	return app.Start(context.Background(), seedJobs...)
}

func createSeedJobs(r io.Reader, maxDepth int) (jobs []scrapemate.IJob, err error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		query := strings.TrimSpace(scanner.Text())
		if query == "" {
			continue
		}
		jobs = append(jobs, gmaps.NewGmapJob(query, maxDepth))
	}
	return jobs, scanner.Err()
}

func installPlaywright() error {
	return playwright.Install()
}
