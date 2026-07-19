package jshttp

import (
	"context"
	"errors"
	"time"

	"github.com/playwright-community/playwright-go"

	"github.com/gosom/scrapemate"
	playwrightadapter "github.com/gosom/scrapemate/adapters/browsers/playwright"
)

var _ scrapemate.HTTPFetcher = (*jsFetch)(nil)

// closeTimeout is the maximum time allowed for a Playwright page.Close() before
// the caller abandons it. A wedged Playwright driver (e.g. EPIPE after a browser
// crash) can make Close block forever; abandoning it frees the worker goroutine
// at the cost of leaking the underlying resource.
const closeTimeout = 5 * time.Second

// closeWithTimeout runs closer in a background goroutine and returns when it
// completes or when d elapses, whichever comes first. On timeout the goroutine
// is abandoned (it will finish on its own if the driver recovers). Without this
// bound, a hung Close propagates through the worker WaitGroup and prevents the
// caller from being unblocked even by context cancellation.
func closeWithTimeout(closer func() error, d time.Duration) {
	done := make(chan struct{})

	go func() {
		_ = closer()

		close(done)
	}()

	select {
	case <-done:
	case <-time.After(d):
	}
}

type JSFetcherOptions struct {
	Headless           bool
	DisableImages      bool
	Rotator            scrapemate.ProxyRotator
	PoolSize           int
	MaxPagesPerBrowser int
	PageReuseLimit     int
	BrowserReuseLimit  int
	UserAgent          string
}

func New(params JSFetcherOptions) (scrapemate.HTTPFetcher, error) {
	opts := []*playwright.RunOptions{
		{
			Browsers: []string{"chromium"},
			Verbose:  true,
		},
	}

	if err := playwright.Install(opts...); err != nil {
		return nil, err
	}

	pw, err := playwright.Run()
	if err != nil {
		return nil, err
	}

	var pool *ProxyPool

	if params.Rotator != nil {
		proxies := params.Rotator.Proxies()

		if len(proxies) > 0 {
			pool, err = NewProxyPool(proxies)
			if err != nil {
				return nil, err
			}
		}
	}

	maxPagesPerBrowser := params.MaxPagesPerBrowser
	if maxPagesPerBrowser < 1 {
		maxPagesPerBrowser = 1
	}

	ans := jsFetch{
		pw:                 pw,
		headless:           params.Headless,
		disableImages:      params.DisableImages,
		pageReuseLimit:     params.PageReuseLimit,
		browserReuseLimit:  params.BrowserReuseLimit,
		ua:                 params.UserAgent,
		proxyPool:          pool,
		rotator:            params.Rotator,
		maxPagesPerBrowser: maxPagesPerBrowser,
	}

	if maxPagesPerBrowser > 1 {
		ans.pageSlots, err = newPageSlotPool(pageSlotPoolConfig{
			poolSize:           params.PoolSize,
			maxPagesPerBrowser: maxPagesPerBrowser,
			factory: playwrightSlotFactory{
				pw:            pw,
				headless:      params.Headless,
				disableImages: params.DisableImages,
				proxyPool:     pool,
				ua:            params.UserAgent,
			},
		})
		if err != nil {
			_ = ans.Close()

			return nil, err
		}

		return &ans, nil
	}

	ans.slots = make(chan *sessionSlot, params.PoolSize)

	sessionFactory := &playwrightRuntimeFactory{
		pw:            pw,
		headless:      params.Headless,
		disableImages: params.DisableImages,
		proxyPool:     pool,
		ua:            params.UserAgent,
	}
	ans.factory = sessionFactory

	for range params.PoolSize {
		ans.slots <- newSessionSlot(sessionFactory)
	}

	return &ans, nil
}

type jsFetch struct {
	pw                *playwright.Playwright
	headless          bool
	disableImages     bool
	pageReuseLimit    int
	browserReuseLimit int
	ua                string
	proxyPool         *ProxyPool
	factory           runtimeFactory
	slots             chan *sessionSlot
	pageSlots         *pageSlotPool

	rotator            scrapemate.ProxyRotator
	maxPagesPerBrowser int
}

func (o *jsFetch) getSlot(ctx context.Context) (*sessionSlot, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case slot := <-o.slots:
		return slot, nil
	}
}

func (o *jsFetch) putSlot(ctx context.Context, slot *sessionSlot) {
	select {
	case <-ctx.Done():
		_ = slot.close()
	case o.slots <- slot:
	}
}

func (o *jsFetch) Close() error {
	if o.pageSlots != nil {
		o.pageSlots.close()
	}

	if o.slots != nil {
		close(o.slots)

		for slot := range o.slots {
			slot.close()
		}
	}

	_ = o.pw.Stop()

	return nil
}

