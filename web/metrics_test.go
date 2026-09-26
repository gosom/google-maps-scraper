//nolint:testpackage // tests the metrics strip's unexported progress tracking
package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRepo struct {
	mu   sync.Mutex
	jobs map[string]Job
}

func (r *fakeRepo) Get(_ context.Context, id string) (Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.jobs[id], nil
}

func (r *fakeRepo) Create(_ context.Context, j *Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.jobs == nil {
		r.jobs = map[string]Job{}
	}

	r.jobs[j.ID] = *j

	return nil
}

func (r *fakeRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.jobs, id)

	return nil
}

func (r *fakeRepo) Select(_ context.Context, p SelectParams) ([]Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]Job, 0, len(r.jobs))

	for id := range r.jobs {
		j := r.jobs[id]

		switch {
		case p.Status != "" && j.Status != p.Status:
			continue
		case p.ExcludeStatus != "" && j.Status == p.ExcludeStatus:
			continue
		}

		out = append(out, j)
	}

	sort.Slice(out, func(i, k int) bool {
		if p.Oldest {
			return out[i].Date.Before(out[k].Date)
		}

		return out[i].Date.After(out[k].Date)
	})

	if p.Limit > 0 && len(out) > p.Limit {
		out = out[:p.Limit]
	}

	return out, nil
}

func (r *fakeRepo) Count(ctx context.Context, p SelectParams) (int, error) {
	out, err := r.Select(ctx, SelectParams{Status: p.Status, ExcludeStatus: p.ExcludeStatus})

	return len(out), err
}

func (r *fakeRepo) Update(ctx context.Context, j *Job) error { return r.Create(ctx, j) }

func rowsCSV(n int) string {
	var b strings.Builder

	b.WriteString("input_id,title")
	b.WriteByte('\n')

	for range n {
		b.WriteString("q,x")
		b.WriteByte('\n')
	}

	return b.String()
}

func metricsServer(t *testing.T) (*Server, *fakeRepo, string) {
	t.Helper()

	dir := t.TempDir()
	repo := &fakeRepo{}

	srv, err := New(NewService(repo, dir), ":0")
	require.NoError(t, err)

	return srv, repo, dir
}

func TestProgressRateETAAndStates(t *testing.T) {
	srv, repo, dir := metricsServer(t)
	ctx := context.Background()

	job := Job{ID: "11111111-1111-1111-1111-111111111111", Name: "Locksmiths", Status: StatusWorking,
		Date: time.Now().UTC(), Data: JobData{Keywords: []string{"a", "b"}, Depth: 5}}
	require.NoError(t, repo.Create(ctx, &job))
	require.NoError(t, repo.Create(ctx, &Job{ID: "p", Status: StatusPending}))

	path := filepath.Join(dir, job.ID+".csv")
	t0 := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

	require.NoError(t, os.WriteFile(path, []byte(rowsCSV(10)), 0o600))

	p := srv.jobProgress(&job, t0)
	assert.Equal(t, 10, p.Rows)
	assert.Equal(t, 120, p.Expected, "2 searches x ~60 at depth 5")
	assert.Equal(t, "running", p.State)

	require.NoError(t, os.WriteFile(path, []byte(rowsCSV(40)), 0o600))

	p = srv.jobProgress(&job, t0.Add(time.Minute))
	assert.InDelta(t, 30, p.RatePerMin, 0.01, "30 listings in a minute")
	assert.Equal(t, 33, p.Percent)
	assert.Equal(t, 2*time.Minute, p.ETA, "80 left at 30 a minute, to the minute")

	p = srv.jobProgress(&job, t0.Add(time.Minute+SlowAfter))
	assert.Equal(t, "slow", p.State)

	p = srv.jobProgress(&job, t0.Add(time.Minute+StallAfter))
	assert.Equal(t, "stalled", p.State)
	assert.Equal(t, StallAfter, p.LastRowAgo)
}

func TestTheMetricsStripRendersAndCountsTheQueue(t *testing.T) {
	srv, repo, _ := metricsServer(t)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &Job{ID: "p1", Status: StatusPending}))
	require.NoError(t, repo.Create(ctx, &Job{ID: "p2", Status: StatusPending}))
	srv.Note("something happened")
	srv.sampleMetrics(ctx)

	rec := httptest.NewRecorder()
	srv.metricsPartial(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))

	body := rec.Body.String()
	assert.Contains(t, body, "Queue <b>2</b>")
	assert.Contains(t, body, "No job running")
	assert.Contains(t, body, "something happened")
}

func TestRerunQueuesAFinishedJobAndRefusesARunningOne(t *testing.T) {
	srv, repo, _ := metricsServer(t)
	ctx := context.Background()

	done := Job{ID: "22222222-2222-2222-2222-222222222222", Name: "Done", Status: StatusOK}
	busy := Job{ID: "33333333-3333-3333-3333-333333333333", Name: "Busy", Status: StatusWorking}

	require.NoError(t, repo.Create(ctx, &done))
	require.NoError(t, repo.Create(ctx, &busy))

	post := func(id string) int {
		rec := httptest.NewRecorder()
		srv.rerun(rec, requestWithID(httptest.NewRequest(http.MethodPost, "/rerun?id="+id, http.NoBody)))

		return rec.Code
	}

	assert.Equal(t, http.StatusNoContent, post(done.ID))
	assert.Equal(t, StatusPending, repo.jobs[done.ID].Status)
	assert.Equal(t, http.StatusConflict, post(busy.ID))
}

func TestARunThatStartsOverIsNotANegativeRate(t *testing.T) {
	srv, repo, dir := metricsServer(t)
	ctx := context.Background()

	job := Job{ID: "44444444-4444-4444-4444-444444444444", Status: StatusWorking, Data: JobData{Keywords: []string{"a"}, Depth: 5}}
	require.NoError(t, repo.Create(ctx, &job))

	path := filepath.Join(dir, job.ID+".csv")
	t0 := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

	require.NoError(t, os.WriteFile(path, []byte(rowsCSV(300)), 0o600))
	srv.jobProgress(&job, t0)

	// Restarted: the file starts over.
	require.NoError(t, os.WriteFile(path, []byte(rowsCSV(5)), 0o600))
	srv.jobProgress(&job, t0.Add(10*time.Second))

	require.NoError(t, os.WriteFile(path, []byte(rowsCSV(65)), 0o600))

	p := srv.jobProgress(&job, t0.Add(70*time.Second))

	assert.InDelta(t, 60, p.RatePerMin, 0.01, "measured from the restart, not the old run")
}
