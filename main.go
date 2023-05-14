package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/csv"
	"flag"
	"io"
	"os"
	"runtime"
	"strings"

	// postgres driver
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
	"github.com/playwright-community/playwright-go"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/postgres"
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
	ctx := context.Background()
	args := parseArgs()

	if args.dsn == "" {
		return runFromLocalFile(ctx, &args)
	}

	return runFromDatabase(ctx, &args)
}

func runFromLocalFile(ctx context.Context, args *arguments) error {
	var input io.Reader

	switch args.inputFile {
	case "stdin":
		input = os.Stdin
	default:
		f, err := os.Open(args.inputFile)
		if err != nil {
			return err
		}

		defer f.Close()

		input = f
	}

	var resultsWriter io.Writer

	switch args.resultsFile {
	case "stdout":
		resultsWriter = os.Stdout
	default:
		f, err := os.Create(args.resultsFile)
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
		scrapemateapp.WithConcurrency(args.concurrency),
	}

	if args.debug {
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

	seedJobs, err := createSeedJobs(args.langCode, input, args.maxDepth)
	if err != nil {
		return err
	}

	return app.Start(ctx, seedJobs...)
}

func runFromDatabase(ctx context.Context, args *arguments) error {
	db, err := openPsqlConn(args.dsn)
	if err != nil {
		return err
	}

	provider := postgres.NewProvider(db)

	if args.produceOnly {
		return produceSeedJobs(ctx, args, provider)
	}

	psqlWriter := postgres.NewResultWriter(db)

	writers := []scrapemate.ResultWriter{
		psqlWriter,
	}

	opts := []func(*scrapemateapp.Config) error{
		// scrapemateapp.WithCache("leveldb", "cache"),
		scrapemateapp.WithConcurrency(args.concurrency),
		scrapemateapp.WithProvider(provider),
	}

	if args.debug {
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

	return app.Start(ctx)
}

func produceSeedJobs(ctx context.Context, args *arguments, provider scrapemate.JobProvider) error {
	var input io.Reader

	switch args.inputFile {
	case "stdin":
		input = os.Stdin
	default:
		f, err := os.Open(args.inputFile)
		if err != nil {
			return err
		}

		defer f.Close()

		input = f
	}

	jobs, err := createSeedJobs(args.langCode, input, args.maxDepth)
	if err != nil {
		return err
	}

	for i := range jobs {
		if err := provider.Push(ctx, jobs[i]); err != nil {
			return err
		}
	}

	return nil
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

type arguments struct {
	concurrency int
	cacheDir    string
	maxDepth    int
	inputFile   string
	resultsFile string
	langCode    string
	debug       bool
	dsn         string
	produceOnly bool
}

func parseArgs() (args arguments) {
	const (
		defaultDepth      = 10
		defaultCPUDivider = 2
	)

	defaultConcurency := runtime.NumCPU() / defaultCPUDivider
	if defaultConcurency < 1 {
		defaultConcurency = 1
	}

	flag.IntVar(&args.concurrency, "c", defaultConcurency, "concurrency")
	flag.StringVar(&args.cacheDir, "cache", "cache", "cache directory")
	flag.IntVar(&args.maxDepth, "depth", defaultDepth, "max depth")
	flag.StringVar(&args.resultsFile, "results", "stdout", "results file")
	flag.StringVar(&args.inputFile, "input", "stdin", "input file")
	flag.StringVar(&args.langCode, "lang", "en", "language code")
	flag.BoolVar(&args.debug, "debug", false, "debug")
	flag.StringVar(&args.dsn, "dsn", "", "Use this if you want to use a database provider")
	flag.BoolVar(&args.produceOnly, "produce", false, "produce seed jobs only (only valid with dsn)")

	flag.Parse()

	return args
}

func openPsqlConn(dsn string) (conn *sql.DB, err error) {
	conn, err = sql.Open("pgx", dsn)
	if err != nil {
		return
	}

	err = conn.Ping()

	return
}
