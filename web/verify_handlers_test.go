//nolint:testpackage // tests the Verify page's unexported handlers and form parsing
package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AxioIntel/Axio-CRED/gmaps"
	"github.com/AxioIntel/Axio-CRED/web/leads"
)

func verifyServer(t *testing.T) (*Server, *fakeRepo, *leads.Store) {
	t.Helper()

	dir := t.TempDir()
	repo := &fakeRepo{}

	store, err := leads.Open(filepath.Join(dir, "leads.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	srv, err := New(NewService(repo, dir), ":0", WithLeads(store))
	require.NoError(t, err)

	return srv, repo, store
}

func postForm(t *testing.T, srv *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rec, req)

	return rec
}

func get(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

	return rec
}

func collectorScrapeForm(collector string) url.Values {
	f := url.Values{
		"keywords": {"dentist"}, "locations": {"Austin TX"}, "maxtime": {"1h"}, "lang": {"en"},
		"zoom": {"15"}, "radius": {"10000"}, "latitude": {"0"}, "longitude": {"0"}, "depth": {"5"},
	}

	if collector != "" {
		f.Set("collector", collector)
	}

	return f
}

func onlyJob(t *testing.T, repo *fakeRepo) Job {
	t.Helper()

	repo.mu.Lock()
	defer repo.mu.Unlock()

	require.Len(t, repo.jobs, 1)

	for id := range repo.jobs {
		return repo.jobs[id]
	}

	return Job{}
}

func TestTheScrapeFormIsV1UnlessItSaysV2(t *testing.T) {
	srv, repo, _ := verifyServer(t)

	rec := postForm(t, srv, "/scrape", collectorScrapeForm(""))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	job := onlyJob(t, repo)
	assert.Equal(t, gmaps.CollectorV1, job.Data.Collector)
	assert.Nil(t, job.Data.Steady)
	assert.Contains(t, rec.Body.String(), `chip-v1`)
}

func TestTheScrapeFormStartsAV2JobWithItsSettings(t *testing.T) {
	srv, repo, _ := verifyServer(t)

	f := collectorScrapeForm("v2")
	f.Set("v2_pages", "2")
	f.Set("v2_contact_pages", "1")
	f.Set("v2_delay_ms", "2000")
	f.Set("v2_site_timeout_sec", "5")
	f.Set("v2_query_budget_sec", "120")
	f.Set("v2_retries", "0")
	// v2_follow_redirects left unticked

	rec := postForm(t, srv, "/scrape", f)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	job := onlyJob(t, repo)
	require.True(t, job.Data.IsV2())
	assert.Contains(t, rec.Body.String(), `chip-v2`)

	cfg := job.Data.SteadyConfig()
	assert.Equal(t, 2, cfg.Pages)
	assert.Equal(t, 1, cfg.ContactPages)
	assert.Equal(t, 2*time.Second, cfg.Delay)
	assert.Equal(t, 5*time.Second, cfg.SiteTimeout)
	assert.Equal(t, 2*time.Minute, cfg.QueryBudget)
	assert.Equal(t, 0, cfg.Retries)
	assert.True(t, cfg.NoRedirects)
}

func TestV2RefusesSettingsOutsideItsLimits(t *testing.T) {
	srv, repo, _ := verifyServer(t)

	f := collectorScrapeForm("v2")
	f.Set("v2_query_budget_sec", "900") // over the 8-minute limit

	rec := postForm(t, srv, "/scrape", f)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "time per search")
	assert.Empty(t, repo.jobs)

	f = collectorScrapeForm("v3")
	rec = postForm(t, srv, "/scrape", f)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestJobDataValidatesTheCollector(t *testing.T) {
	base := JobData{Keywords: []string{"q"}, Lang: "en", Depth: 1, MaxTime: time.Hour}

	ok := base
	require.NoError(t, ok.Validate(), "no collector is v1")

	v2 := base
	v2.Collector = gmaps.CollectorV2
	require.NoError(t, v2.Validate(), "v2 needs no coordinates")

	grid := v2
	grid.Grid = &GridSpec{}
	require.Error(t, grid.Validate())

	fast := v2
	fast.FastMode, fast.Lat, fast.Lon = true, "1", "1"
	require.Error(t, fast.Validate())

	bad := base
	bad.Collector = "v9"
	require.Error(t, bad.Validate())
}

func TestTheVerifyPageRendersEmptyAndWithAComparison(t *testing.T) {
	srv, _, _ := verifyServer(t)

	rec := get(t, srv, "/verify")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Run smoke check")
	assert.Contains(t, rec.Body.String(), "roofing contractor in Travis County TX", "the default searches")

	start := url.Values{
		"queries": {"a in X\nb in Y"}, "runs": {"2"}, "maxtime": {"1h"}, "v1_depth": {"1"},
		"v2_pages": {"1"}, "v2_contact_pages": {"0"}, "v2_delay_ms": {"1000"}, "v2_query_budget_sec": {"240"},
		"v2_site_timeout_sec": {"10"}, "v2_retries": {"1"}, "v2_follow_redirects": {"on"}, "arm_v2_deep": {"on"},
		"name": {"trial"},
	}

	rec = postForm(t, srv, "/verify/compare", start)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Header().Get("HX-Redirect"), "/verify?c=")

	rec = get(t, srv, "/verify")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "trial")
	assert.Contains(t, body, "0 of 6 jobs finished")
	assert.Contains(t, body, "Not yet: need 3 more fair runs")
}

