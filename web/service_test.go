//nolint:testpackage // shares the internal web test package with web_test.go
package web

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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

type mockJobRepo struct {
	jobs []Job
}

func (r *mockJobRepo) Get(ctx context.Context, id string) (Job, error) {
	return Job{}, nil
}

func (r *mockJobRepo) Create(ctx context.Context, job *Job) error {
	return nil
}

func (r *mockJobRepo) Delete(ctx context.Context, id string) error {
	return nil
}

func (r *mockJobRepo) Update(ctx context.Context, job *Job) error {
	return nil
}

func (r *mockJobRepo) Select(ctx context.Context, params SelectParams) ([]Job, error) {
	var res []Job
	for _, j := range r.jobs {
		if params.Status == "" || j.Status == params.Status {
			res = append(res, j)
		}
	}
	if params.Offset > 0 {
		if params.Offset > len(res) {
			res = nil
		} else {
			res = res[params.Offset:]
		}
	}
	if params.Limit > 0 && len(res) > params.Limit {
		res = res[:params.Limit]
	}
	return res, nil
}

func (r *mockJobRepo) Count(ctx context.Context, params SelectParams) (int, error) {
	count := 0
	for _, j := range r.jobs {
		if params.Status == "" || j.Status == params.Status {
			count++
		}
	}
	return count, nil
}

func TestListJobsPagination(t *testing.T) {
	repo := &mockJobRepo{}
	for i := 0; i < 45; i++ {
		repo.jobs = append(repo.jobs, Job{})
	}

	svc := NewService(repo, t.TempDir())

	// Test page 1
	page, err := svc.ListJobs(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(page.Jobs) != 20 {
		t.Errorf("expected 20 jobs, got %d", len(page.Jobs))
	}
	if page.CurrentPage != 1 || page.TotalPages != 3 || page.Total != 45 {
		t.Errorf("unexpected pagination metadata: %+v", page)
	}

	// Test page 2
	page, err = svc.ListJobs(context.Background(), 2, 20)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(page.Jobs) != 20 {
		t.Errorf("expected 20 jobs on page 2, got %d", len(page.Jobs))
	}
	if page.CurrentPage != 2 {
		t.Errorf("expected current page 2, got %d", page.CurrentPage)
	}

	// Test page 3 (last page)
	page, err = svc.ListJobs(context.Background(), 3, 20)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(page.Jobs) != 5 {
		t.Errorf("expected 5 jobs on page 3, got %d", len(page.Jobs))
	}
	if page.CurrentPage != 3 || page.HasNext != false {
		t.Errorf("unexpected metadata on page 3: %+v", page)
	}

	// Test invalid low page
	page, err = svc.ListJobs(context.Background(), -1, 20)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if page.CurrentPage != 1 || len(page.Jobs) != 20 {
		t.Errorf("expected page -1 to clamp to 1")
	}

	// Test excessively high page
	page, err = svc.ListJobs(context.Background(), 999, 20)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if page.CurrentPage != 3 || len(page.Jobs) != 5 {
		t.Errorf("expected page 999 to clamp to 3, got %d with %d jobs", page.CurrentPage, len(page.Jobs))
	}
}

func TestListJobsEmpty(t *testing.T) {
	repo := &mockJobRepo{}
	svc := NewService(repo, t.TempDir())

	page, err := svc.ListJobs(context.Background(), 1, 20)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(page.Jobs) != 0 {
		t.Errorf("expected 0 jobs, got %d", len(page.Jobs))
	}
	if page.CurrentPage != 1 || page.TotalPages != 1 || page.HasPrev || page.HasNext {
		t.Errorf("unexpected pagination metadata for empty dataset: %+v", page)
	}
}
