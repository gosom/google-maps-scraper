package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"io"
	"log"
	"os"

	// postgres driver
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"

	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/adapters/writers/jsonwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
	"github.com/playwright-community/playwright-go"

	"github.com/gosom/google-maps-scraper/constants"
	"github.com/gosom/google-maps-scraper/scrape_app"

	"github.com/gosom/google-maps-scraper/jobs"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/utils"
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
	args := models.ParseArgs()
	err := godotenv.Load()
	if err != nil {
		log.Println("[WARN] Error loading .env file")
	}
	if args.Dsn == "" && len(os.Getenv(constants.POSTGREST_CONN)) <= 0 {
		return runFromLocalFile(ctx, &args)
	}

	return runFromDatabase(ctx, &args)
}

func runFromLocalFile(ctx context.Context, args *models.Arguments) error {
	var input io.Reader

	switch args.InputFile {
	case "stdin":
		input = os.Stdin
	default:
		f, err := os.Open(args.InputFile)
		if err != nil {
			return err
		}

		defer f.Close()

		input = f
	}

	var resultsWriter io.Writer

	switch args.ResultsFile {
	case "stdout":
		resultsWriter = os.Stdout
	default:
		f, err := os.Create(args.ResultsFile)
		if err != nil {
			return err
		}

		defer f.Close()

		resultsWriter = f
	}

	csvWriter := csvwriter.NewCsvWriter(csv.NewWriter(resultsWriter))

	writers := []scrapemate.ResultWriter{}

	if args.Json {
		writers = append(writers, jsonwriter.NewJSONWriter(resultsWriter))
	} else {
		writers = append(writers, csvWriter)
	}

	opts := []func(*scrapemateapp.Config) error{
		// scrapemateapp.WithCache("leveldb", "cache"),
		scrapemateapp.WithConcurrency(args.Concurrency),
		scrapemateapp.WithExitOnInactivity(args.ExitOnInactivityDuration),
	}

	if args.Debug {
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

	app, err := scrape_app.NewGoogleMapScrapApp(cfg, utils.GetProxiesFromTxtFile(args.ProxyTxtFile)...)
	if err != nil {
		return err
	}

	seedJobs, err := jobs.CreateSeedJobs(args.LangCode, input, args.MaxDepth, args.Email, args.UseLatLong)
	if err != nil {
		return err
	}

	return app.Start(ctx, seedJobs...)
}

func runFromDatabase(ctx context.Context, args *models.Arguments) error {

	dbConn := args.Dsn
	if len(os.Getenv(constants.POSTGREST_CONN)) > 0 {
		dbConn = os.Getenv(constants.POSTGREST_CONN)
	}

	db, err := openPsqlConn(dbConn)
	if err != nil {
		return err
	}

	provider := postgres.NewProvider(db)

	if args.ProduceOnly {
		return jobs.ProduceSeedJobs(ctx, args, provider)
	}

	psqlWriter := postgres.NewResultWriter(db)

	writers := []scrapemate.ResultWriter{
		psqlWriter,
	}

	opts := []func(*scrapemateapp.Config) error{
		// scrapemateapp.WithCache("leveldb", "cache"),
		scrapemateapp.WithConcurrency(args.Concurrency),
		scrapemateapp.WithProvider(provider),
		scrapemateapp.WithExitOnInactivity(args.ExitOnInactivityDuration),
	}

	if args.Debug {
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

	app, err := scrape_app.NewGoogleMapScrapApp(cfg, utils.GetProxiesFromTxtFile(args.ProxyTxtFile)...)
	if err != nil {
		return err
	}

	return app.Start(ctx)
}

func installPlaywright() error {
	return playwright.Install()
}

func openPsqlConn(dsn string) (conn *sql.DB, err error) {
	conn, err = sql.Open("pgx", dsn)
	if err != nil {
		return
	}

	err = conn.Ping()

	return
}