func TestAComparisonQueuesInterleavedTaggedJobs(t *testing.T) {
	srv, repo, store := verifyServer(t)
	ctx := context.Background()

	f, err := parseComparisonForm(url.Values{
		"queries": {"a\nb\n\n"}, "runs": {"3"}, "maxtime": {"2h"}, "v1_depth": {"5"}, "v2_pages": {"3"},
	}.Get)
	require.NoError(t, err)
	require.Len(t, f.Arms, 2, "v1 and v2; v2 deep only when ticked")
	assert.Equal(t, []string{"a", "b"}, f.Queries)

	jobs, links := comparisonJobs("cid", &f, time.Now().UTC())
	require.Len(t, jobs, 6)

	order := make([]string, 0, len(links))

	for _, l := range links {
		order = append(order, l.Arm)
	}

	assert.Equal(t, []string{"v1", "v2", "v1", "v2", "v1", "v2"}, order)

	for i := range jobs {
		require.NoError(t, jobs[i].Validate())
		assert.Equal(t, "cmp:cid", jobs[i].Data.Tag)
		require.NoError(t, repo.Create(ctx, &jobs[i]))
	}

	require.NoError(t, store.CreateComparison(ctx, &leads.Comparison{ID: "cid", Name: "n", CreatedAt: time.Now(),
		Queries: f.Queries, Arms: []byte(`[{"name":"v1","collector":"v1","depth":5},{"name":"v2","collector":"v2"}]`),
		RunsPerArm: 3, Jobs: links}))

	// Stopping drops the queued jobs and marks the comparison stopped.
	rec := postForm(t, srv, "/verify/compare/cid/stop", url.Values{})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, repo.jobs)

	c, err := store.Comparison(ctx, "cid")
	require.NoError(t, err)
	assert.True(t, c.Stopped)

	rep, err := srv.comparisonReport(ctx, "cid")
	require.NoError(t, err)
	assert.True(t, rep.Ended, "dropped jobs are not waited for")

	rec = get(t, srv, "/verify/compare/cid/export?table=summary")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, strings.HasPrefix(rec.Body.String(), "arm,run,job_id,status"), rec.Body.String())

	rec = get(t, srv, "/verify/compare/cid/export?table=searches")
	assert.True(t, strings.HasPrefix(rec.Body.String(), "arm,run,job_id,search"), rec.Body.String())

	assert.Equal(t, http.StatusNotFound, get(t, srv, "/verify/compare/nope").Code)
}

func TestComparisonFormRejectsNonsense(t *testing.T) {
	for name, form := range map[string]url.Values{
		"no searches":   {"queries": {" \n "}, "runs": {"1"}, "maxtime": {"1h"}, "v1_depth": {"1"}},
		"too many runs": {"queries": {"a"}, "runs": {"9"}, "maxtime": {"1h"}, "v1_depth": {"1"}},
		"short jobs":    {"queries": {"a"}, "runs": {"1"}, "maxtime": {"1m"}, "v1_depth": {"1"}},
		"bad depth":     {"queries": {"a"}, "runs": {"1"}, "maxtime": {"1h"}, "v1_depth": {"x"}},
	} {
		_, err := parseComparisonForm(form.Get)
		assert.Error(t, err, name)
	}
}

func TestASmokeCheckIsQueuedAheadOfTheQueueAndShown(t *testing.T) {
	srv, repo, store := verifyServer(t)
	ctx := context.Background()

	earlier := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, repo.Create(ctx, &Job{ID: "queued", Status: StatusPending, Date: earlier,
		Data: JobData{Keywords: []string{"x"}}}))

	rec := postForm(t, srv, "/verify/smoke/v2", url.Values{"query": {"plumber in Austin TX"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "badge-pending", "the v2 card shows its queued smoke job")

	var smoke Job

	for _, j := range repo.jobs {
		if j.Data.Tag == TagSmoke {
			smoke = j
		}
	}

	require.NotEmpty(t, smoke.ID)
	assert.True(t, smoke.Date.Before(earlier), "it runs next")
	assert.Equal(t, []string{"plumber in Austin TX"}, smoke.Data.Keywords)
	assert.True(t, smoke.Data.IsV2())

	assert.Equal(t, http.StatusNotFound, postForm(t, srv, "/verify/smoke/v7", url.Values{}).Code)

	require.NoError(t, store.RecordSmokeCheck(ctx, &leads.SmokeCheck{Collector: "v1", JobID: "old", At: time.Now(),
		Passed: false, Checks: []leads.Check{{Name: "Job finished ok", Result: leads.CheckFail, Detail: "status failed"}}}))

	rec = get(t, srv, "/verify/smoke")
	assert.Contains(t, rec.Body.String(), "Failed")
	assert.Contains(t, rec.Body.String(), "Job finished ok")

	rec = get(t, srv, "/api/v1/verify/smoke")
	assert.Contains(t, rec.Body.String(), `"Collector":"v1"`)
}

func TestTheRunsAPIReturnsTheLedger(t *testing.T) {
	srv, _, store := verifyServer(t)
	ctx := context.Background()

	at := time.Now().UTC()
	require.NoError(t, store.RecordSearchRun(ctx, "j", &gmaps.SearchRun{Search: "q", Method: gmaps.SteadyMethod,
		StartedAt: at, FinishedAt: at.Add(time.Second), Status: gmaps.SearchOK, PlacesFound: 3}))

	rec := get(t, srv, "/api/v1/runs?job=j")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"places_found":3`)

	rec = get(t, srv, "/api/v1/runs?job=j&format=csv")
	assert.True(t, strings.HasPrefix(rec.Body.String(), "arm,run,job_id,search"))

	assert.Equal(t, http.StatusUnprocessableEntity, get(t, srv, "/api/v1/runs").Code)
}
