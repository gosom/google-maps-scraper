//nolint:testpackage // shares the internal web test package with web_test.go
package web

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeCSV(t *testing.T, dir, id, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, id+".csv"), []byte(content), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}
}

func TestGetPlacesParsesCSV(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(nil, dir)

	csv := "title,address,latitude,longitude,link,category,phone,website,review_rating\n" +
		"Coffee Place,1 Main St,37.7749,-122.4194,http://maps/1,cafe,555,http://web,4.5\n"
	writeCSV(t, dir, "job-1", csv)

	places, err := svc.GetPlaces(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("GetPlaces: %v", err)
	}

	if len(places) != 1 {
		t.Fatalf("expected 1 place, got %d", len(places))
	}

	p := places[0]
	if p.Title != "Coffee Place" || p.Latitude != 37.7749 || p.Longitude != -122.4194 {
		t.Fatalf("unexpected place: %+v", p)
	}

	if p.ReviewRating != 4.5 {
		t.Fatalf("unexpected rating: %v", p.ReviewRating)
	}
}

func TestGetPlacesSkipsRowsWithoutCoords(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(nil, dir)

	csv := "title,latitude,longitude\n" +
		"No Coords,,\n" +
		"Zero,0,0\n" +
		"Bad,abc,def\n" +
		"Good,1.5,2.5\n"
	writeCSV(t, dir, "job-2", csv)

	places, err := svc.GetPlaces(context.Background(), "job-2")
	if err != nil {
		t.Fatalf("GetPlaces: %v", err)
	}

	if len(places) != 1 {
		t.Fatalf("expected 1 place, got %d", len(places))
	}

	if places[0].Title != "Good" {
		t.Fatalf("unexpected place: %+v", places[0])
	}
}

func TestGetPlacesSkipsNonFiniteAndOutOfRangeCoords(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(nil, dir)

	csv := "title,latitude,longitude,review_rating\n" +
		"NaN,NaN,2.5,3\n" +
		"Inf,Inf,2.5,3\n" +
		"OutOfRange,91,200,3\n" +
		"BadRating,1.5,2.5,NaN\n" +
		"Good,1.5,2.5,4.5\n"
	writeCSV(t, dir, "job-nf", csv)

	places, err := svc.GetPlaces(context.Background(), "job-nf")
	if err != nil {
		t.Fatalf("GetPlaces: %v", err)
	}

	if len(places) != 2 {
		t.Fatalf("expected 2 places (BadRating + Good), got %d: %+v", len(places), places)
	}

	for _, p := range places {
		if p.Title == "BadRating" && p.ReviewRating != 0 {
			t.Fatalf("non-finite rating should be sanitized to 0, got %v", p.ReviewRating)
		}
	}
}

func TestGetPlacesMissingCSV(t *testing.T) {
	svc := NewService(nil, t.TempDir())

	if _, err := svc.GetPlaces(context.Background(), "missing"); err == nil {
		t.Fatal("expected error for missing csv")
	}
}

func TestGetPlacesRejectsTraversal(t *testing.T) {
	svc := NewService(nil, t.TempDir())

	if _, err := svc.GetPlaces(context.Background(), "../etc/passwd"); err == nil {
		t.Fatal("expected error for path traversal")
	}
}

// A campaign can queue tens of thousands of pending jobs; PendingCount, PendingPreview,
// RecentHistory and Working must never decode more of them than they say they will.
func TestPendingCountPreviewHistoryAndWorkingDoNotOverread(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, t.TempDir())
	ctx := context.Background()

	t0 := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)

	for i := range 5 {
		require.NoError(t, repo.Create(ctx, &Job{
			ID: "pending-" + string(rune('a'+i)), Status: StatusPending,
			Date: t0.Add(time.Duration(i) * time.Minute),
		}))
	}

	require.NoError(t, repo.Create(ctx, &Job{ID: "running", Status: StatusWorking, Date: t0}))
	require.NoError(t, repo.Create(ctx, &Job{ID: "done-old", Status: StatusOK, Date: t0.Add(-time.Hour)}))
	require.NoError(t, repo.Create(ctx, &Job{ID: "done-new", Status: StatusFailed, Date: t0.Add(time.Hour)}))

	n, err := svc.PendingCount(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, n, "every pending job counted, not just the preview")

	preview, err := svc.PendingPreview(ctx, 2)
	require.NoError(t, err)
	require.Len(t, preview, 2)
	assert.Equal(t, "pending-a", preview[0].ID, "oldest-first: what runs next")
	assert.Equal(t, "pending-b", preview[1].ID)

	history, err := svc.RecentHistory(ctx, 200)
	require.NoError(t, err)
	require.Len(t, history, 3, "pending jobs are excluded from history; the running one is not")
	assert.Equal(t, "done-new", history[0].ID, "most recent first")
	assert.Equal(t, "running", history[1].ID)
	assert.Equal(t, "done-old", history[2].ID)

	working, err := svc.Working(ctx)
	require.NoError(t, err)
	require.NotNil(t, working)
	assert.Equal(t, "running", working.ID)
}

func TestWorkingIsNilWhenNoJobIsRunning(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, t.TempDir())
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &Job{ID: "p", Status: StatusPending}))

	working, err := svc.Working(ctx)
	require.NoError(t, err)
	assert.Nil(t, working)
}

func TestJobListViewShowsPendingThenHistoryAndCountsWhatItLeftOut(t *testing.T) {
	srv, repo, _ := metricsServer(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)

	for i := range pendingPreviewSize + 3 {
		require.NoError(t, repo.Create(ctx, &Job{
			ID: "pending-" + string(rune('a'+i%26)) + string(rune('0'+i/26)), Status: StatusPending,
			Date: t0.Add(time.Duration(i) * time.Second),
			Data: JobData{Keywords: []string{"q"}},
		}))
	}

	require.NoError(t, repo.Create(ctx, &Job{ID: "done", Status: StatusOK, Date: t0.Add(-time.Hour),
		Data: JobData{Keywords: []string{"q"}}}))

	view, err := srv.jobListView(ctx)
	require.NoError(t, err)

	assert.Len(t, view.Rows, pendingPreviewSize+1, "the pending preview plus history, not every pending job")
	assert.Equal(t, 3, view.MoreQueued, "the rest of the pending queue is counted, not rendered")
}
