//nolint:testpackage // tests unexported manager state directly
package scraper

import (
	"context"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/require"
)

func TestScraperManagerSubmitJobRequiresWriterManagedCompletion(t *testing.T) {
	m := NewScraperManager(nil, 1, false, false, 10, nil)

	err := m.SubmitJob(context.Background(), gmaps.NewGmapJob("job-1", "en", "coffee", 1, false, "", 0))
	require.Error(t, err)
	require.Contains(t, err.Error(), "WriterManagedCompletion")
}

func TestScraperManagerSubmitJobWaitsForProvider(t *testing.T) {
	m := NewScraperManager(nil, 1, false, false, 10, nil)
	job := gmaps.NewSearchJob(&gmaps.MapSearchParams{Query: "coffee", Hl: "en"},
		gmaps.WithSearchJobWriterManagedCompletion(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- m.SubmitJob(ctx, job)
	}()

	time.Sleep(20 * time.Millisecond)

	m.mu.Lock()
	m.provider = NewProvider(2)
	m.mu.Unlock()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("submit did not complete")
	}
}

func TestScraperManagerSubmitJobRequiresManagedSearchJob(t *testing.T) {
	m := NewScraperManager(nil, 1, false, false, 10, nil)

	job := gmaps.NewSearchJob(&gmaps.MapSearchParams{Query: "q", Hl: "en"})
	err := m.SubmitJob(context.Background(), job)

	require.Error(t, err)
	require.Contains(t, err.Error(), "WriterManagedCompletion")
}

func TestScraperManagerSubmitJobAllowsNonMapsJobs(t *testing.T) {
	m := NewScraperManager(nil, 1, false, false, 10, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)

	defer cancel()

	m.mu.Lock()
	m.provider = NewProvider(1)
	m.mu.Unlock()

	err := m.SubmitJob(ctx, &scrapemate.Job{ID: "x"})
	require.NoError(t, err)
}
