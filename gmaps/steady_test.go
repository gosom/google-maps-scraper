//nolint:testpackage // tests collector v2's unexported internals
package gmaps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const steadyQuery = "roofing contractor in Travis County TX"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)

	return b
}

func TestResultsRequestURLComesFromTheSearchPage(t *testing.T) {
	got, err := resultsRequestURL(readFixture(t, "steady_search_page.html"))
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(got, "https://www.google.com/search?tbm=map&"), got)
	assert.Contains(t, got, "&q=roofing+contractor+in+Travis+County+TX&")
	assert.NotContains(t, got, "&amp;")

	_, err = resultsRequestURL([]byte("<html>consent</html>"))
	assert.ErrorIs(t, err, errNoResultsRequest)
}

func TestResultsPageURLAddsTheOffsetAfterThePageSize(t *testing.T) {
	first := "https://www.google.com/search?tbm=map&pb=%211sq%217i20%2110b1"

	got, err := resultsPageURL(first, 0)
	require.NoError(t, err)
	assert.Equal(t, first, got)

	got, err = resultsPageURL(first, 40)
	require.NoError(t, err)
	assert.Equal(t, "https://www.google.com/search?tbm=map&pb=%211sq%217i20%218i40%2110b1", got)

	_, err = resultsPageURL("https://www.google.com/search?tbm=map&pb=%211sq", 20)
	assert.Error(t, err)
}

func TestParseResultsPageReadsEachPlaceFully(t *testing.T) {
	entries, err := parseResultsPage(readFixture(t, "steady_rpc_page0.txt"))
	require.NoError(t, err)
	require.Len(t, entries, 3)

	first := entries[0]
	assert.Equal(t, "Austin Roofing Company", first.Title)
	assert.Equal(t, "https://austinroofingcompany.com/", first.WebSite)
	assert.Equal(t, "ChIJn-vEVrO1RIYRTFOINIMEGQA", first.PlaceID)
	assert.Equal(t, "https://www.google.com/maps/place/?q=place_id:ChIJn-vEVrO1RIYRTFOINIMEGQA", first.Link)
	assert.True(t, strings.HasPrefix(first.DataID, "0x"))
	assert.NotEmpty(t, first.Address)
	assert.NotEmpty(t, first.Category)

	for _, e := range entries {
		assert.NotEmpty(t, e.PlaceID, e.Title)
		assert.NotEmpty(t, e.Link, e.Title)
	}
}

// fakeMaps answers v2's Google requests from a table of URL substrings, in order.
type fakeMaps struct {
	mu       sync.Mutex
	routes   []googleRoute
	asked    []string
	switches int
}

type googleRoute struct {
	contains string
	resp     func(n int) (rpcResponse, error) // n: how many times this route has answered
	n        int
}

func (g *fakeMaps) get(_ context.Context, u string) (rpcResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.asked = append(g.asked, u)

	for i := range g.routes {
		if strings.Contains(u, g.routes[i].contains) {
			r := &g.routes[i]
			r.n++

			return r.resp(r.n)
		}
	}

	return rpcResponse{Status: http.StatusNotFound, FinalURL: u}, nil
}

func (g *fakeMaps) next() { g.switches++ }

func (g *fakeMaps) close() {}

func okBody(b []byte) func(int) (rpcResponse, error) {
	return func(int) (rpcResponse, error) { return rpcResponse{Status: http.StatusOK, Body: b}, nil }
}

// googleFor answers the search page for steadyQuery, and results pages by offset from pages.
func googleFor(t *testing.T, pages map[string][]byte) *fakeMaps {
	t.Helper()

	g := &fakeMaps{}
	g.routes = append(g.routes, googleRoute{
		contains: "/maps/search/" + strings.ReplaceAll(steadyQuery, " ", "+"),
		resp:     okBody(readFixture(t, "steady_search_page.html")),
	})

	for marker, body := range pages {
		g.routes = append(g.routes, googleRoute{contains: marker, resp: okBody(body)})
	}

	return g
}

// firstPage is the marker of the offset-0 results request; later pages carry "%218i<offset>".
const firstPage = "%217i20%2110b1"

// testClock is a fake clock that sleep advances.
type testClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *testClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	c.mu.Unlock()

	return ctx.Err()
}

