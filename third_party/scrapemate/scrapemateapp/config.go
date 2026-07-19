package scrapemateapp

import (
	"errors"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gosom/scrapemate"
)

type jsOptions struct {
	Headfull      bool
	DisableImages bool
	UA            string
}

type Config struct {
	Concurrency        int `validate:"required,gte=1"`
	BrowserPoolSize    int `validate:"omitempty,gte=0"`
	MaxPagesPerBrowser int `validate:"required,gte=1"`

	CacheType string `validate:"omitempty,oneof=file leveldb"`
	CachePath string `validate:"required_with=CacheType"`

	UseJS          bool   `validate:"omitempty"`
	UseStealth     bool   `validate:"omitempty"`
	StealthBrowser string `validate:"omitempty"`
	JSOpts         jsOptions

	Provider scrapemate.JobProvider

	Writers                  []scrapemate.ResultWriter `validate:"required,gt=0"`
	InitJob                  scrapemate.IJob
	ExitOnInactivityDuration time.Duration
	Proxies                  []string
	BrowserReuseLimit        int
	PageReuseLimit           int
}

func (o *Config) validate() error {
	once.Do(func() {
		validate = validator.New()
	})

	return validate.Struct(o)
}

func (o *Config) derivedBrowserPoolSize() int {
	if o.BrowserPoolSize > 0 {
		return o.BrowserPoolSize
	}

	maxPagesPerBrowser := o.MaxPagesPerBrowser
	if maxPagesPerBrowser <= 0 {
		maxPagesPerBrowser = 1
	}

	return (o.Concurrency + maxPagesPerBrowser - 1) / maxPagesPerBrowser
}

func NewConfig(writers []scrapemate.ResultWriter, options ...func(*Config) error) (*Config, error) {
	cfg := Config{
		Writers:            writers,
		Concurrency:        DefaultConcurrency,
		MaxPagesPerBrowser: 1,
	}

	for _, opt := range options {
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func WithBrowserReuseLimit(limit int) func(*Config) error {
	return func(o *Config) error {
		o.BrowserReuseLimit = limit

		return nil
	}
}

func WithPageReuseLimit(limit int) func(*Config) error {
	return func(o *Config) error {
		o.PageReuseLimit = limit

		return nil
	}
}

func WithConcurrency(concurrency int) func(*Config) error {
	return func(o *Config) error {
		o.Concurrency = concurrency

		return o.validate()
	}
}

func WithMaxPagesPerBrowser(limit int) func(*Config) error {
	return func(o *Config) error {
		o.MaxPagesPerBrowser = limit

		return o.validate()
	}
}

func WithBrowserPoolSize(size int) func(*Config) error {
	return func(o *Config) error {
		o.BrowserPoolSize = size

		return o.validate()
	}
}

func WithCache(cacheType, cachePath string) func(*Config) error {
	return func(o *Config) error {
		o.CacheType = cacheType
		o.CachePath = cachePath

		return o.validate()
	}
}

func WithJS(opts ...func(*jsOptions)) func(*Config) error {
	return func(o *Config) error {
		o.UseJS = true

		for _, opt := range opts {
			opt(&o.JSOpts)
		}

		return o.validate()
	}
}

func WithStealth(browser string) func(*Config) error {
	return func(o *Config) error {
		o.UseStealth = true
		o.StealthBrowser = browser

		return o.validate()
	}
}

func WithProvider(provider scrapemate.JobProvider) func(*Config) error {
	return func(o *Config) error {
		if provider == nil {
			return errors.New("provider cannot be nil")
		}

		o.Provider = provider

		return nil
	}
}

func WithInitJob(job scrapemate.IJob) func(*Config) error {
	return func(o *Config) error {
		o.InitJob = job

		return nil
	}
}

func WithProxies(proxies []string) func(*Config) error {
	return func(o *Config) error {
		o.Proxies = proxies

		return nil
	}
}

func Headfull() func(*jsOptions) {
	return func(o *jsOptions) {
		o.Headfull = true
	}
}

func DisableImages() func(*jsOptions) {
	return func(o *jsOptions) {
		o.DisableImages = true
	}
}

func WithUA(ua string) func(*jsOptions) {
	return func(o *jsOptions) {
		o.UA = ua
	}
}

func WithBrowserEngine(_ string) func(*jsOptions) {
	return func(_ *jsOptions) {
	}
}

func WithRodStealth() func(*jsOptions) {
	return func(_ *jsOptions) {
	}
}

func WithExitOnInactivity(duration time.Duration) func(*Config) error {
	return func(o *Config) error {
		o.ExitOnInactivityDuration = duration

		return nil
	}
}
