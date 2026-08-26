package gmaps_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/require"
)

type recordingCompletionTracker struct {
	seedInputID     string
	seedPlacesFound int
}

func (t *recordingCompletionTracker) SeedDiscovered(inputID string, placesFound int) error {
	t.seedInputID = inputID
	t.seedPlacesFound = placesFound

	return nil
}

func TestGmapJobProcessNotifiesSeedDiscovery(t *testing.T) {
	t.Parallel()

	tracker := &recordingCompletionTracker{}
	job := gmaps.NewGmapJob("input-1", "en", "coffee", 1, false, "", 0, gmaps.WithGmapCompletionTracker(tracker))
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
		<html><body>
			<div role="feed">
				<div jsaction="x"><a href="https://www.google.com/maps/place/a"></a></div>
				<div jsaction="x"><a href="https://www.google.com/maps/place/b"></a></div>
			</div>
		</body></html>
	`))
	require.NoError(t, err)

	_, next, err := job.Process(context.Background(), &scrapemate.Response{Document: doc})

	require.NoError(t, err)
	require.Len(t, next, 2)
	require.Equal(t, "input-1", tracker.seedInputID)
	require.Equal(t, 2, tracker.seedPlacesFound)
}

func TestPlaceJobProcessDoesNotCompleteInputOnTerminalError(t *testing.T) {
	t.Parallel()

	job := gmaps.NewPlaceJob("input-1", "en", "https://www.google.com/maps/place/a", false, false)

	_, next, err := job.Process(context.Background(), &scrapemate.Response{Error: errors.New("fetch failed")})

	require.Error(t, err)
	require.Empty(t, next)
}

func TestPlaceJobProcessDoesNotCompleteInputBeforeResultIsWritten(t *testing.T) {
	t.Parallel()

	job := gmaps.NewPlaceJob("input-1", "en", "https://www.google.com/maps/place/a", false, false)
	raw, err := os.ReadFile("../testdata/raw.json")
	require.NoError(t, err)

	result, next, err := job.Process(context.Background(), &scrapemate.Response{
		Meta: map[string]any{
			"json": raw,
		},
	})

	require.NoError(t, err)
	require.Empty(t, next)
	require.NotNil(t, result)
}

func TestEmailExtractJobProcessDoesNotCompleteInputBeforeResultIsWritten(t *testing.T) {
	t.Parallel()

	entry := &gmaps.Entry{ID: "input-1", WebSite: "https://example.com"}
	job := gmaps.NewEmailJob("place-1", entry)

	result, next, err := job.Process(context.Background(), &scrapemate.Response{Error: errors.New("fetch failed")})

	require.NoError(t, err)
	require.Empty(t, next)
	require.Equal(t, entry, result)
}
