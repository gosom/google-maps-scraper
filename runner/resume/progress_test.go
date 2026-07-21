package resume_test

import (
	"testing"

	"github.com/gosom/google-maps-scraper/runner/resume"
	"github.com/stretchr/testify/require"
)

type recordingMarker struct {
	completed map[string]int
	events    []string
}

func newRecordingMarker() *recordingMarker {
	return &recordingMarker{completed: make(map[string]int)}
}

func (m *recordingMarker) MarkInputCompleted(inputID string) error {
	m.events = append(m.events, "marked")
	m.completed[inputID]++

	return nil
}

func TestProgressMarksZeroResultInputComplete(t *testing.T) {
	t.Parallel()

	marker := newRecordingMarker()
	tracker := resume.NewProgressTracker(marker)

	require.NoError(t, tracker.SeedDiscovered("q1", 0))

	require.Equal(t, 1, marker.completed["q1"])
}

func TestProgressWaitsForPlaces(t *testing.T) {
	t.Parallel()

	marker := newRecordingMarker()
	tracker := resume.NewProgressTracker(marker)

	require.NoError(t, tracker.SeedDiscovered("q1", 2))
	require.Zero(t, marker.completed["q1"])
	require.NoError(t, tracker.ResultPersisted("q1", nil))
	require.Zero(t, marker.completed["q1"])
	require.NoError(t, tracker.ResultPersisted("q1", nil))
	require.Equal(t, 1, marker.completed["q1"])
}

func TestProgressMarksInputOnlyOnce(t *testing.T) {
	t.Parallel()

	marker := newRecordingMarker()
	tracker := resume.NewProgressTracker(marker)

	require.NoError(t, tracker.SeedDiscovered("q1", 1))
	require.NoError(t, tracker.ResultPersisted("q1", nil))
	require.NoError(t, tracker.ResultPersisted("q1", nil))

	require.Equal(t, 1, marker.completed["q1"])
}

func TestProgressSyncsOutputBeforeMarkingInputComplete(t *testing.T) {
	t.Parallel()

	marker := newRecordingMarker()
	tracker := resume.NewProgressTracker(marker)
	require.NoError(t, tracker.SeedDiscovered("q1", 1))

	err := tracker.ResultPersisted("q1", func() error {
		marker.events = append(marker.events, "synced")
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, []string{"synced", "marked"}, marker.events)
}
