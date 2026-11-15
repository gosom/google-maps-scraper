//nolint:testpackage // verify writer cancellation and resume accounting without launching browsers
package filerunner

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"math"
	"os"
	"testing"

	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/adapters/writers/csvwriter"
	"github.com/gosom/scrapemate/adapters/writers/jsonwriter"
	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/grid"
	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/google-maps-scraper/runner/resume"
)

func TestGridFilterOutputFormats(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"csv", "json"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			var (
				output bytes.Buffer
				next   = jsonwriter.NewJSONWriter(&output)
			)
			if format == "csv" {
				next = csvwriter.NewCsvWriter(csv.NewWriter(&output))
			}

			writer := gridFilterWriter{next: next, bounds: grid.BoundingBox{MinLat: 1, MinLon: 2, MaxLat: 3, MaxLon: 4}}

			input := make(chan scrapemate.Result, 4)
			input <- scrapemate.Result{Data: &gmaps.Entry{Title: "inside", Latitude: 2, Longtitude: 3}}

			input <- scrapemate.Result{Data: &gmaps.Entry{Title: "edge", Latitude: 3, Longtitude: 4}}

			input <- scrapemate.Result{Data: &gmaps.Entry{Title: "outside", Latitude: 4, Longtitude: 3}}

			input <- scrapemate.Result{Data: &gmaps.Entry{Title: "nonfinite", Latitude: math.NaN(), Longtitude: 3}}

			close(input)

			require.NoError(t, writer.Run(context.Background(), input))
			require.Contains(t, output.String(), "inside")
			require.Contains(t, output.String(), "edge")
			require.NotContains(t, output.String(), "outside")
			require.NotContains(t, output.String(), "nonfinite")

			if format == "csv" {
				rows, err := csv.NewReader(&output).ReadAll()
				require.NoError(t, err)
				require.Len(t, rows, 3) // one header and two accepted places
			}
		})
	}
}

type gridWriterFunc func(context.Context, <-chan scrapemate.Result) error

func (f gridWriterFunc) Run(ctx context.Context, input <-chan scrapemate.Result) error {
	return f(ctx, input)
}

func TestGridFilterFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data any
		next scrapemate.ResultWriter
		want string
	}{
		{name: "invalid result", data: "invalid", want: "expected a place entry"},
		{name: "nil entry", data: (*gmaps.Entry)(nil), want: "expected a place entry"},
		{
			name: "output failure cancels forwarding",
			data: &gmaps.Entry{},
			next: gridWriterFunc(func(context.Context, <-chan scrapemate.Result) error { return errors.New("disk full") }),
			want: "disk full",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			next := tc.next
			if next == nil {
				next = jsonwriter.NewJSONWriter(&bytes.Buffer{})
			}

			input := make(chan scrapemate.Result, 1)
			input <- scrapemate.Result{Data: tc.data}
			// Leave input open: a downstream failure must unblock forwarding.
			writer := gridFilterWriter{next: next}
			require.ErrorContains(t, writer.Run(context.Background(), input), tc.want)
		})
	}
}

func TestGridFilterCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	writer := gridFilterWriter{next: jsonwriter.NewJSONWriter(&bytes.Buffer{})}
	require.ErrorIs(t, writer.Run(ctx, make(chan scrapemate.Result)), context.Canceled)
}

type gridInputMarker struct{ completed []string }

func (m *gridInputMarker) MarkInputCompleted(id string) error {
	m.completed = append(m.completed, id)
	return nil
}

func TestStrictGridResumeAccountsForExcludedPlaces(t *testing.T) {
	t.Parallel()

	for _, allExcluded := range []bool{false, true} {
		t.Run(map[bool]string{false: "mixed", true: "all excluded"}[allExcluded], func(t *testing.T) {
			t.Parallel()

			output, err := os.CreateTemp(t.TempDir(), "results-*.jsonl")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, output.Close()) })

			marker := &gridInputMarker{}
			tracker := resume.NewProgressTracker(marker)
			require.NoError(t, tracker.SeedDiscovered("input", 2))

			r := fileRunner{
				cfg: &runner.Config{GridBBox: "1,2,3,4"}, outfile: output, resumeProgress: tracker,
				writers: []scrapemate.ResultWriter{resume.NewJSONLAppendWriter(output, nil, tracker, output.Sync)},
			}
			require.NoError(t, r.filterGridWriters())

			first := &gmaps.Entry{ID: "input", Title: "inside", Latitude: 2, Longtitude: 3}
			if allExcluded {
				first.Latitude = 5
			}

			input := make(chan scrapemate.Result, 2)
			input <- scrapemate.Result{Data: first}

			input <- scrapemate.Result{Data: &gmaps.Entry{ID: "input", Latitude: 5, Longtitude: 3}}

			close(input)

			require.NoError(t, r.writers[0].Run(context.Background(), input))
			require.Equal(t, []string{"input"}, marker.completed)

			raw, err := os.ReadFile(output.Name())
			require.NoError(t, err)

			if allExcluded {
				require.Empty(t, raw)
			} else {
				var entry gmaps.Entry
				require.NoError(t, json.Unmarshal(raw, &entry))
				require.Equal(t, "inside", entry.Title)
			}
		})
	}
}