// noBackoff is a block guard whose back-off has always already passed.
func noBackoff() *BlockGuard {
	var calls int64

	return &BlockGuard{now: func() time.Time {
		calls++

		return time.Unix(0, 0).Add(time.Duration(calls) * time.Hour)
	}}
}

func newTestSteady(cfg SteadyConfig, google googleGetter, sites map[string]string, clock *testClock) *SteadyCollector {
	return &SteadyCollector{
		cfg:    cfg.WithDefaults(),
		google: google,
		site: func(_ context.Context, u string) ([]byte, error) {
			best := ""

			for prefix := range sites {
				if strings.HasPrefix(u, prefix) && len(prefix) > len(best) {
					best = prefix
				}
			}

			page, ok := sites[best]

			switch {
			case !ok:
				return nil, errors.New("HTTP 404")
			case strings.HasPrefix(page, "ERR "):
				return nil, errors.New(strings.TrimPrefix(page, "ERR "))
			default:
				return []byte(page), nil
			}
		},
		sleep:  clock.Sleep,
		now:    clock.Now,
		seen:   map[string]bool{},
		blocks: noBackoff(),
	}
}

// runSteady runs one collector over the queries and returns what it emitted and observed.
func runSteady(t *testing.T, c *SteadyCollector, queries ...string) (rows map[string][]*Entry, runs []SearchRun) {
	t.Helper()

	rows = map[string][]*Entry{}

	qs := make([]SteadyQuery, 0, len(queries))
	for _, q := range queries {
		qs = append(qs, SteadyQuery{ID: q, Text: q})
	}

	err := c.Run(context.Background(), qs,
		func(q SteadyQuery, es []*Entry) error { rows[q.ID] = append(rows[q.ID], es...); return nil },
		func(r SearchRun) { runs = append(runs, r) })
	require.NoError(t, err)

	return rows, runs
}

func TestSteadyReadsASearchAndEachWebsiteInTurn(t *testing.T) {
	clock := newTestClock()
	google := googleFor(t, map[string][]byte{firstPage: readFixture(t, "steady_rpc_page0.txt")})
	c := newTestSteady(SteadyConfig{Delay: time.Second}, google, map[string]string{
		"https://austinroofingcompany.com": `<a href="mailto:office@austinroofingcompany.com">mail</a>`,
		"https://www.loaconstruction.com":  "ERR HTTP 403",
		"https://www.eliteroofpro.com":     `<p>Call us</p>`,
	}, clock)

	rows, runs := runSteady(t, c, steadyQuery)

	entries := rows[steadyQuery]
	require.Len(t, entries, 3)

	for _, e := range entries {
		assert.Equal(t, steadyQuery, e.ID, "input_id carries the search")
	}

	assert.Equal(t, []string{"office@austinroofingcompany.com"}, entries[0].Emails)
	assert.Equal(t, EmailFound, entries[0].EmailStatus)
	assert.Equal(t, EmailFetchFailed+"http_4xx", entries[1].EmailStatus)
	assert.Equal(t, EmailNone, entries[2].EmailStatus)

	require.Len(t, runs, 1)
	run := runs[0]
	assert.Equal(t, SearchOK, run.Status)
	assert.Equal(t, SteadyMethod, run.Method)
	assert.Equal(t, 1, run.Pages)
	assert.Equal(t, 3, run.PlacesFound)
	assert.Equal(t, 3, run.Websites)
	assert.Equal(t, 3, run.EmailAttempted)
	assert.Equal(t, 1, run.EmailsFound)
	assert.Equal(t, 1, run.EmailNone)
	assert.Equal(t, 1, run.EmailFetchFailed)
	assert.Equal(t, 1, run.UniqueEmails)
	assert.Equal(t, time.Second, run.MinSiteGap, "websites are fetched one after another, Delay apart")
	assert.Equal(t, []time.Duration{time.Second, time.Second}, clock.sleeps, "a gap before the 2nd and 3rd site")
	assert.Equal(t, 2*time.Second, run.FinishedAt.Sub(run.StartedAt))
}

