//nolint:testpackage // This test needs unexported hooks to avoid running a browser.
package webrunner

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/google-maps-scraper/web"
	"github.com/gosom/scrapemate"
)

func TestScrapeJobMarksOKBeforeClosingMate(t *testing.T) {
	t.Parallel()

	repo := &memoryJobRepo{}
	svc := web.NewService(repo, t.TempDir())
	job := web.Job{
		ID:     "job-1",
		Name:   "coffee",
		Date:   time.Now().UTC(),
		Status: web.StatusPending,
		Data: web.JobData{
			Keywords: []string{"coffee"},
			Lang:     "en",
			Zoom:     15,
			Lat:      "37.7749",
			Lon:      "-122.4194",
			FastMode: true,
			Radius:   1000,
			Depth:    10,
			MaxTime:  time.Minute,
		},
	}

	if err := svc.Create(context.Background(), &job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	w := &webrunner{
		svc: svc,
		cfg: &runner.Config{DataFolder: t.TempDir(), Concurrency: 1},
		setupMate: func(_ context.Context, _ io.Writer, _ *web.Job) (mateRunner, error) {
			return fakeMate{
				onClose: func() {
					got, err := svc.Get(context.Background(), job.ID)
					if err != nil {
						t.Fatalf("get job during close: %v", err)
					}

					if got.Status != web.StatusOK {
						t.Fatalf("status during close = %q, want %q", got.Status, web.StatusOK)
					}
				},
			}, nil
		},
	}

	if err := w.scrapeJob(context.Background(), &job); err != nil {
		t.Fatalf("scrape job: %v", err)
	}
}

func TestScrapeJobMarksFailedOnError(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		blockCSV  bool
		latitude  string
		startErr  error
		wantError string
	}{
		{name: "output file", blockCSV: true, latitude: "37.77", wantError: "job-error.csv"},
		{name: "seed jobs", latitude: "invalid", wantError: "invalid latitude"},
		{name: "scraper", latitude: "37.77", startErr: errors.New("scraper failed"), wantError: "scraper failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			data := t.TempDir()
			svc := web.NewService(&memoryJobRepo{}, data)
			job := web.Job{
				ID:     "job-error",
				Status: web.StatusPending,
				Data: web.JobData{
					Keywords: []string{"coffee"},
					FastMode: true,
					Lat:      tc.latitude,
					Lon:      "-122.42",
					Zoom:     15,
				},
			}
			if err := svc.Create(ctx, &job); err != nil {
				t.Fatal(err)
			}
			if tc.blockCSV {
				if err := os.Mkdir(filepath.Join(data, job.ID+".csv"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			w := &webrunner{
				svc: svc,
				cfg: &runner.Config{DataFolder: data},
				setupMate: func(context.Context, io.Writer, *web.Job) (mateRunner, error) {
					return fakeMate{startErr: tc.startErr}, nil
				},
			}
			if err := w.scrapeJob(ctx, &job); err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("scrapeJob() error = %v, want %q", err, tc.wantError)
			}
			stored, err := svc.Get(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != web.StatusFailed {
				t.Fatalf("stored status = %q, want %q", stored.Status, web.StatusFailed)
			}
		})
	}
}

type fakeMate struct {
	onClose  func()
	startErr error
}

func (m fakeMate) Start(context.Context, ...scrapemate.IJob) error {
	return m.startErr
}

func (m fakeMate) Close() error {
	if m.onClose != nil {
		m.onClose()
	}

	return nil
}

type memoryJobRepo struct {
	jobs map[string]web.Job
}

func (r *memoryJobRepo) Get(_ context.Context, id string) (web.Job, error) {
	return r.jobs[id], nil
}

func (r *memoryJobRepo) Create(_ context.Context, job *web.Job) error {
	if r.jobs == nil {
		r.jobs = make(map[string]web.Job)
	}

	r.jobs[job.ID] = *job

	return nil
}

func (r *memoryJobRepo) Delete(_ context.Context, id string) error {
	delete(r.jobs, id)
	return nil
}

func (r *memoryJobRepo) Select(_ context.Context, params web.SelectParams) ([]web.Job, error) {
	var jobs []web.Job

	for id := range r.jobs {
		job := r.jobs[id]

		if params.Status == "" || job.Status == params.Status {
			jobs = append(jobs, job)
		}
	}

	// Sort by created_at DESC (Date)
	for i := 0; i < len(jobs); i++ {
		for j := i + 1; j < len(jobs); j++ {
			if jobs[i].Date.Before(jobs[j].Date) || (jobs[i].Date.Equal(jobs[j].Date) && jobs[i].ID < jobs[j].ID) {
				jobs[i], jobs[j] = jobs[j], jobs[i]
			}
		}
	}

	if params.Offset > 0 {
		if params.Offset > len(jobs) {
			jobs = nil
		} else {
			jobs = jobs[params.Offset:]
		}
	}

	if params.Limit > 0 && len(jobs) > params.Limit {
		jobs = jobs[:params.Limit]
	}

	return jobs, nil
}

func (r *memoryJobRepo) Count(_ context.Context, params web.SelectParams) (int, error) {
	count := 0

	for id := range r.jobs {
		job := r.jobs[id]
		if params.Status == "" || job.Status == params.Status {
			count++
		}
	}

	return count, nil
}

func (r *memoryJobRepo) Update(_ context.Context, job *web.Job) error {
	r.jobs[job.ID] = *job
	return nil
}
