package jshttp

import (
	"context"
	"errors"

	"github.com/playwright-community/playwright-go"
)

var errNoPages = errors.New("no pages available")

type page interface {
	isClosed() bool
}

type slotRuntime interface {
	pageCount() int
	closeExtraPages() error
	closeBrowser() error
	primaryPage() (page, error)
	recreatePage() error
	recreateContext() error
	recreateBrowser() error
	recycleIfNeeded() error
}

type runtimeFactory interface {
	create(context.Context) (slotRuntime, error)
}

type sessionSlot struct {
	factory runtimeFactory
	runtime slotRuntime
}

func newSessionSlot(factory runtimeFactory) *sessionSlot {
	return &sessionSlot{factory: factory}
}

func (s *sessionSlot) ensureReady(ctx context.Context) error {
	if s.runtime != nil {
		return nil
	}

	runtime, err := s.factory.create(ctx)
	if err != nil {
		return err
	}

	s.runtime = runtime

	return nil
}

func (s *sessionSlot) cleanup(_ context.Context) error {
	if s.runtime == nil {
		return nil
	}

	return s.runtime.closeExtraPages()
}

func (s *sessionSlot) close() error {
	if s.runtime == nil {
		return nil
	}

	return s.runtime.closeBrowser()
}

func (s *sessionSlot) acquirePage(ctx context.Context) (page, error) {
	if err := s.ensureReady(ctx); err != nil {
		return nil, err
	}

	p, err := s.runtime.primaryPage()
	if err == nil && !p.isClosed() {
		return p, nil
	}

	if err := s.runtime.recreatePage(); err != nil {
		if err := s.runtime.recreateContext(); err != nil {
			return nil, s.runtime.recreateBrowser()
		}
	}

	return s.runtime.primaryPage()
}

func (s *sessionSlot) release(ctx context.Context) error {
	if err := s.cleanup(ctx); err != nil {
		return err
	}

	return s.runtime.recycleIfNeeded()
}

type playwrightRuntimeFactory struct {
	pw            *playwright.Playwright
	headless      bool
	disableImages bool
	proxyPool     *ProxyPool
	ua            string
}

func (f *playwrightRuntimeFactory) create(context.Context) (slotRuntime, error) {
	b, err := newBrowser(f.pw, f.headless, f.disableImages, f.proxyPool, f.ua)
	if err != nil {
		return nil, err
	}

	return &playwrightRuntime{
		browser:       b,
		pw:            f.pw,
		headless:      f.headless,
		disableImages: f.disableImages,
		proxyPool:     f.proxyPool,
		ua:            f.ua,
	}, nil
}

type playwrightRuntime struct {
	browser       *browser
	pw            *playwright.Playwright
	headless      bool
	disableImages bool
	proxyPool     *ProxyPool
	ua            string
}

func (r *playwrightRuntime) pageCount() int {
	return len(r.browser.ctx.Pages())
}

func (r *playwrightRuntime) closeExtraPages() error {
	pages := r.browser.ctx.Pages()
	for i := 1; i < len(pages); i++ {
		pages[i].Close()
	}

	return nil
}

func (r *playwrightRuntime) closeBrowser() error {
	r.browser.Close()
	return nil
}

func (r *playwrightRuntime) primaryPage() (page, error) {
	pages := r.browser.ctx.Pages()
	if len(pages) == 0 {
		p, err := r.browser.ctx.NewPage()
		if err != nil {
			return nil, err
		}

		return &playwrightPage{p: p}, nil
	}

	return &playwrightPage{p: pages[0]}, nil
}

func (r *playwrightRuntime) recreatePage() error {
	pages := r.browser.ctx.Pages()
	for _, p := range pages {
		p.Close()
	}

	p, err := r.browser.ctx.NewPage()
	if err != nil {
		return err
	}

	r.browser.page0Usage = 0
	_ = p

	return nil
}

func (r *playwrightRuntime) recreateContext() error {
	r.browser.ctx.Close()

	ctx, err := r.browser.browser.NewContext()
	if err != nil {
		return err
	}

	r.browser.ctx = ctx
	r.browser.page0Usage = 0

	return nil
}

func (r *playwrightRuntime) recreateBrowser() error {
	r.browser.Close()

	b, err := newBrowser(r.pw, r.headless, r.disableImages, r.proxyPool, r.ua)
	if err != nil {
		return err
	}

	r.browser = b

	return nil
}

func (r *playwrightRuntime) recycleIfNeeded() error {
	return nil
}

type playwrightPage struct {
	p playwright.Page
}

func (p *playwrightPage) isClosed() bool {
	return !p.p.IsClosed()
}

func (p *playwrightPage) playwrightPage() playwright.Page {
	return p.p
}
