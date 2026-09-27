package gmaps

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Collector v2 ("steady") runs one search at a time, start to finish, and nothing in parallel:
//
//	search page -> results request -> up to Pages pages of 20 places -> each place's website, one
//	after another, Delay apart -> rows written -> the search's SearchRun recorded
//
// No browser, no scrolling, no background retries. Every search has a fixed time budget
// (QueryBudget) that covers its Google requests and its websites together, so a job of N searches
// can never run longer than N x (QueryBudget + Delay): the point of v2 is that its time and its
// output are predictable. v1 (the browser collector) is untouched by any of this.

// CollectorV1 and CollectorV2 name the two collectors wherever a job says which one ran it.
const (
	CollectorV1 = "v1"
	CollectorV2 = "v2"
)

// Limits on SteadyConfig. MaxSteadyQueryBudget stays well under the dashboard's stall watchdog
// (10 minutes without a new row).
const (
	MaxSteadyPages        = 6
	MaxSteadyContactPages = 4
	MaxSteadyRetries      = 3
	MaxSteadyDelay        = 30 * time.Second
	MaxSteadySiteTimeout  = time.Minute
	MinSteadyQueryBudget  = 30 * time.Second
	MaxSteadyQueryBudget  = 8 * time.Minute
)

// SteadyConfig is how v2 runs. The zero value of every field means its default.
type SteadyConfig struct {
	// Pages of 20 places read per search (1 = the first 20).
	Pages int `json:"pages"`
	// Delay is the gap between two website fetches, two results pages, and two searches.
	Delay time.Duration `json:"delay"`
	// ContactPages is how many of a website's own contact/about pages are read after its front
	// page; 0 reads the front page only.
	ContactPages int `json:"contact_pages"`
	// NoRedirects treats a website that redirects as unreadable instead of following it.
	NoRedirects bool `json:"no_redirects"`
	// SiteTimeout bounds one website page.
	SiteTimeout time.Duration `json:"site_timeout"`
	// QueryBudget bounds one whole search: its Google requests and every website.
	QueryBudget time.Duration `json:"query_budget"`
	// Retries is how many more times a refused or failed Google request is tried, each on the
	// next proxy.
	Retries int `json:"retries"`
	// Lang is Google's interface language.
	Lang string `json:"lang"`
}

// Defaults for SteadyConfig.
const (
	DefaultSteadyDelay       = time.Second
	DefaultSteadySiteTimeout = 10 * time.Second
	DefaultSteadyQueryBudget = 4 * time.Minute
	DefaultSteadyRetries     = 1
)

// WithDefaults fills every unset field. ContactPages 0 and NoRedirects false are meaningful, so
// they are kept as they are.
func (c SteadyConfig) WithDefaults() SteadyConfig {
	if c.Pages <= 0 {
		c.Pages = 1
	}

	if c.Delay <= 0 {
		c.Delay = DefaultSteadyDelay
	}

	if c.SiteTimeout <= 0 {
		c.SiteTimeout = DefaultSteadySiteTimeout
	}

	if c.QueryBudget <= 0 {
		c.QueryBudget = DefaultSteadyQueryBudget
	}

	if c.Retries < 0 {
		c.Retries = DefaultSteadyRetries
	}

	if c.Lang == "" {
		c.Lang = "en"
	}

	return c
}

// Validate reports a setting outside its limits.
func (c SteadyConfig) Validate() error {
	switch {
	case c.Pages < 1 || c.Pages > MaxSteadyPages:
		return fmt.Errorf("v2 pages must be 1 to %d", MaxSteadyPages)
	case c.ContactPages < 0 || c.ContactPages > MaxSteadyContactPages:
		return fmt.Errorf("v2 contact pages must be 0 to %d", MaxSteadyContactPages)
	case c.Retries < 0 || c.Retries > MaxSteadyRetries:
		return fmt.Errorf("v2 retries must be 0 to %d", MaxSteadyRetries)
	case c.Delay < 0 || c.Delay > MaxSteadyDelay:
		return fmt.Errorf("v2 delay must be 0 to %s", MaxSteadyDelay)
	case c.SiteTimeout <= 0 || c.SiteTimeout > MaxSteadySiteTimeout:
		return fmt.Errorf("v2 site timeout must be up to %s", MaxSteadySiteTimeout)
	case c.QueryBudget < MinSteadyQueryBudget || c.QueryBudget > MaxSteadyQueryBudget:
		return fmt.Errorf("v2 time per search must be %s to %s", MinSteadyQueryBudget, MaxSteadyQueryBudget)
	}

	return nil
}

// JobBound is the longest a v2 job of this many searches can take: each search's budget, the
// gaps between searches, and a minute for starting and writing.
func (c SteadyConfig) JobBound(searches int) time.Duration {
	c = c.WithDefaults()

	return time.Duration(searches)*(c.QueryBudget+c.Delay) + time.Minute
}