// Fetch fetches the url specicied by the job and returns the response
func (o *jsFetch) Fetch(ctx context.Context, job scrapemate.IJob) scrapemate.Response {
	if o.maxPagesPerBrowser > 1 {
		return o.fetchWithPageSlot(ctx, job)
	}

	slot, err := o.getSlot(ctx)
	if err != nil {
		return scrapemate.Response{
			Error: err,
		}
	}

	defer o.putSlot(ctx, slot)

	p, err := slot.acquirePage(ctx)
	if err != nil {
		return scrapemate.Response{
			Error: err,
		}
	}

	pp, ok := p.(*playwrightPage)
	if !ok {
		return scrapemate.Response{
			Error: errors.New("unexpected page type"),
		}
	}

	if job.GetTimeout() > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, job.GetTimeout())

		defer cancel()

		pp.playwrightPage().SetDefaultTimeout(float64(job.GetTimeout().Milliseconds()))
	}

	resp := runBrowserActions(ctx, job, playwrightadapter.NewPage(pp.playwrightPage()))

	if cleanErr := slot.release(ctx); cleanErr != nil && resp.Error == nil {
		resp.Error = cleanErr
	}

	return resp
}

func (o *jsFetch) fetchWithPageSlot(ctx context.Context, job scrapemate.IJob) scrapemate.Response {
	if job.GetTimeout() > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, job.GetTimeout())

		defer cancel()
	}

	lease, err := o.pageSlots.acquire(ctx)
	if err != nil {
		return scrapemate.Response{Error: err}
	}

	defer lease.release(ctx)

	page, err := lease.slot.ctx.NewPage()
	if err != nil {
		return scrapemate.Response{Error: err}
	}

	defer closeWithTimeout(func() error { return page.Close() }, closeTimeout)

	if job.GetTimeout() > 0 {
		page.SetDefaultTimeout(float64(job.GetTimeout().Milliseconds()))
	}

	lease.slot.mu.Lock()
	lease.slot.browserUsage++
	lease.slot.mu.Unlock()

	return runBrowserActions(ctx, job, playwrightadapter.NewPage(page))
}

func runBrowserActions(ctx context.Context, job scrapemate.IJob, page scrapemate.BrowserPage) scrapemate.Response {
	if hooks, ok := page.(interface{ ClearNetworkHooks() }); ok {
		defer hooks.ClearNetworkHooks()
	}

	return job.BrowserActions(ctx, page)
}

type browser struct {
	browser      playwright.Browser
	ctx          playwright.BrowserContext
	page0Usage   int
	browserUsage int
}

func (o *browser) Close() {
	_ = o.ctx.Close()
	_ = o.browser.Close()
}

func newBrowser(pw *playwright.Playwright, headless, disableImages bool, proxyPool *ProxyPool, ua string) (*browser, error) {
	opts := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(headless),
		Args: []string{
			`--start-maximized`,
			`--no-default-browser-check`,
			`--disable-dev-shm-usage`,
			`--no-sandbox`,
			`--disable-setuid-sandbox`,
			`--no-zygote`,
			`--disable-gpu`,
			`--mute-audio`,
			`--disable-extensions`,
			`--disable-breakpad`,
			`--disable-features=TranslateUI,BlinkGenPropertyTrees`,
			`--disable-ipc-flooding-protection`,
			`--enable-features=NetworkService,NetworkServiceInProcess`,
			"--enable-features=NetworkService",
			`--disable-default-apps`,
			`--disable-notifications`,
			`--disable-webgl`,
			`--disable-blink-features=AutomationControlled`,
			"--ignore-certificate-errors",
			"--ignore-certificate-errors-spki-list",
			"--disable-web-security",
		},
	}
	if disableImages {
		opts.Args = append(opts.Args, `--blink-settings=imagesEnabled=false`)
	}

	br, err := pw.Chromium.Launch(opts)
	if err != nil {
		return nil, err
	}

	const defaultWidth, defaultHeight = 1920, 1080

	bctx, err := br.NewContext(playwright.BrowserNewContextOptions{
		UserAgent: func() *string {
			if ua == "" {
				defaultUA := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

				return &defaultUA
			}

			return &ua
		}(),
		Viewport: &playwright.Size{
			Width:  defaultWidth,
			Height: defaultHeight,
		},
		Proxy: func() *playwright.Proxy {
			if proxyPool != nil {
				authProxy := proxyPool.Next()

				addr := authProxy.Address()

				return &playwright.Proxy{
					Server: addr,
				}
			}

			return nil
		}(),
	})
	if err != nil {
		return nil, err
	}

	ans := browser{
		browser: br,
		ctx:     bctx,
	}

	return &ans, nil
}
