//nolint:testpackage // tests unexported handlers (viewJob, requestWithID, securityHeaders) directly
package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mutableJobRepo struct {
	mockJobRepo
}

func (r *mutableJobRepo) Create(_ context.Context, job *Job) error {
	r.jobs = append([]Job{*job}, r.jobs...)

	return nil
}

func (r *mutableJobRepo) Delete(_ context.Context, id string) error {
	for i := range r.jobs {
		if r.jobs[i].ID == id {
			r.jobs = append(r.jobs[:i], r.jobs[i+1:]...)

			break
		}
	}

	return nil
}

func paginatedJobs(count int) []Job {
	jobs := make([]Job, count)

	for i := range jobs {
		jobs[i] = Job{
			ID:   fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1),
			Name: fmt.Sprintf("job-%d", i+1),
		}
	}

	return jobs
}

func newTestServer(t *testing.T, dir string) *Server {
	t.Helper()

	srv, err := New(NewService(nil, dir), ":0")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return srv
}

func TestViewJobRendersPlaces(t *testing.T) {
	dir := t.TempDir()
	id := "11111111-1111-1111-1111-111111111111"

	csv := "title,latitude,longitude\nPlace,1.5,2.5\n"
	if err := os.WriteFile(filepath.Join(dir, id+".csv"), []byte(csv), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	srv := newTestServer(t, dir)

	req := requestWithID(httptest.NewRequest(http.MethodGet, "/view?id="+id, http.NoBody))
	rec := httptest.NewRecorder()
	srv.viewJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	for _, want := range []string{`id="map-modal"`, `initJobMap()`, `"title":"Place"`, `"latitude":1.5`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestViewJobEmptyState(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	id := "22222222-2222-2222-2222-222222222222"
	req := requestWithID(httptest.NewRequest(http.MethodGet, "/view?id="+id, http.NoBody))
	rec := httptest.NewRecorder()
	srv.viewJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "var places = [];") {
		t.Fatalf("expected empty places array, got:\n%s", body)
	}
}

func TestViewJobInvalidID(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	req := requestWithID(httptest.NewRequest(http.MethodGet, "/view?id=not-a-uuid", http.NoBody))
	rec := httptest.NewRecorder()
	srv.viewJob(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
}

func TestSecurityHeadersAllowMapResources(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"tile.openstreetmap.org", "cdnjs.cloudflare.com"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP missing %q: %s", want, csp)
		}
	}
}

func newTestServerWithRepo(t *testing.T, repo JobRepository) *Server {
	t.Helper()

	srv, err := New(NewService(repo, t.TempDir()), ":0")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return srv
}

func TestGetJobsHTMXTriggerIsPollingOnly(t *testing.T) {
	srv := newTestServerWithRepo(t, &mockJobRepo{jobs: paginatedJobs(1)})

	req := httptest.NewRequest(http.MethodGet, "/jobs", http.NoBody)
	rec := httptest.NewRecorder()
	srv.getJobs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()

	// The returned tbody must only poll; it must NOT contain "load" in hx-trigger
	// because load on an outerHTML-swapped element would cause an immediate reload loop.
	if strings.Contains(body, `hx-trigger="load`) {
		t.Fatal("job_rows.html must not use hx-trigger=\"load\" — it causes an HTMX outerHTML reload loop")
	}

	if !strings.Contains(body, `hx-trigger="every 10s"`) {
		t.Fatal("job_rows.html must use hx-trigger=\"every 10s\" for periodic polling")
	}

	if !strings.Contains(body, `hx-delete="/delete?id=00000000-0000-0000-0000-000000000001&page=1"`) {
		t.Fatalf("delete request must preserve the current page: %s", body)
	}

	if !strings.Contains(body, `hx-target="#job-tbody"`) {
		t.Fatal("delete request must replace the paginated job body")
	}
}

func TestIndexScrapeFormReplacesPaginatedJobs(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	srv.index(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `hx-target="#job-tbody"`) {
		t.Fatal("scrape form must target the paginated job body")
	}

	if !strings.Contains(body, `hx-swap="outerHTML"`) {
		t.Fatal("scrape form must replace the paginated job body")
	}
}

func TestScrapeRendersFirstPageAfterCreatingJob(t *testing.T) {
	repo := &mutableJobRepo{mockJobRepo: mockJobRepo{jobs: paginatedJobs(20)}}
	srv := newTestServerWithRepo(t, repo)

	form := url.Values{
		"name":      {"new-job"},
		"maxtime":   {"4m"},
		"keywords":  {"coffee"},
		"lang":      {"en"},
		"zoom":      {"15"},
		"radius":    {"10000"},
		"latitude":  {"0"},
		"longitude": {"0"},
		"depth":     {"10"},
	}
	req := httptest.NewRequest(http.MethodPost, "/scrape", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	srv.scrape(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, `<tbody id="job-tbody"`) {
		t.Fatal("scrape response must contain the complete paginated job body")
	}

	if !strings.Contains(body, "new-job") || !strings.Contains(body, "Page 1 of 2") {
		t.Fatalf("unexpected scrape response: %s", body)
	}

	if rows := strings.Count(body, "<tr>"); rows != 20 {
		t.Fatalf("got %d rows, want 20", rows)
	}
}

func TestDeleteRendersClampedCurrentPage(t *testing.T) {
	repo := &mutableJobRepo{mockJobRepo: mockJobRepo{jobs: paginatedJobs(21)}}
	srv := newTestServerWithRepo(t, repo)
	deletedJob := repo.jobs[20]

	req := httptest.NewRequest(
		http.MethodDelete,
		"/delete?id="+deletedJob.ID+"&page=2",
		http.NoBody,
	)
	req = requestWithID(req)
	rec := httptest.NewRecorder()
	srv.delete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, `<tbody id="job-tbody"`) {
		t.Fatal("delete response must contain the complete paginated job body")
	}

	if strings.Contains(body, deletedJob.Name) {
		t.Fatalf("delete response still contains %q", deletedJob.Name)
	}

	if rows := strings.Count(body, "<tr>"); rows != 20 {
		t.Fatalf("got %d rows, want 20", rows)
	}
}