func TestSteadyReadsTheFrontPageOnlyUnlessAskedForMore(t *testing.T) {
	front := `<a href="/contact">Contact</a>`
	sites := map[string]string{
		"https://austinroofingcompany.com/contact": `<a href="mailto:hi@austinroofingcompany.com">x</a>`,
		"https://austinroofingcompany.com":         front,
	}
	page := readFixture(t, "steady_rpc_page0.txt")

	for _, tc := range []struct {
		contactPages int
		want         []string
	}{{0, []string{}}, {1, []string{"hi@austinroofingcompany.com"}}} {
		c := newTestSteady(SteadyConfig{ContactPages: tc.contactPages},
			googleFor(t, map[string][]byte{firstPage: page}), sites, newTestClock())
		rows, _ := runSteady(t, c, steadyQuery)
		assert.Equal(t, tc.want, rows[steadyQuery][0].Emails, "contact pages %d", tc.contactPages)
	}
}

// fullPage is a results page of 20 distinct places, made from the fixture's first place.
func fullPage(t *testing.T) []byte {
	t.Helper()

	body := readFixture(t, "steady_rpc_page0.txt")
	nl := strings.IndexByte(string(body), '\n')

	var root []any

	require.NoError(t, json.Unmarshal(body[nl+1:], &root))

	places, _ := root[64].([]any)
	proto, _ := places[0].([]any)

	list := make([]any, 0, steadyPageSize)

	for i := range steadyPageSize {
		var item []any

		raw, err := json.Marshal(proto)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &item))

		place, _ := item[1].([]any)
		place[10] = fmt.Sprintf("0x1:0x%d", i)
		place[78] = fmt.Sprintf("ChIJsynthetic%02d", i)
		place[7] = nil // no website: the paging test is about places

		list = append(list, item)
	}

	root[64] = list

	out, err := json.Marshal(root)
	require.NoError(t, err)

	return append([]byte(")]}'\n"), out...)
}

func TestSteadyReadsPagesUntilAShortOne(t *testing.T) {
	clock := newTestClock()
	google := googleFor(t, map[string][]byte{
		firstPage: fullPage(t),
		"%218i20": readFixture(t, "steady_rpc_page1.txt"),
	})
	c := newTestSteady(SteadyConfig{Pages: 3, Delay: 2 * time.Second}, google, nil, clock)

	rows, runs := runSteady(t, c, steadyQuery)

	assert.Len(t, rows[steadyQuery], 23)
	assert.Equal(t, 2, runs[0].Pages, "the second page was short, so there is no third request")

	for _, u := range google.asked {
		assert.NotContains(t, u, "%218i40")
	}

	assert.Equal(t, 2*time.Second, clock.sleeps[0], "a gap between results pages")
}

func TestSteadyDoesNotRepeatAPlaceAnEarlierSearchFound(t *testing.T) {
	page := readFixture(t, "steady_rpc_page0.txt")
	other := "roofer in Austin TX"
	google := googleFor(t, map[string][]byte{firstPage: page})
	google.routes = append(google.routes, googleRoute{
		contains: "/maps/search/roofer+in+Austin+TX",
		resp:     okBody(readFixture(t, "steady_search_page.html")),
	})

	c := newTestSteady(SteadyConfig{}, google, nil, newTestClock())
	rows, runs := runSteady(t, c, steadyQuery, other)

	assert.Len(t, rows[steadyQuery], 3)
	assert.Empty(t, rows[other])
	assert.Equal(t, 3, runs[1].Duplicates)
	assert.Equal(t, SearchOK, runs[1].Status, "a search whose places were all seen before still found them")
	assert.Equal(t, 2, google.switches, "the next identity after every search")
}

func TestSteadyRetriesARefusalOnTheNextIdentity(t *testing.T) {
	google := googleFor(t, map[string][]byte{firstPage: readFixture(t, "steady_rpc_page0.txt")})
	search := google.routes[0].resp
	google.routes[0].resp = func(n int) (rpcResponse, error) {
		if n == 1 {
			return rpcResponse{Status: http.StatusTooManyRequests}, nil
		}

		return search(n)
	}

	c := newTestSteady(SteadyConfig{Retries: 1}, google, nil, newTestClock())
	rows, runs := runSteady(t, c, steadyQuery)

	assert.Len(t, rows[steadyQuery], 3)
	assert.Equal(t, 1, runs[0].Blocks)
	assert.Equal(t, SearchOK, runs[0].Status)
	assert.Equal(t, 1, c.blocks.Stats().Total)
}

