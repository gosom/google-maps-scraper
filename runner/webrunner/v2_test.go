//nolint:testpackage // drives the runner's v2 path and ledger through unexported hooks
package webrunner

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AxioIntel/Axio-CRED/gmaps"
	"github.com/AxioIntel/Axio-CRED/runner"
	"github.com/AxioIntel/Axio-CRED/web"
	"github.com/AxioIntel/Axio-CRED/web/leads"
)

// fakeSteady is collector v2 without a network: each search yields the places named for it.
type fakeSteady struct {
	places  map[string][]*gmaps.Entry
	block   bool // wait for the job to end instead of returning
	queries []gmaps.SteadyQuery
	closed  bool
}

func (f *fakeSteady) Run(ctx context.Context, queries []gmaps.SteadyQuery,
	emit func(gmaps.SteadyQuery, []*gmaps.Entry) error, observe func(gmaps.SearchRun)) error {
	f.queries = queries

	for _, q := range queries {
		if f.block {
			<-ctx.Done()

			return ctx.Err()
		}

		start := time.Now().UTC()
		entries := f.places[q.Text]

		for _, e := range entries {
			e.ID = q.ID
		}

		if err := emit(q, entries); err != nil {
			return err
		}

		observe(gmaps.SearchRun{Search: q.Text, Method: gmaps.SteadyMethod, StartedAt: start,
			FinishedAt: start.Add(time.Second), PlacesFound: len(entries), Status: gmaps.SearchOK})
	}

	return nil
}

func (f *fakeSteady) Close() { f.closed = true }

func place(title, placeID, email string) *gmaps.Entry {
	e := &gmaps.Entry{Title: title, PlaceID: placeID, Link: "https://maps.google.com/?q=" + placeID,
		WebSite: "https://" + placeID + ".com", EmailStatus: gmaps.EmailNone}

	if email != "" {
		e.Emails, e.EmailStatus = []string{email}, gmaps.EmailFound
	}

	return e
}

func ledgerRunner(t *testing.T, steady *fakeSteady) (*webrunner, *memoryJobRepo, *leads.Store, string) {
	t.Helper()

	dir := t.TempDir()
	repo := &memoryJobRepo{}

	store, err := leads.Open(filepath.Join(dir, "leads.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	w := &webrunner{
		svc:   web.NewService(repo, dir),
		cfg:   &runner.Config{DataFolder: dir, Concurrency: 1},
		leads: store,
		newSteady: func(gmaps.SteadyConfig, []string) (steadyRunner, error) {
			return steady, nil
		},
	}

	return w, repo, store, dir
}

func v2Job(id string, keywords ...string) web.Job {
	return web.Job{ID: id, Name: id, Date: time.Now().UTC(), Status: web.StatusPending,
		Data: web.JobData{Keywords: keywords, Lang: "en", Depth: 1, Email: true, MaxTime: time.Hour,
			Collector: gmaps.CollectorV2}}
}

// runJob is one pass of the work loop over one job: scrape, ingest, afterJob, ledger.
func runJob(t *testing.T, w *webrunner, repo *memoryJobRepo, job *web.Job) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, job))
	require.NoError(t, w.scrapeJob(ctx, job))

	w.ingest(ctx, job)
	w.afterJob(ctx, job)
	w.finishLedger(ctx, job)
}

func TestAV2JobWritesItsRowsLedgerAndLeads(t *testing.T) {
	steady := &fakeSteady{places: map[string][]*gmaps.Entry{
		"roofer in Austin TX": {place("A Roofing", "P1", "a@a.com"), place("B Roofing", "P2", "")},
	}}
	w, repo, store, dir := ledgerRunner(t, steady)
	ctx := context.Background()

	job := v2Job("v2job", "roofer in Austin TX", "roofer in Waco TX")
	runJob(t, w, repo, &job)

	assert.Equal(t, web.StatusOK, repo.jobs["v2job"].Status)
	assert.True(t, steady.closed)
	assert.Equal(t, []gmaps.SteadyQuery{{ID: "roofer in Austin TX", Text: "roofer in Austin TX"},
		{ID: "roofer in Waco TX", Text: "roofer in Waco TX"}}, steady.queries)

	body, err := os.ReadFile(filepath.Join(dir, "v2job.csv"))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	require.Len(t, lines, 3, "header and two rows")
	assert.True(t, strings.HasPrefix(lines[0], "input_id,link,title"))
	assert.True(t, strings.HasSuffix(lines[0], ",emails,email_status"))

	counts, err := store.JobLeadCounts(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, counts["v2job"], "v2's rows reach the lead list")

	runs, err := store.Runs(ctx, "v2job")
	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Equal(t, gmaps.SteadyMethod, runs[0].Method)

	jr, err := store.JobRuns(ctx, "v2job")
	require.NoError(t, err)
	assert.Equal(t, gmaps.CollectorV2, jr["v2job"].Collector)
	assert.Equal(t, web.StatusOK, jr["v2job"].Status)
	assert.Equal(t, gmaps.SteadyConfig{}.JobBound(2), jr["v2job"].Bound)

	for _, j := range repo.jobs {
		assert.False(t, strings.HasPrefix(j.Name, retryPrefix), "v2 gets no retry job for its empty search")
	}
}

