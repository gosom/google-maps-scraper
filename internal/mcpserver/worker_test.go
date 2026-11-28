//nolint:testpackage // Exercise the worker with a deterministic cell scraper.
package mcpserver

import (
	"context"
	"testing"

	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/grid"
)

func TestRunScrapeStopsAtExportLimit(t *testing.T) {
	cells := []grid.Cell{{Lat: 1}, {Lat: 2}, {Lat: 3}}
	j := job{Plan: plan{Limit: 1, Cells: cells, RadiusKM: 3}}

	runCell := func(ctx context.Context, _ string, _ *plan, cell grid.Cell, writer scrapemate.ResultWriter) error {
		require.Equal(t, cells[0], cell, "only the first cell should run")

		results := make(chan scrapemate.Result, 1)
		results <- scrapemate.Result{Data: &gmaps.Entry{Title: "First", PlaceID: "first"}}

		close(results)

		return writer.Run(ctx, results)
	}

	require.NoError(t, runScrape(t.Context(), t.TempDir(), &j, runCell))
	require.Equal(t, 1, j.Count)
	require.Equal(t, 1, j.CellsDone)
}
