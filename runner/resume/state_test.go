package resume_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gosom/google-maps-scraper/runner/resume"
	"github.com/stretchr/testify/require"
)

func TestStateLoadMissingFile(t *testing.T) {
	t.Parallel()

	state, err := resume.LoadState(filepath.Join(t.TempDir(), "missing.resume.json"))

	require.NoError(t, err)
	require.False(t, state.IsInputCompleted("q1"))
}

func TestStateMarkInputCompletedPersists(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "results.csv.resume.json")
	state, err := resume.LoadState(path)
	require.NoError(t, err)

	require.NoError(t, state.MarkInputCompleted("q1"))

	reloaded, err := resume.LoadState(path)
	require.NoError(t, err)
	require.True(t, reloaded.IsInputCompleted("q1"))
}

func TestDefaultStatePath(t *testing.T) {
	t.Parallel()

	require.Equal(t, "results.csv.resume.json", resume.DefaultStatePath("results.csv"))
}

func TestStateDoesNotMarkInputCompletedWhenPersistenceFails(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "state")
	require.NoError(t, os.Mkdir(dir, 0o700))
	path := filepath.Join(dir, "results.csv.resume.json")
	state, err := resume.LoadState(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(dir))

	err = state.MarkInputCompleted("q1")

	require.Error(t, err)
	require.False(t, state.IsInputCompleted("q1"))
}
