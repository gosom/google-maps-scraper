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
	// just install playwright
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
	const (
		defaultDepth      = 10
		defaultCPUDivider = 2
	)

	var (
		concurrency int
		cacheDir    string
		maxDepth    int
		inputFile   string
		resultsFile string
		langCode    string
		debug       bool
	)

	flag.IntVar(&concurrency, "c", runtime.NumCPU()/defaultCPUDivider, "concurrency")
	flag.StringVar(&cacheDir, "cache", "cache", "cache directory")
	flag.IntVar(&maxDepth, "depth", defaultDepth, "max depth")
	flag.StringVar(&resultsFile, "results", "stdout", "results file")
	flag.StringVar(&inputFile, "input", "stdin", "input file")
	flag.StringVar(&langCode, "lang", "en", "language code")
	flag.BoolVar(&debug, "debug", false, "debug")

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

	opts := []func(*scrapemateapp.Config) error{
		// scrapemateapp.WithCache("leveldb", "cache"),
		scrapemateapp.WithConcurrency(concurrency),
	}

	if debug {
		opts = append(opts, scrapemateapp.WithJS(scrapemateapp.Headfull()))
	} else {
		opts = append(opts, scrapemateapp.WithJS())
	}

	cfg, err := scrapemateapp.NewConfig(
		writers,
		opts...,
	)
	if err != nil {
		return err
	}

	app, err := scrapemateapp.NewScrapeMateApp(cfg)
	if err != nil {
		return err
	}

	seedJobs, err := createSeedJobs(langCode, input, maxDepth)
	if err != nil {
		return err
	}

	return app.Start(context.Background(), seedJobs...)
}

func createSeedJobs(langCode string, r io.Reader, maxDepth int) (jobs []scrapemate.IJob, err error) {
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		query := strings.TrimSpace(scanner.Text())
		if query == "" {
			continue
		}

		jobs = append(jobs, gmaps.NewGmapJob(langCode, query, maxDepth))
	}

	return jobs, scanner.Err()
}

func installPlaywright() error {
	return playwright.Install()
}
