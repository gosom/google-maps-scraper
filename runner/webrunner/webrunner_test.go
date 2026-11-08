//nolint:testpackage // This test needs unexported hooks to avoid running a browser.
package webrunner

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/deduper"
	"github.com/gosom/google-maps-scraper/gmaps"
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

func TestScrapeJobSharesPlacesOnlyFromJobsThatFinish(t *testing.T) {
	t.Parallel()

	const (
		kept = "https://www.google.com/maps/place/Kept/data=!4m7!3m6!1s0x1:0xa!8m2?hl=en"
		lost = "https://www.google.com/maps/place/Lost/data=!4m7!3m6!1s0x2:0xb!8m2?hl=en"
	)

	ctx := context.Background()
	repo := &memoryJobRepo{}
	svc := web.NewService(repo, t.TempDir())

	shared, err := deduper.NewFile(filepath.Join(t.TempDir(), "seen.txt"))
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	defer shared.Close()

	run := func(id, place string, deleteWhileRunning bool) {
		job := web.Job{
			ID:     id,
			Name:   id,
			Date:   time.Now().UTC(),
			Status: web.StatusPending,
			Data:   web.JobData{Keywords: []string{"coffee"}, Lang: "en", Zoom: 15, Depth: 1, MaxTime: time.Minute},
		}

		if err := svc.Create(ctx, &job); err != nil {
			t.Fatalf("create job: %v", err)
		}

		w := &webrunner{
			svc:         svc,
			cfg:         &runner.Config{DataFolder: t.TempDir(), Concurrency: 1},
			sharedDedup: shared,
			setupMate: func(_ context.Context, _ io.Writer, _ *web.Job) (mateRunner, error) {
				return fakeMate{onStart: func(seeds []scrapemate.IJob) {
					for _, seed := range seeds {
						if gj, ok := seed.(*gmaps.GmapJob); ok {
							gj.Deduper.AddIfNotExists(ctx, place)
						}
					}

					if deleteWhileRunning {
						_ = repo.Delete(ctx, id)
					}
				}}, nil
			},
		}

		if err := w.scrapeJob(ctx, &job); err != nil {
			t.Fatalf("scrape job %s: %v", id, err)
		}
	}

	run("finished", kept, false)
	run("deleted", lost, true)

	next := shared.NewJob()
	if next.AddIfNotExists(ctx, kept) {
		t.Fatal("place of the finished job should be shared")
	}

	if !next.AddIfNotExists(ctx, lost) {
		t.Fatal("place of the deleted job must be scraped again")
	}
}

type fakeMate struct {
	onStart func([]scrapemate.IJob)
	onClose func()
}

func (m fakeMate) Start(_ context.Context, seeds ...scrapemate.IJob) error {
	if m.onStart != nil {
		m.onStart(seeds)
	}

	return nil
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

// Update leaves deleted jobs deleted, like the sqlite repo's UPDATE ... WHERE id = ?.
func (r *memoryJobRepo) Update(_ context.Context, job *web.Job) error {
	if _, ok := r.jobs[job.ID]; ok {
		r.jobs[job.ID] = *job
	}

	return nil
}