func TestSteadyASearchRefusedOnEveryTryIsBlocked(t *testing.T) {
	google := &fakeMaps{routes: []googleRoute{{contains: "/maps/search/", resp: func(int) (rpcResponse, error) {
		return rpcResponse{Status: http.StatusOK, Body: []byte("Our systems have detected unusual traffic")}, nil
	}}}}

	c := newTestSteady(SteadyConfig{Retries: 2}, google, nil, newTestClock())
	rows, runs := runSteady(t, c, steadyQuery)

	assert.Empty(t, rows)
	assert.Equal(t, SearchBlocked, runs[0].Status)
	assert.Equal(t, 3, runs[0].Blocks)
	assert.Contains(t, runs[0].Error, "unusual-traffic")
}

func TestSteadyConsentWallIsAFailureNotABlock(t *testing.T) {
	google := &fakeMaps{routes: []googleRoute{{contains: "/maps/search/", resp: func(int) (rpcResponse, error) {
		return rpcResponse{Status: http.StatusFound, FinalURL: "https://consent.google.com/ml?continue=x"}, nil
	}}}}

	c := newTestSteady(SteadyConfig{}, google, nil, newTestClock())
	_, runs := runSteady(t, c, steadyQuery)

	assert.Equal(t, SearchFailed, runs[0].Status)
	assert.Equal(t, 0, runs[0].Blocks)
	assert.Contains(t, runs[0].Error, "consent")
}

func TestSteadyBudgetKeepsThePlacesAndSkipsTheSitesLeft(t *testing.T) {
	google := googleFor(t, map[string][]byte{firstPage: readFixture(t, "steady_rpc_page0.txt")})
	c := newTestSteady(SteadyConfig{QueryBudget: 50 * time.Millisecond}, google, nil, newTestClock())
	c.site = func(ctx context.Context, _ string) ([]byte, error) {
		<-ctx.Done() // a site that never answers

		return nil, ctx.Err()
	}

	rows, runs := runSteady(t, c, steadyQuery)

	require.Len(t, rows[steadyQuery], 3, "the places are written even though their websites were not read")

	for _, e := range rows[steadyQuery] {
		assert.Equal(t, EmailSkippedBudget, e.EmailStatus, e.Title)
	}

	assert.Equal(t, SearchPartial, runs[0].Status)
	assert.Equal(t, 3, runs[0].EmailSkipped)
	assert.Equal(t, 0, runs[0].EmailFetchFailed, "running out of time is not a site's failure")
}

func TestSteadyStopsBetweenSearchesWhenTheJobEnds(t *testing.T) {
	google := googleFor(t, map[string][]byte{firstPage: readFixture(t, "steady_rpc_page0.txt")})
	c := newTestSteady(SteadyConfig{}, google, nil, newTestClock())

	ctx, cancel := context.WithCancel(context.Background())
	emitted := 0

	err := c.Run(ctx, []SteadyQuery{{ID: "a", Text: steadyQuery}, {ID: "b", Text: "second"}},
		func(SteadyQuery, []*Entry) error { emitted++; cancel(); return nil }, nil)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, emitted, "the first search's rows are written, the second search never starts")
}

func TestSteadyConfigDefaultsLimitsAndBound(t *testing.T) {
	cfg := SteadyConfig{}.WithDefaults()
	assert.Equal(t, 1, cfg.Pages)
	assert.Equal(t, time.Second, cfg.Delay)
	assert.Equal(t, 0, cfg.ContactPages, "front page only unless asked")
	assert.False(t, cfg.NoRedirects)
	assert.Equal(t, DefaultSteadyQueryBudget, cfg.QueryBudget)
	require.NoError(t, cfg.Validate())

	for _, bad := range []SteadyConfig{
		{Pages: 7}, {ContactPages: 5}, {Retries: 4}, {Delay: time.Minute},
		{QueryBudget: 9 * time.Minute}, {QueryBudget: time.Second}, {SiteTimeout: 2 * time.Minute},
	} {
		assert.Error(t, bad.WithDefaults().Validate(), "%+v", bad)
	}

	assert.Equal(t, 10*(4*time.Minute+time.Second)+time.Minute, cfg.JobBound(10))
}
