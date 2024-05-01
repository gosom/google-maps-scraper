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
	"time"

	// postgres driver
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/adapters/writers/jsonwriter"
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

	writers := []scrapemate.ResultWriter{}

	if args.json {
		writers = append(writers, jsonwriter.NewJSONWriter(resultsWriter))
	} else {
		writers = append(writers, csvWriter)
	}

	opts := []func(*scrapemateapp.Config) error{
		// scrapemateapp.WithCache("leveldb", "cache"),
		scrapemateapp.WithConcurrency(args.concurrency),
		scrapemateapp.WithExitOnInactivity(args.exitOnInactivityDuration),
	}

	if args.debug {
		opts = append(opts, scrapemateapp.WithJS(
			scrapemateapp.Headfull(),
			scrapemateapp.DisableImages(),
		),
		)
	} else {
		opts = append(opts, scrapemateapp.WithJS(scrapemateapp.DisableImages()))
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

	seedJobs, err := createSeedJobs(args.langCode, input, args.maxDepth, args.email)
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
		scrapemateapp.WithExitOnInactivity(args.exitOnInactivityDuration),
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

	jobs, err := createSeedJobs(args.langCode, input, args.maxDepth, args.email)
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

func createSeedJobs(langCode string, r io.Reader, maxDepth int, email bool) (jobs []scrapemate.IJob, err error) {
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		query := strings.TrimSpace(scanner.Text())
		if query == "" {
			continue
		}

		var id string

		if before, after, ok := strings.Cut(query, "#!#"); ok {
			query = strings.TrimSpace(before)
			id = strings.TrimSpace(after)
		}

		jobs = append(jobs, gmaps.NewGmapJob(id, langCode, query, maxDepth, email))
	}

	return jobs, scanner.Err()
}

func installPlaywright() error {
	return playwright.Install()
}

type arguments struct {
	concurrency              int
	cacheDir                 string
	maxDepth                 int
	inputFile                string
	resultsFile              string
	json                     bool
	langCode                 string
	debug                    bool
	dsn                      string
	produceOnly              bool
	exitOnInactivityDuration time.Duration
	email                    bool
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

	flag.IntVar(&args.concurrency, "c", defaultConcurency, "sets the concurrency. By default it is set to half of the number of CPUs")
	flag.StringVar(&args.cacheDir, "cache", "cache", "sets the cache directory (no effect at the moment)")
	flag.IntVar(&args.maxDepth, "depth", defaultDepth, "is how much you allow the scraper to scroll in the search results. Experiment with that value")
	flag.StringVar(&args.resultsFile, "results", "stdout", "is the path to the file where the results will be written")
	flag.StringVar(&args.inputFile, "input", "stdin", "is the path to the file where the queries are stored (one query per line). By default it reads from stdin")
	flag.StringVar(&args.langCode, "lang", "en", "is the languate code to use for google (the hl urlparam).Default is en . For example use de for German or el for Greek")
	flag.BoolVar(&args.debug, "debug", false, "Use this to perform a headfull crawl (it will open a browser window) [only when using without docker]")
	flag.StringVar(&args.dsn, "dsn", "", "Use this if you want to use a database provider")
	flag.BoolVar(&args.produceOnly, "produce", false, "produce seed jobs only (only valid with dsn)")
	flag.DurationVar(&args.exitOnInactivityDuration, "exit-on-inactivity", 0, "program exits after this duration of inactivity(example value '5m')")
	flag.BoolVar(&args.json, "json", false, "Use this to produce a json file instead of csv (not available when using db)")
	flag.BoolVar(&args.email, "email", false, "Use this to extract emails from the websites")

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
