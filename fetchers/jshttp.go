package fetchers

import (
	"context"

	"github.com/gosom/google-maps-scraper/utils"
	"github.com/gosom/scrapemate"
	"github.com/playwright-community/playwright-go"
)

var _ scrapemate.HTTPFetcher = (*jsFetch)(nil)

func New(headless, disableImages bool, proxyURLS ...string) (scrapemate.HTTPFetcher, error) {
	if err := playwright.Install(); err != nil {
		return nil, err
	}

	const poolSize = 10

	ans := jsFetch{
		headless:      headless,
		disableImages: disableImages,
		pool:          make(chan *browser, poolSize),
		proxyURLS:     proxyURLS,
	}

	return &ans, nil
}

type jsFetch struct {
	headless      bool
	disableImages bool
	pool          chan *browser
	proxyURLS     []string
}

func (o *jsFetch) GetBrowser(ctx context.Context) (*browser, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case ans := <-o.pool:
		return ans, nil
	default:
		return newBrowser(o.headless, o.disableImages, o.proxyURLS...)
	}
}

func (o *jsFetch) PutBrowser(ctx context.Context, b *browser) {
	select {
	case <-ctx.Done():
		b.Close()
	case o.pool <- b:
	default:
		b.Close()
	}
}

// Fetch fetches the url specicied by the job and returns the response
func (o *jsFetch) Fetch(ctx context.Context, job scrapemate.IJob) scrapemate.Response {
	browser, err := o.GetBrowser(ctx)
	if err != nil {
		return scrapemate.Response{
			Error: err,
		}
	}

	defer o.PutBrowser(ctx, browser)

	if job.GetTimeout() > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, job.GetTimeout())

		defer cancel()
	}

	var page playwright.Page

	if len(browser.ctx.Pages()) > 0 {
		page = browser.ctx.Pages()[0]

		for i := 1; i < len(browser.ctx.Pages()); i++ {
			browser.ctx.Pages()[i].Close()
		}
	} else {
		page, err = browser.ctx.NewPage()
		if err != nil {
			return scrapemate.Response{
				Error: err,
			}
		}
	}

	defer page.Close()

	return job.BrowserActions(ctx, page)
}

type browser struct {
	pw      *playwright.Playwright
	browser playwright.Browser
	ctx     playwright.BrowserContext
}

func (o *browser) Close() {
	_ = o.ctx.Close()
	_ = o.browser.Close()
	_ = o.pw.Stop()
}

func newBrowser(headless, disableImages bool, proxyURLS ...string) (*browser, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, err
	}

	opts := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(headless),
		Args: []string{
			`--start-maximized`,
			`--no-default-browser-check`,
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
		Viewport: &playwright.Size{
			Width:  defaultWidth,
			Height: defaultHeight,
		},
		Proxy: utils.ToPWProxy(utils.GetRoundRobinInProxyUrl(proxyURLS)),
	})
	if err != nil {
		return nil, err
	}

	ans := browser{
		pw:      pw,
		browser: br,
		ctx:     bctx,
	}

	return &ans, nil
}
