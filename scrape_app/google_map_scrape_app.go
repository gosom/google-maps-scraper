package scrape_app

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"time"

	"github.com/gosom/scrapemate"
	"golang.org/x/sync/errgroup"

	"github.com/gosom/google-maps-scraper/fetchers"
	"github.com/gosom/scrapemate/adapters/cache/filecache"
	"github.com/gosom/scrapemate/adapters/cache/leveldbcache"

	"github.com/gosom/google-maps-scraper/utils"
	parser "github.com/gosom/scrapemate/adapters/parsers/goqueryparser"
	memprovider "github.com/gosom/scrapemate/adapters/providers/memory"
	"github.com/gosom/scrapemate/scrapemateapp"
)

type GoogleMapScrapApp struct {
	cfg *scrapemateapp.Config

	ctx    context.Context
	cancel context.CancelCauseFunc

	provider  scrapemate.JobProvider
	cacher    scrapemate.Cacher
	proxyURLS []string
}

func NewGoogleMapScrapApp(cfg *scrapemateapp.Config, proxyUrls ...string) (*GoogleMapScrapApp, error) {
	app := GoogleMapScrapApp{
		cfg:       cfg,
		proxyURLS: proxyUrls,
	}

	return &app, nil
}

// Start starts the app.
func (app *GoogleMapScrapApp) Start(ctx context.Context, seedJobs ...scrapemate.IJob) error {
	g, ctx := errgroup.WithContext(ctx)
	ctx, cancel := context.WithCancelCause(ctx)

	defer cancel(errors.New("closing app"))

	mate, err := app.getMate(ctx)
	if err != nil {
		return err
	}

	defer app.Close()

	for i := range app.cfg.Writers {
		writer := app.cfg.Writers[i]

		g.Go(func() error {
			if err := writer.Run(ctx, mate.Results()); err != nil {
				cancel(err)
				return err
			}
			return nil
		})
	}

	g.Go(func() error {
		return mate.Start()
	})

	g.Go(func() error {
		for i := range seedJobs {
			if err := app.provider.Push(ctx, seedJobs[i]); err != nil {
				return err
			}
		}

		return nil
	})

	return g.Wait()
}

// Close closes the app.
func (app *GoogleMapScrapApp) Close() error {
	if app.cacher != nil {
		app.cacher.Close()
	}

	return nil
}

func (app *GoogleMapScrapApp) getMate(ctx context.Context) (*scrapemate.ScrapeMate, error) {
	var err error

	app.provider, err = app.getProvider()
	if err != nil {
		return nil, err
	}

	fetcherInstance, err := app.getFetcher()
	if err != nil {
		return nil, err
	}

	app.cacher, err = app.getCacher()
	if err != nil {
		return nil, err
	}

	params := []func(*scrapemate.ScrapeMate) error{
		scrapemate.WithContext(ctx, app.cancel),
		scrapemate.WithJobProvider(app.provider),
		scrapemate.WithHTTPFetcher(fetcherInstance),
		scrapemate.WithHTMLParser(parser.New()),
		scrapemate.WithConcurrency(app.cfg.Concurrency),
		scrapemate.WithExitBecauseOfInactivity(app.cfg.ExitOnInactivityDuration),
	}

	if app.cacher != nil {
		params = append(params, scrapemate.WithCache(app.cacher))
	}

	if app.cfg.InitJob != nil {
		params = append(params, scrapemate.WithInitJob(app.cfg.InitJob))
	}

	return scrapemate.New(params...)
}

func (app *GoogleMapScrapApp) getCacher() (scrapemate.Cacher, error) {
	var (
		cacher scrapemate.Cacher
		err    error
	)

	switch app.cfg.CacheType {
	case "file":
		cacher, err = filecache.NewFileCache(app.cfg.CachePath)
	case "leveldb":
		cacher, err = leveldbcache.NewLevelDBCache(app.cfg.CachePath)
	}

	return cacher, err
}

func (app *GoogleMapScrapApp) getFetcher() (scrapemate.HTTPFetcher, error) {
	var (
		httpFetcher scrapemate.HTTPFetcher
		err         error
	)

	const timeout = 10 * time.Second

	switch app.cfg.UseJS {
	case true:
		httpFetcher, err = fetchers.New(!app.cfg.JSOpts.Headfull, app.cfg.JSOpts.DisableImages, app.proxyURLS...)
		if err != nil {
			return nil, err
		}
	default:
		cookieJar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}

		netClient := &http.Client{
			Timeout: timeout,
			Jar:     cookieJar,
		}
		if app.proxyURLS != nil && len(app.proxyURLS) > 0 {
			netClient.Transport = &http.Transport{
				Proxy: http.ProxyURL(utils.ToUrlProxy(app.proxyURLS[0])),
			}
		}
	}

	return httpFetcher, nil
}

//nolint:unparam // this function returns always nil error
func (app *GoogleMapScrapApp) getProvider() (scrapemate.JobProvider, error) {
	var provider scrapemate.JobProvider

	switch app.cfg.Provider {
	case nil:
		provider = memprovider.New()
	default:
		provider = app.cfg.Provider
	}

	return provider, nil
}
