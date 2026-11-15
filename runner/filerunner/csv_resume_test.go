//nolint:testpackage // verify that file setup passes the existing schema to the resume writer
package filerunner

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/runner"
)

func TestResumePreservesCSVSchemaBeforeReviewMetadata(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "results.csv")
	entry := &gmaps.Entry{ID: "old-input", Title: "Old place", PlaceID: "old-place"}
	headers, row := entry.CsvHeaders(), entry.CsvRow()
	// Older files do not have the appended review_collection column.
	headers, row = headers[:len(headers)-1], row[:len(row)-1]
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, csv.NewWriter(file).WriteAll([][]string{headers, row}))
	require.NoError(t, file.Close())

	r := &fileRunner{cfg: &runner.Config{Resume: true, ResultsFile: path}}
	require.NoError(t, r.setWriters())
	t.Cleanup(func() { require.NoError(t, r.Close(context.Background())) })

	input := make(chan scrapemate.Result, 1)
	input <- scrapemate.Result{Data: &gmaps.Entry{
		ID: "new-input", Title: "New place", PlaceID: "new-place",
		ReviewCollection: &gmaps.ReviewCollection{Status: "limited", Collected: 5, Expected: 92},
	}}

	close(input)
	require.NoError(t, r.writers[0].Run(context.Background(), input))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	require.NoError(t, err, "all rows must keep the original number of columns")
	require.Len(t, rows, 3)
	require.Equal(t, headers, rows[0])
	require.Equal(t, "New place", rows[2][2])
}
