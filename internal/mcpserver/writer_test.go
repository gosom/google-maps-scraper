//nolint:testpackage // Exercise the internal streaming writer before a job publishes its final state.
package mcpserver

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gosom/scrapemate"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/runner/resume"
)

func TestJobWriterPublishesRowsBeforeSearchEnds(t *testing.T) {
	dir := t.TempDir()
	j := job{Plan: plan{Limit: 2, RadiusKM: 3}, Status: "running"}

	var jsonOutput, csvOutput bytes.Buffer

	writer := &jobWriter{
		ctx: t.Context(), dir: dir, job: &j, ids: resume.NewIdentitySet(),
		json: json.NewEncoder(&jsonOutput), csv: csv.NewWriter(&csvOutput),
	}
	results := make(chan scrapemate.Result)

	done := make(chan error, 1)
	go func() { done <- writer.Run(t.Context(), results) }()

	first := &gmaps.Entry{Title: "First", PlaceID: "first"}
	results <- scrapemate.Result{Data: []*gmaps.Entry{first, first}}
	// This send synchronizes with the writer after it has flushed the batch.
	// A nil place is discarded without mutating the output.
	results <- scrapemate.Result{Data: (*gmaps.Entry)(nil)}

	publishedEarly := bytes.Count(jsonOutput.Bytes(), []byte("\n")) == 1 && csvOutput.Len() > 0
	data, stateErr := os.ReadFile(filepath.Join(dir, stateFile))

	var state job

	decodeErr := json.Unmarshal(data, &state)

	results <- scrapemate.Result{Data: &gmaps.Entry{Title: "Second", PlaceID: "second"}}

	close(results)

	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if !publishedEarly || stateErr != nil || decodeErr != nil || state.Count != 1 || state.Status != "running" {
		t.Fatalf("expected one visible row while running, got %+v (error %v)", state, stateErr)
	}

	if j.Count != 2 {
		t.Fatalf("expected two distinct rows, got %d", j.Count)
	}
}

func TestJobWriterReservesResultSlotsForLaterCells(t *testing.T) {
	j := job{Plan: plan{Limit: 4, RadiusKM: 3}, Status: "running"}

	var output bytes.Buffer

	writer := &jobWriter{
		ctx: t.Context(), dir: t.TempDir(), job: &j, ids: resume.NewIdentitySet(),
		json: json.NewEncoder(&output), csv: csv.NewWriter(&bytes.Buffer{}), cellLimit: 2,
	}

	results := []*gmaps.Entry{
		{Title: "One", PlaceID: "one"}, {Title: "Two", PlaceID: "two"},
		{Title: "Three", PlaceID: "three"}, {Title: "Four", PlaceID: "four"},
	}
	if err := writer.writeResult(results); err != nil {
		t.Fatal(err)
	}

	if j.Count != 2 {
		t.Fatalf("first cell used %d slots, want 2", j.Count)
	}

	writer.cellLimit = 4
	if err := writer.writeResult([]*gmaps.Entry{results[2], {Title: "Later", PlaceID: "later"}}); err != nil {
		t.Fatal(err)
	}

	if err := writer.writeOverflow(); err != nil {
		t.Fatal(err)
	}

	decoder := json.NewDecoder(&output)

	for _, want := range []string{"One", "Two", "Later", "Three"} {
		var row gmaps.Entry
		if err := decoder.Decode(&row); err != nil || row.Title != want {
			t.Fatalf("row = %q, want %q (error %v)", row.Title, want, err)
		}
	}

	if j.Count != 4 {
		t.Fatalf("final count = %d, want 4", j.Count)
	}
}
