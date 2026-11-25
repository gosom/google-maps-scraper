//nolint:testpackage // Seed persisted jobs directly to test partial results and cursor handling.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixtureStore(t *testing.T) (store, job, string) {
	t.Helper()
	s := store{root: t.TempDir()}
	j := job{ID: "cd2aaaf6-85f3-46db-8b77-3155be4bb5b1", Status: "running",
		Plan: plan{Mode: "fast"}, RequestHash: "internal"}

	dir := filepath.Join(s.root, j.ID)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := writeState(dir, &j); err != nil {
		t.Fatal(err)
	}

	return s, j, dir
}

func TestCompactResultsAndExports(t *testing.T) {
	s, j, dir := fixtureStore(t)
	j.Status, j.Count = "completed", 4

	j.Plan.Fields = []string{"website", "extra_reviews"}
	if err := writeState(dir, &j); err != nil {
		t.Fatal(err)
	}

	largeValue := strings.Repeat("x", fullPageBytes)
	record := map[string]any{
		"title": "Coffee", "web_site": "https://example.com", "phone": "555",
		"popular_times": largeValue, "data_id": "raw",
		"user_reviews_extended": []map[string]any{{"Description": strings.Repeat("α", 300), "Rating": 5}},
	}

	line, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}

	for name, data := range map[string]string{jsonResults: strings.Repeat(string(line)+"\n", 4), csvResults: "title\nCoffee\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	in := resultsInput{ID: j.ID}

	page, err := s.results(t.Context(), &in)
	if err != nil || len(page.Rows) != 3 || page.Next == nil || *page.Next != 3 || page.Partial {
		t.Fatalf("unexpected preview: %+v, %v", page, err)
	}

	row := page.Rows[0]
	if row["web_site"] != record["web_site"] || row["phone"] != nil || row["popular_times"] != nil || row["details_in_export"] != true {
		t.Fatalf("incorrect projection: %+v", row)
	}

	reviews, ok := row["user_reviews_extended"].([]map[string]any)
	if !ok || len(reviews) != 1 {
		t.Fatalf("unexpected reviews: %v", row)
	}

	text, _ := reviews[0]["text"].(string)
	if len([]rune(text)) != 241 {
		t.Fatalf("unexpected review sample: %+v", reviews)
	}

	in.Offset, in.Fields = *page.Next, []string{"phone"}

	last, err := s.results(t.Context(), &in)
	if err != nil || last.Next != nil || len(last.Rows) != 1 || !reflect.DeepEqual(last.Rows[0], map[string]any{"title": "Coffee", "phone": "555"}) {
		t.Fatalf("last page: %+v, %v", last, err)
	}

	checkResultExports(t, s, j.ID, largeValue)
}

func checkResultExports(t *testing.T, s store, id, largeValue string) {
	t.Helper()

	in := resultsInput{ID: id, View: "export"}

	exports, err := s.results(t.Context(), &in)
	if err != nil || len(exports.Rows) != 0 || exports.JSON == "" || exports.CSV == "" {
		t.Fatalf("exports: %+v, %v", exports, err)
	}
	// A large field excluded from a preview still exists in the export.
	saved, err := os.ReadFile(exports.JSON)
	if err != nil || !strings.Contains(string(saved), largeValue) {
		t.Fatal("missing full record", err)
	}

	in.Offset, in.View = 0, "full"

	full, err := s.results(t.Context(), &in)
	if err != nil || len(full.Rows) != 1 || full.Rows[0]["details_in_export"] != true || full.Next == nil || *full.Next != 1 {
		t.Fatalf("oversize full record: %+v, %v", full, err)
	}
}

func TestWaitForNextPublishedRow(t *testing.T) {
	s, j, dir := fixtureStore(t)
	in := resultsInput{ID: j.ID, WaitFor: "rows"}

	page, err := s.results(t.Context(), &resultsInput{ID: j.ID, WaitFor: "none"})
	if err != nil || !page.Partial || page.Next == nil || *page.Next != 0 || len(page.Rows) != 0 {
		t.Fatalf("queued snapshot: %+v, %v", page, err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	ready := make(chan error, 1)

	go func() {
		page, err := s.results(ctx, &in)
		if err == nil && (len(page.Rows) != 1 || page.Next == nil || *page.Next != 1 || !page.Partial) {
			err = errors.New("expected one published row and a resume offset")
		}

		ready <- err
	}()

	if err := os.WriteFile(filepath.Join(dir, jsonResults), []byte("{\"title\":\"First\"}\n{\"ti"), 0o600); err != nil {
		t.Fatal(err)
	}

	j.Count = 1
	if err := writeState(dir, &j); err != nil {
		t.Fatal(err)
	}

	if err := <-ready; err != nil {
		t.Fatal(err)
	}

	in.Offset = 1

	stillRunning, err := s.waitForResults(ctx, &in, time.Millisecond)
	if err != nil || stillRunning.finished() {
		t.Fatalf("wait timeout: %+v, %v", stillRunning, err)
	}

	cancel()

	if _, err := s.results(ctx, &in); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}

	j.Status = "cancelled"
	if err := writeState(dir, &j); err != nil {
		t.Fatal(err)
	}

	end, err := s.results(t.Context(), &in)
	if err != nil || end.Next != nil || len(end.Rows) != 0 || !end.Partial {
		t.Fatalf("terminal partial export: %+v, %v", end, err)
	}
}

func TestStartRetryCanChangeResponseWithoutStartingAnotherWorker(t *testing.T) {
	s := store{root: t.TempDir()}
	in := startInput{Query: "dentists", Location: "Latsia", RequestID: "search"}

	request, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	j := job{ID: hash([]byte(in.RequestID)), RequestHash: hash(request), Status: "completed", Count: 1}

	dir := filepath.Join(s.root, j.ID)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := writeState(dir, &j); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, jsonResults), []byte("{\"title\":\"Practice\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, response := range []string{"job", "preview", "export", "live"} {
		t.Run(response, func(t *testing.T) {
			in.Response = response

			reply, err := s.start(t.Context(), &in)
			if err != nil || reply.Job == nil || reply.Job.ID != j.ID {
				t.Fatalf("replaying start: %+v, %v", reply, err)
			}

			if response == "job" {
				if reply.Result != nil {
					t.Fatal("background start included results")
				}

				return
			}

			if reply.Result == nil || reply.Result.JSON == "" {
				t.Fatal("missing export")
			}

			wantRows := 1
			if response == "export" {
				wantRows = 0
			}

			if got := len(reply.Result.Rows); got != wantRows {
				t.Fatalf("got %d preview rows, want %d", got, wantRows)
			}
		})
	}
}