func TestAV2JobThatRunsOutOfTimeIsPartial(t *testing.T) {
	w, repo, store, _ := ledgerRunner(t, &fakeSteady{block: true})

	job := v2Job("slow", "q")
	job.Data.MaxTime = 50 * time.Millisecond // shorter than v2's own bound: it wins

	runJob(t, w, repo, &job)

	assert.Equal(t, web.StatusPartial, repo.jobs["slow"].Status)

	jr, err := store.JobRuns(context.Background(), "slow")
	require.NoError(t, err)
	assert.Equal(t, 50*time.Millisecond, jr["slow"].Bound.Round(time.Millisecond))
}

type deadlineMate struct{}

func (deadlineMate) Start(ctx context.Context, _ ...scrapemate.IJob) error {
	<-ctx.Done()

	return ctx.Err()
}

func (deadlineMate) Close() error { return nil }

func TestAV1JobThatRunsOutOfItsTimeIsPartialNotOK(t *testing.T) {
	w, repo, store, _ := ledgerRunner(t, nil)
	w.setupMate = func(context.Context, io.Writer, *web.Job) (mateRunner, error) { return deadlineMate{}, nil }
	w.boundCap = 50 * time.Millisecond

	job := web.Job{ID: "v1job", Name: "v1job", Date: time.Now().UTC(), Status: web.StatusPending,
		Data: web.JobData{Keywords: []string{"q"}, Lang: "en", Depth: 1, Email: true, MaxTime: time.Hour}}

	runJob(t, w, repo, &job)

	assert.Equal(t, web.StatusPartial, repo.jobs["v1job"].Status)

	jr, err := store.JobRuns(context.Background(), "v1job")
	require.NoError(t, err)
	assert.Equal(t, gmaps.CollectorV1, jr["v1job"].Collector)
	assert.Equal(t, web.StatusPartial, jr["v1job"].Status)

	runs, err := store.Runs(context.Background(), "v1job")
	require.NoError(t, err)
	require.Len(t, runs, 1, "v1's searches are recorded from its CSV")
	assert.True(t, runs[0].JobLevel)
}

func TestAV1JobStoppedFromOutsideIsNotPartial(t *testing.T) {
	w, repo, _, _ := ledgerRunner(t, nil)
	w.setupMate = func(context.Context, io.Writer, *web.Job) (mateRunner, error) { return deadlineMate{}, nil }

	job := web.Job{ID: "v1stop", Name: "v1stop", Date: time.Now().UTC(), Status: web.StatusPending,
		Data: web.JobData{Keywords: []string{"q"}, Lang: "en", Depth: 1, Email: true, MaxTime: time.Hour}}
	require.NoError(t, repo.Create(context.Background(), &job))

	// A deadline that is not the job's own (the process shutting down) is not the job running out.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	require.NoError(t, w.scrapeJob(ctx, &job))
	assert.Equal(t, web.StatusOK, repo.jobs["v1stop"].Status)
}

func TestASmokeJobIsCheckedWhenItFinishes(t *testing.T) {
	steady := &fakeSteady{places: map[string][]*gmaps.Entry{
		"dentist in Austin TX": {place("Smile", "S1", "hi@smile.com"), place("Tooth", "S2", "")},
	}}
	w, repo, store, _ := ledgerRunner(t, steady)

	job := v2Job("smoke1", "dentist in Austin TX")
	job.Data.Tag = web.TagSmoke
	runJob(t, w, repo, &job)

	last, err := store.LastSmokeChecks(context.Background())
	require.NoError(t, err)

	check, ok := last[gmaps.CollectorV2]
	require.True(t, ok, "a smoke check was recorded")
	assert.True(t, check.Passed, "%+v", check.Checks)
	assert.Equal(t, "smoke1", check.JobID)
}

func TestASmokeJobWithBrokenRowsFails(t *testing.T) {
	broken := place("", "", "") // no title, no place id: the ingest bug the check exists to catch
	broken.Link = ""
	steady := &fakeSteady{places: map[string][]*gmaps.Entry{"q": {broken}}}
	w, repo, store, _ := ledgerRunner(t, steady)

	job := v2Job("smoke2", "q")
	job.Data.Tag = web.TagSmoke
	runJob(t, w, repo, &job)

	last, err := store.LastSmokeChecks(context.Background())
	require.NoError(t, err)
	assert.False(t, last[gmaps.CollectorV2].Passed)
}

func TestARestartAndAPauseAreCountedInTheLedger(t *testing.T) {
	w, repo, store, _ := ledgerRunner(t, &fakeSteady{})
	ctx := context.Background()

	job := v2Job("cut", "q")
	job.Status = web.StatusWorking
	require.NoError(t, repo.Create(ctx, &job))
	require.NoError(t, store.StartJobRun(ctx, "cut", gmaps.CollectorV2, 1, time.Minute, time.Now()))

	w.requeueInterrupted(ctx)

	w.paused = map[string]bool{"cut": true}
	w.afterJob(ctx, &job)

	jr, err := store.JobRuns(ctx, "cut")
	require.NoError(t, err)
	assert.Equal(t, 1, jr["cut"].Restarts)
	assert.True(t, jr["cut"].Discarded, "a run the yield gate stopped is not a fair sample")
}

func TestTheWatchdogLeavesAV2JobAlone(t *testing.T) {
	w, _, _, dir := ledgerRunner(t, nil)
	w.watchEvery, w.stallAfter = 10*time.Millisecond, 30*time.Millisecond

	writeCSV(t, dir, "v2quiet", "input_id,title"+nl)

	stopped := make(chan struct{})

	w.setCurrent("v2quiet", func() { close(stopped) })
	w.setBounded(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.watchdog(ctx)

	select {
	case <-stopped:
		t.Fatal("the watchdog stopped a v2 job, which bounds its own time")
	case <-time.After(200 * time.Millisecond):
	}
}
