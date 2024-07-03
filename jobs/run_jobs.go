package jobs

import (
	"context"
	"encoding/csv"
	"io"
	"os"

	"github.com/gosom/google-maps-scraper/constants"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/scrape_app"
	"github.com/gosom/google-maps-scraper/utils"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/adapters/writers/jsonwriter"
	"github.com/gosom/scrapemate/scrapemateapp"
)

func RunFromDatabase(ctx context.Context, args *models.Arguments, jsonInput *models.JsonInput) error {

	dbConn := args.Dsn
	if len(os.Getenv(constants.POSTGREST_CONN)) > 0 {
		dbConn = os.Getenv(constants.POSTGREST_CONN)
	}

	db, err := postgres.OpenPsqlConn(dbConn)
	if err != nil {
		return err
	}

	provider := postgres.NewProvider(db)

	if args.ProduceOnly {
		return ProduceSeedJobs(ctx, args, provider, jsonInput)
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

func RunFromLocalFile(ctx context.Context, args *models.Arguments) error {
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

	seedJobs, err := CreateSeedJobs(args.LangCode, input, args.MaxDepth, args.Email, args.UseLatLong)
	if err != nil {
		return err
	}

	return app.Start(ctx, seedJobs...)
}