// Statuses of one search.
const (
	SearchOK      = "ok"      // read, with places
	SearchEmpty   = "empty"   // read, no places
	SearchPartial = "partial" // its time ran out: some places or websites were not reached
	SearchBlocked = "blocked" // Google refused it on every try
	SearchFailed  = "failed"  // anything else went wrong before any place was read
)

// SearchRun is one search as it went: the per-search ledger row both collectors are compared on.
type SearchRun struct {
	Search      string    `json:"search"`
	Method      string    `json:"method"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	Pages       int       `json:"pages"`
	PlacesFound int       `json:"places_found"`
	// Duplicates are places this search showed that an earlier search of the job already found.
	Duplicates       int           `json:"duplicates"`
	Websites         int           `json:"websites"`
	EmailAttempted   int           `json:"email_attempted"`
	EmailsFound      int           `json:"emails_found"`
	EmailNone        int           `json:"email_none"`
	EmailFetchFailed int           `json:"email_fetch_failed"`
	EmailSkipped     int           `json:"email_skipped"`
	UniqueEmails     int           `json:"unique_emails"`
	Blocks           int           `json:"blocks"`
	MinSiteGap       time.Duration `json:"min_site_gap"`
	Status           string        `json:"status"`
	Error            string        `json:"error,omitempty"`
}

// Seconds is how long the search took.
func (r *SearchRun) Seconds() float64 { return r.FinishedAt.Sub(r.StartedAt).Seconds() }

// SteadyMethod is the ledger's name for v2's runs.
const SteadyMethod = "v2-http"

// SteadyQuery is one search and the id its rows carry in input_id.
type SteadyQuery struct {
	ID   string
	Text string
}

// SteadyCollector is collector v2.
type SteadyCollector struct {
	cfg    SteadyConfig
	google googleGetter
	site   pageFetcher
	sleep  func(context.Context, time.Duration) error
	now    func() time.Time
	seen   map[string]bool
	// blocks is the back-off shared with v1 (Blocks); tests use their own.
	blocks *BlockGuard
}

// NewSteadyCollector makes a v2 collector. Google is reached through the proxies (one identity
// each, in turn); websites are always fetched directly, as v1 does.
func NewSteadyCollector(cfg SteadyConfig, proxies []string) (*SteadyCollector, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	google, err := newSteadyGoogle(proxies)
	if err != nil {
		return nil, err
	}

	return &SteadyCollector{
		cfg:    cfg,
		google: google,
		site:   newSiteFetcher(cfg.SiteTimeout, !cfg.NoRedirects),
		sleep:  sleepCtx,
		now:    time.Now,
		seen:   map[string]bool{},
		blocks: Blocks,
	}, nil
}

// Close ends the collector's Google sessions.
func (c *SteadyCollector) Close() { c.google.close() }

// Config is the collector's settings, defaults filled in.
func (c *SteadyCollector) Config() SteadyConfig { return c.cfg }

// Run collects every search in order. emit gets each search's places (already carrying their
// emails) as soon as the search ends; observe gets its SearchRun. A place found by an earlier
// search is not returned again. Run stops early only when ctx ends, and returns ctx's error then.
func (c *SteadyCollector) Run(ctx context.Context, queries []SteadyQuery,
	emit func(SteadyQuery, []*Entry) error, observe func(SearchRun)) error {
	for i, q := range queries {
		if i > 0 {
			if err := c.sleep(ctx, c.cfg.Delay); err != nil {
				return err
			}
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		entries, run := c.collect(ctx, q)

		if emit != nil && len(entries) > 0 {
			if err := emit(q, entries); err != nil {
				return err
			}
		}

		if observe != nil {
			observe(run)
		}

		c.google.next()
	}

	return ctx.Err()
}

// collect is one search, inside its own time budget.
func (c *SteadyCollector) collect(parent context.Context, q SteadyQuery) ([]*Entry, SearchRun) {
	ctx, cancel := context.WithTimeout(parent, c.cfg.QueryBudget)
	defer cancel()

	run := SearchRun{Search: q.Text, Method: SteadyMethod, StartedAt: c.now().UTC()}

	entries, err := c.places(ctx, q.Text, &run)

	for _, e := range entries {
		e.ID = q.ID
	}

	run.PlacesFound = len(entries)

	c.findEmails(ctx, entries, &run)

	run.FinishedAt = c.now().UTC()
	run.Status = searchStatus(parent, ctx, len(entries)+run.Duplicates, &run, err)

	if err != nil {
		run.Error = err.Error()
	}

	return entries, run
}

// searchStatus is how the search ended.
func searchStatus(parent, ctx context.Context, places int, run *SearchRun, err error) string {
	budgetHit := ctx.Err() != nil && parent.Err() == nil

	switch {
	case budgetHit || run.EmailSkipped > 0 || (err != nil && places > 0):
		return SearchPartial
	case errors.Is(err, errRefused):
		return SearchBlocked
	case err != nil:
		return SearchFailed
	case places == 0:
		return SearchEmpty
	default:
		return SearchOK
	}
}

// places reads the search's results pages. It returns what it read even when a later page fails.
func (c *SteadyCollector) places(ctx context.Context, query string, run *SearchRun) ([]*Entry, error) {
	page, err := c.googleGet(ctx, steadySearchPageURL(query, c.cfg.Lang), run)
	if err != nil {
		return nil, err
	}

	first, err := resultsRequestURL(page)
	if err != nil {
		return nil, err
	}

	var out []*Entry

	for p := 0; p < c.cfg.Pages; p++ {
		if p > 0 {
			if err := c.sleep(ctx, c.cfg.Delay); err != nil {
				return out, err
			}
		}

		pageURL, err := resultsPageURL(first, p*steadyPageSize)
		if err != nil {
			return out, err
		}

		body, err := c.googleGet(ctx, pageURL, run)
		if err != nil {
			return out, err
		}

		entries, err := parseResultsPage(body)
		if err != nil {
			return out, err
		}

		run.Pages++

		for _, e := range entries {
			key := placeKey(e)
			if key != "" && c.seen[key] {
				run.Duplicates++

				continue
			}

			c.seen[key] = true

			out = append(out, e)
		}

		if len(entries) < steadyPageSize {
			break // the last page
		}
	}

	return out, nil
}

// errRefused is a Google request refused on every try.
var errRefused = errors.New("google refused the request")

// googleGet is one Google request, tried 1+Retries times, each retry on the next identity and
// after the shared back-off.
func (c *SteadyCollector) googleGet(ctx context.Context, u string, run *SearchRun) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if attempt > 0 {
			c.google.next()
		}

		if err := c.blocks.Wait(ctx); err != nil {
			return nil, err
		}

		resp, err := c.google.get(ctx, u)

		switch {
		case err != nil:
			lastErr = err

			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		case googleRefusal(resp) != "":
			run.Blocks++

			c.blocks.Refused()

			lastErr = fmt.Errorf("%w: %s", errRefused, googleRefusal(resp))
		case consentWall(resp):
			return nil, errors.New("google sent its cookie-consent page; v2 does not click through it")
		case resp.Status != http.StatusOK:
			lastErr = fmt.Errorf("google answered HTTP %d", resp.Status)
		default:
			c.blocks.Loaded()

			return resp.Body, nil
		}
	}

	return nil, lastErr
}

// findEmails visits each place's website in turn, Delay apart, and sets its emails and
// EmailStatus. Places left when the search's budget runs out are marked EmailSkippedBudget.
func (c *SteadyCollector) findEmails(ctx context.Context, entries []*Entry, run *SearchRun) {
	unique := map[string]bool{}

	var lastStart time.Time

	for _, e := range entries {
		if reason := e.noEmailSearchReason(); reason != "" {
			e.EmailStatus = reason

			continue
		}

		run.Websites++

		if !lastStart.IsZero() {
			if err := c.sleep(ctx, c.cfg.Delay); err != nil {
				e.EmailStatus = EmailSkippedBudget
				run.EmailSkipped++

				continue
			}
		}

		if ctx.Err() != nil {
			e.EmailStatus = EmailSkippedBudget
			run.EmailSkipped++

			continue
		}

		start := c.now()
		if gap := start.Sub(lastStart); !lastStart.IsZero() && (run.MinSiteGap == 0 || gap < run.MinSiteGap) {
			run.MinSiteGap = gap
		}

		lastStart = start
		run.EmailAttempted++

		c.findEmailsForSite(ctx, e, run)

		for _, addr := range e.Emails {
			unique[addr] = true
		}
	}

	run.UniqueEmails = len(unique)
}

// findEmailsForSite reads one website (front page, then ContactPages of its own pages).
func (c *SteadyCollector) findEmailsForSite(ctx context.Context, e *Entry, run *SearchRun) {
	site := normalizeGoogleURL(e.WebSite)

	body, err := c.site(ctx, site)
	if err != nil {
		if ctx.Err() != nil {
			e.EmailStatus = EmailSkippedBudget
			run.EmailSkipped++

			return
		}

		e.EmailStatus = EmailFetchFailed + fetchFailReason(err)
		run.EmailFetchFailed++

		return
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		doc = nil
	}

	e.Emails = findBusinessEmailsOpts(ctx, site, doc, body, c.site, emailFindOptions{
		MaxContactPages: c.cfg.ContactPages,
		Delay:           c.cfg.Delay,
		Sleep:           c.sleep,
	})
	e.EmailStatus = emailOutcome(e.Emails)

	if e.EmailStatus == EmailFound {
		run.EmailsFound++
	} else {
		run.EmailNone++
	}
}
