package databaserunner

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"

	// postgres driver
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/gosom/google-maps-scraper/postgres"
	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/google-maps-scraper/tlmt"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/scrapemateapp"
)

type dbrunner struct {
	cfg      *runner.Config
	provider scrapemate.JobProvider
	produce  bool
	app      *scrapemateapp.ScrapemateApp
	conn     *sql.DB
}

func New(cfg *runner.Config) (runner.Runner, error) {
	if cfg.RunMode != runner.RunModeDatabase && cfg.RunMode != runner.RunModeDatabaseProduce {
		return nil, fmt.Errorf("%w: %d", runner.ErrInvalidRunMode, cfg.RunMode)
	}

	conn, err := openPsqlConn(cfg.Dsn)
	if err != nil {
		return nil, err
	}

	ans := dbrunner{
		cfg:      cfg,
		provider: postgres.NewProvider(conn),
		produce:  cfg.ProduceOnly,
		conn:     conn,
	}

	if ans.produce {
		return &ans, nil
	}

	psqlWriter := postgres.NewResultWriter(conn)

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
		d.cfg.GeoCoordinates,
		d.cfg.Zoom,
		d.cfg.Radius,
		nil,
		nil,
		d.cfg.ExtraReviews,
		d.cfg.ReviewsLimit,
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
