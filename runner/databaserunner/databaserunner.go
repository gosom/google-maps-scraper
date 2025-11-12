package databaserunner

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"

	// postgres driver
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/google-maps-scraper/tlmt"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/scrapemateapp"
)

type dbrunner struct {
	cfg         *runner.Config
	provider    scrapemate.JobProvider
	produce     bool
	app         *scrapemateapp.ScrapemateApp
	conn        *sql.DB
	exitMonitor exiter.Exiter
	jobRepo     models.JobRepository
}

func New(cfg *runner.Config) (runner.Runner, error) {
	if cfg.RunMode != runner.RunModeDatabase && cfg.RunMode != runner.RunModeDatabaseProduce {
		return nil, fmt.Errorf("%w: %d", runner.ErrInvalidRunMode, cfg.RunMode)
	}

	conn, err := openPsqlConn(cfg.Dsn)
	if err != nil {
		return nil, err
	}

	// Create exit monitor if max results are specified
	var exitMonitor exiter.Exiter
	if cfg.MaxResults > 0 {
		exitMonitor = exiter.New()
		exitMonitor.SetMaxResults(cfg.MaxResults)
		fmt.Printf("DEBUG: Database runner - Max results limit set to %d\n", cfg.MaxResults)
	}

	// Initialize job repository for tracking job status
	jobRepo, err := postgres.NewRepository(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create job repository: %w", err)
	}

	ans := dbrunner{
		cfg:         cfg,
		provider:    postgres.NewProvider(conn),
		produce:     cfg.ProduceOnly,
		conn:        conn,
		exitMonitor: exitMonitor,
		jobRepo:     jobRepo,
	}

	if ans.produce {
		return &ans, nil
	}

	// Choose result writer based on whether max results are enabled
	var psqlWriter scrapemate.ResultWriter
	if cfg.MaxResults > 0 && exitMonitor != nil {
		// Use enhanced result writer with exit monitor for max results support
		psqlWriter = postgres.NewEnhancedResultWriterWithExiter(conn, "cli-user", "cli-job", exitMonitor)
		fmt.Printf("DEBUG: Using enhanced result writer with max results: %d\n", cfg.MaxResults)
	} else {
		// Use basic result writer for unlimited results
		psqlWriter = postgres.NewResultWriter(conn)
		fmt.Printf("DEBUG: Using basic result writer (unlimited results)\n")
	}

	writers := []scrapemate.ResultWriter{
		psqlWriter,
	}

	opts := []func(*scrapemateapp.Config) error{
		// scrapemateapp.WithCache("leveldb", "cache"),
		scrapemateapp.WithConcurrency(cfg.Concurrency),
		scrapemateapp.WithProvider(ans.provider),
		scrapemateapp.WithExitOnInactivity(cfg.ExitOnInactivityDuration),
	}

	if len(cfg.Proxies) > 0 {
		opts = append(opts,
			scrapemateapp.WithProxies(cfg.Proxies),
		)
	}

	if !cfg.FastMode {
		if cfg.Debug {
			opts = append(opts, scrapemateapp.WithJS(
				scrapemateapp.Headfull(),
				scrapemateapp.DisableImages(),
			))
		} else {
			opts = append(opts, scrapemateapp.WithJS(scrapemateapp.DisableImages()))
		}
	} else {
		opts = append(opts, scrapemateapp.WithStealth("firefox"))
	}

	if !cfg.DisablePageReuse {
		opts = append(opts,
			scrapemateapp.WithPageReuseLimit(2),
			scrapemateapp.WithPageReuseLimit(200),
		)
	}

	matecfg, err := scrapemateapp.NewConfig(
		writers,
		opts...,
	)
	if err != nil {
		return nil, err
	}

	ans.app, err = scrapemateapp.NewScrapeMateApp(matecfg)
	if err != nil {
		return nil, err
	}

	return &ans, nil
}

func (d *dbrunner) Run(ctx context.Context) error {
	_ = runner.Telemetry().Send(ctx, tlmt.NewEvent("databaserunner.Run", nil))

	if d.produce {
		return d.produceSeedJobs(ctx)
	}

	// Note: Job cancellation is handled by the webrunner when using web interface

	// Set up context cancellation for max results if exit monitor is enabled
	if d.exitMonitor != nil {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Set the cancel function on the exit monitor
		d.exitMonitor.SetCancelFunc(cancel)

		// Start the exit monitor in a goroutine
		go d.exitMonitor.Run(ctx)

		fmt.Printf("DEBUG: Exit monitor started for max results: %d\n", d.cfg.MaxResults)
	}

	return d.app.Start(ctx)
}

func (d *dbrunner) Close(context.Context) error {
	if d.app != nil {
		return d.app.Close()
	}

	if d.conn != nil {
		return d.conn.Close()
	}

	return nil
}

func (d *dbrunner) produceSeedJobs(ctx context.Context) error {
	var input io.Reader

	switch d.cfg.InputFile {
	case "stdin":
		input = os.Stdin
	default:
		f, err := os.Open(d.cfg.InputFile)
		if err != nil {
			return err
		}

		defer f.Close()

		input = f
	}

	jobs, err := runner.CreateSeedJobs(
		d.cfg.FastMode,
		d.cfg.LangCode,
		input,
		d.cfg.MaxDepth,
		d.cfg.Email,
		d.cfg.Images,
		d.cfg.Debug,
		func() int {
			if d.cfg.ExtraReviews {
				return 1 // Default to 1 review if extra reviews enabled
			}
			return 0 // No reviews if not enabled
		}(),
		d.cfg.GeoCoordinates,
		d.cfg.Zoom,
		d.cfg.Radius,
		nil,
		d.exitMonitor, // Pass exit monitor for max results support
		d.cfg.ExtraReviews,
		d.cfg.MaxResults, // Pass max results limit from config
	)
	if err != nil {
		return err
	}

	for i := range jobs {
		if err := d.provider.Push(ctx, jobs[i]); err != nil {
			return err
		}
	}

	_ = runner.Telemetry().Send(ctx, tlmt.NewEvent("databaserunner.produceSeedJobs", map[string]any{
		"job_count": len(jobs),
	}))

	return nil
}

func openPsqlConn(dsn string) (conn *sql.DB, err error) {
	conn, err = sql.Open("pgx", dsn)
	if err != nil {
		return
	}

	err = conn.Ping()
	if err != nil {
		return
	}

	conn.SetMaxOpenConns(10)

	return
}
