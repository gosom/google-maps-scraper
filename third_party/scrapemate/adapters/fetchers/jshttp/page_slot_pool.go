package jshttp

import (
	"context"
	"sync"

	"github.com/playwright-community/playwright-go"
)

type pageSlotFactory interface {
	newSlot() (*pageSlot, error)
}

type pageSlotPoolConfig struct {
	poolSize           int
	maxPagesPerBrowser int
	factory            pageSlotFactory
}

type pageSlotPool struct {
	available chan *pageSlot
	slots     []*pageSlot
}

type pageSlot struct {
	mu           sync.Mutex
	active       int
	closed       bool
	browser      playwright.Browser
	ctx          playwright.BrowserContext
	browserUsage int
}

type pageLease struct {
	pool *pageSlotPool
	slot *pageSlot
	once sync.Once
}

func newPageSlotPool(cfg pageSlotPoolConfig) (*pageSlotPool, error) {
	pool := &pageSlotPool{
		available: make(chan *pageSlot, cfg.poolSize*cfg.maxPagesPerBrowser),
		slots:     make([]*pageSlot, 0, cfg.poolSize),
	}

	for range cfg.poolSize {
		slot, err := cfg.factory.newSlot()
		if err != nil {
			pool.close()

			return nil, err
		}

		pool.slots = append(pool.slots, slot)

		for range cfg.maxPagesPerBrowser {
			pool.available <- slot
		}
	}

	return pool, nil
}

func (p *pageSlotPool) acquire(ctx context.Context) (*pageLease, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case slot := <-p.available:
		slot.mu.Lock()
		slot.active++
		slot.mu.Unlock()

		return &pageLease{pool: p, slot: slot}, nil
	}
}

func (l *pageLease) release(ctx context.Context) {
	l.once.Do(func() {
		l.slot.mu.Lock()
		l.slot.active--
		closed := l.slot.closed
		l.slot.mu.Unlock()

		if closed {
			return
		}

		select {
		case <-ctx.Done():
			return
		case l.pool.available <- l.slot:
		}
	})
}

func (p *pageSlotPool) close() {
	for _, slot := range p.slots {
		slot.mu.Lock()
		slot.closed = true
		slot.mu.Unlock()

		if slot.ctx != nil {
			_ = slot.ctx.Close()
		}

		if slot.browser != nil {
			_ = slot.browser.Close()
		}
	}
}

type playwrightSlotFactory struct {
	pw            *playwright.Playwright
	headless      bool
	disableImages bool
	proxyPool     *ProxyPool
	ua            string
}

func (f playwrightSlotFactory) newSlot() (*pageSlot, error) {
	b, err := newBrowser(f.pw, f.headless, f.disableImages, f.proxyPool, f.ua)
	if err != nil {
		return nil, err
	}

	return &pageSlot{
		browser: b.browser,
		ctx:     b.ctx,
	}, nil
}
