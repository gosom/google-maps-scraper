//nolint:testpackage // tests unexported writer internals directly
package scraper

import (
	"context"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noopSave records the entries it receives without touching a DB.
func noopSave(dst *[]*gmaps.Entry) SaveFunc {
	return func(_ context.Context, _ int64, _ string, entries []*gmaps.Entry) error {
		if dst != nil {
			*dst = entries
		}

		return nil
	}
}

func TestCentralWriter_AddResultThenFlush(t *testing.T) {
	var saved []*gmaps.Entry
	cw := NewCentralWriter(nil, noopSave(&saved))

	ch := cw.RegisterJob("job1", 100, "restaurants")
	cw.AddResult("job1", &gmaps.Entry{Title: "Place A"})
	cw.AddResult("job1", &gmaps.Entry{Title: "Place B"})
	cw.Flush("job1")

	select {
	case result := <-ch:
		assert.NoError(t, result.Err)
		assert.Equal(t, 2, result.ResultCount)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for flush")
	}

	require.Len(t, saved, 2)
	assert.Equal(t, "Place A", saved[0].Title)
	assert.Equal(t, "Place B", saved[1].Title)
}

func TestCentralWriter_MarkDoneFlushes(t *testing.T) {
	cw := NewCentralWriter(nil, noopSave(nil))
	ch := cw.RegisterJob("job1", 100, "restaurants")
	cw.AddResult("job1", &gmaps.Entry{Title: "Place A"})

	cw.MarkDone("job1")

	select {
	case result := <-ch:
		assert.NoError(t, result.Err)
		assert.Equal(t, 1, result.ResultCount)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for flush")
	}
}

func TestCentralWriter_MarkDoneFlushesWithSave(t *testing.T) {
	var saved []*gmaps.Entry
	cw := NewCentralWriter(nil, noopSave(&saved))

	ch := cw.RegisterJob("job1", 100, "restaurants")
	cw.AddResult("job1", &gmaps.Entry{Title: "Place A"})
	cw.MarkDone("job1")

	select {
	case result := <-ch:
		assert.NoError(t, result.Err)
		assert.Equal(t, 1, result.ResultCount)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for flush")
	}

	require.Len(t, saved, 1)
	assert.Equal(t, "Place A", saved[0].Title)
}

func TestCentralWriter_ForceFlush(t *testing.T) {
	var saved []*gmaps.Entry
	cw := NewCentralWriter(nil, noopSave(&saved))

	ch := cw.RegisterJob("job1", 100, "restaurants")
	cw.AddResult("job1", &gmaps.Entry{Title: "Place A"})
	cw.ForceFlush("job1")

	select {
	case result := <-ch:
		assert.NoError(t, result.Err)
		assert.Equal(t, 1, result.ResultCount)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for flush")
	}

	require.Len(t, saved, 1)
}

func TestCentralWriter_DoubleFlushIdempotent(t *testing.T) {
	saveCount := 0
	cw := NewCentralWriter(nil, func(_ context.Context, _ int64, _ string, _ []*gmaps.Entry) error {
		saveCount++
		return nil
	})

	ch := cw.RegisterJob("job1", 100, "restaurants")
	cw.AddResult("job1", &gmaps.Entry{Title: "Place A"})
	cw.ForceFlush("job1")
	<-ch

	// Second call is a no-op (job already removed from map)
	cw.ForceFlush("job1")

	assert.Equal(t, 1, saveCount)
}

func TestCentralWriter_AddResultAfterFlush(_ *testing.T) {
	cw := NewCentralWriter(nil, noopSave(nil))

	ch := cw.RegisterJob("job1", 100, "restaurants")
	cw.ForceFlush("job1")
	<-ch

	// Should not panic — job is gone from map, silently dropped
	cw.AddResult("job1", &gmaps.Entry{Title: "Too Late"})
}

func TestCentralWriter_OnResultsSavedCallback(t *testing.T) {
	cw := NewCentralWriter(nil, noopSave(nil))

	var callbackCount int

	cw.OnResultsSaved = func(count int) { callbackCount = count }

	ch := cw.RegisterJob("job1", 100, "restaurants")
	cw.AddResult("job1", &gmaps.Entry{Title: "A"})
	cw.AddResult("job1", &gmaps.Entry{Title: "B"})
	cw.AddResult("job1", &gmaps.Entry{Title: "C"})
	cw.ForceFlush("job1")
	<-ch

	assert.Equal(t, 3, callbackCount)
}

func TestCentralWriter_EmptyFlushNoCallback(t *testing.T) {
	cw := NewCentralWriter(nil, noopSave(nil))

	called := false
	cw.OnResultsSaved = func(_ int) { called = true }

	ch := cw.RegisterJob("job1", 100, "restaurants")
	cw.ForceFlush("job1")
	<-ch

	assert.False(t, called)
}

func TestCentralWriter_FlushUnregisteredJobIgnored(_ *testing.T) {
	cw := NewCentralWriter(nil, noopSave(nil))

	// Should not panic
	cw.AddResult("nonexistent", &gmaps.Entry{Title: "Ghost"})
	cw.ForceFlush("nonexistent")
}

func TestCentralWriter_ZeroResultFlush(t *testing.T) {
	var saved []*gmaps.Entry
	cw := NewCentralWriter(nil, noopSave(&saved))

	ch := cw.RegisterJob("job1", 100, "restaurants")
	// No results added — simulates GmapJob error / no places found
	cw.Flush("job1")

	select {
	case result := <-ch:
		assert.NoError(t, result.Err)
		assert.Equal(t, 0, result.ResultCount)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for flush")
	}

	require.Len(t, saved, 0)
}

func TestCentralWriter_DiscardDropsTrackedJobWithoutSave(t *testing.T) {
	saveCount := 0
	cw := NewCentralWriter(nil, func(_ context.Context, _ int64, _ string, _ []*gmaps.Entry) error {
		saveCount++
		return nil
	})

	cw.RegisterJob("job1", 100, "restaurants")
	cw.AddResult("job1", &gmaps.Entry{Title: "Place A"})
	cw.Discard("job1")

	// No-op after discard and no persistence should happen.
	cw.ForceFlush("job1")

	assert.Equal(t, 0, saveCount)
	assert.Equal(t, 0, cw.TrackedJobs())
}
