package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AxioIntel/Axio-CRED/web"
	"github.com/AxioIntel/Axio-CRED/web/sqlite"
)

func job(id string, at time.Time) *web.Job {
	return &web.Job{ID: id, Name: id, Date: at, Status: web.StatusPending,
		Data: web.JobData{Keywords: []string{"q"}, Lang: "en", Depth: 1, MaxTime: time.Hour}}
}

func TestTheQueueRunsJobsInTheOrderTheyWereAdded(t *testing.T) {
	// The repository keeps its database open for the process's life (it has no Close), which
	// Windows will not delete under t.TempDir's strict cleanup.
	dir, err := os.MkdirTemp("", "jobs-queue-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	repo, err := sqlite.New(filepath.Join(dir, "jobs.db"))
	require.NoError(t, err)

	svc := web.NewService(repo, t.TempDir())
	ctx := context.Background()
	t0 := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

	// Two added in the same second (a batch), then a later one.
	require.NoError(t, repo.Create(ctx, job("first", t0)))
	require.NoError(t, repo.Create(ctx, job("second", t0)))
	require.NoError(t, repo.Create(ctx, job("third", t0.Add(time.Minute))))

	for _, want := range []string{"first", "second", "third"} {
		next, err := svc.SelectPending(ctx)
		require.NoError(t, err)
		require.Len(t, next, 1)
		assert.Equal(t, want, next[0].ID)

		next[0].Status = web.StatusOK
		require.NoError(t, repo.Update(ctx, &next[0]))
	}

	all, err := svc.All(ctx)
	require.NoError(t, err)
	assert.Equal(t, "third", all[0].ID, "the job list still shows the newest first")
}

func TestCountAndExcludeStatusMatchWhatSelectWouldReturn(t *testing.T) {
	dir, err := os.MkdirTemp("", "jobs-count-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	repo, err := sqlite.New(filepath.Join(dir, "jobs.db"))
	require.NoError(t, err)

	ctx := context.Background()
	t0 := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)

	require.NoError(t, repo.Create(ctx, job("p1", t0)))
	require.NoError(t, repo.Create(ctx, job("p2", t0.Add(time.Second))))

	done := job("d1", t0.Add(-time.Hour))
	done.Status = web.StatusOK
	require.NoError(t, repo.Create(ctx, done))

	n, err := repo.Count(ctx, web.SelectParams{Status: web.StatusPending})
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	n, err = repo.Count(ctx, web.SelectParams{})
	require.NoError(t, err)
	assert.Equal(t, 3, n, "no filter counts every job")

	notPending, err := repo.Select(ctx, web.SelectParams{ExcludeStatus: web.StatusPending})
	require.NoError(t, err)
	require.Len(t, notPending, 1)
	assert.Equal(t, "d1", notPending[0].ID)
}
