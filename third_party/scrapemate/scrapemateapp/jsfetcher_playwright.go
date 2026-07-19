package scrapemateapp

import (
	"github.com/gosom/scrapemate"
	jsfetcher "github.com/gosom/scrapemate/adapters/fetchers/jshttp"
)

func (app *ScrapemateApp) getJSFetcher(rotator scrapemate.ProxyRotator) (scrapemate.HTTPFetcher, error) {
	return jsfetcher.New(jsfetcher.JSFetcherOptions{
		Headless:           !app.cfg.JSOpts.Headfull,
		DisableImages:      app.cfg.JSOpts.DisableImages,
		Rotator:            rotator,
		PoolSize:           app.cfg.derivedBrowserPoolSize(),
		MaxPagesPerBrowser: app.cfg.MaxPagesPerBrowser,
		PageReuseLimit:     app.cfg.PageReuseLimit,
		BrowserReuseLimit:  app.cfg.BrowserReuseLimit,
		UserAgent:          app.cfg.JSOpts.UA,
	})
}
