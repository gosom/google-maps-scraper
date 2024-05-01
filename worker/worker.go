package worker

import (
	"context"
	"time"

	"github.com/gosom/google-maps-scraper/entities"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/scrapemateapp"

	"github.com/gosom/google-maps-scraper/gmaps"
)

type worker struct {
	jobs         chan entities.Job
	resultWriter scrapemate.ResultWriter
	store        entities.JobStore
}

func NewWorker(resultWriter scrapemate.ResultWriter) entities.Worker {
	ans := worker{
		jobs:         make(chan entities.Job),
		resultWriter: resultWriter,
	}

	if store, ok := resultWriter.(entities.JobStore); ok {
		ans.store = store
	}

	return &ans
}

func (w *worker) Start(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case job := <-w.jobs:
			if err := w.ProcessJob(ctx, job); err != nil {
				return err
			}
		}
	}
}

func (w *worker) ScheduleJob(ctx context.Context, job entities.Job) error {
	select {
	case <-ctx.Done():
		return nil
	case w.jobs <- job:
	default:
		return entities.ErrOtherJobRunning
	}

	return nil
}

func (w *worker) ProcessJob(ctx context.Context, job entities.Job) (err error) {
	if err = w.updateJobStatus(ctx, job, entities.JobStatusRunning); err != nil {
		return err
	}

	defer func() {
		if err != nil {
			if err2 := w.updateJobStatus(ctx, job, entities.JobStatusFailed); err2 != nil {
				panic(err2)
			}
		} else {
			if err2 := w.updateJobStatus(ctx, job, entities.JobStatusDone); err2 != nil {
				panic(err2)
			}
		}
	}()

	writers := []scrapemate.ResultWriter{}
	writers = append(writers, w.resultWriter)

	opts := []func(*scrapemateapp.Config) error{
		// scrapemateapp.WithCache("leveldb", "cache"),
		scrapemateapp.WithConcurrency(job.Concurrency),
		scrapemateapp.WithExitOnInactivity(time.Minute * 1),
	}

	if job.Debug {
		opts = append(opts, scrapemateapp.WithJS(
			scrapemateapp.Headfull(),
			scrapemateapp.DisableImages(),
		),
		)
	} else {
		opts = append(opts, scrapemateapp.WithJS(scrapemateapp.DisableImages()))
	}

	cfg, err := scrapemateapp.NewConfig(
		writers,
		opts...,
	)
	if err != nil {
		return err
	}

	app, err := scrapemateapp.NewScrapeMateApp(cfg)
	if err != nil {
		return err
	}

	seedJobs, err := w.createSeedJobs(job)
	if err != nil {
		return err
	}

	err = app.Start(ctx, seedJobs...)

	return err
}

func (w *worker) updateJobStatus(ctx context.Context, job entities.Job, status string) error {
	if w.store == nil {
		return nil
	}

	return w.store.SetJobStatus(ctx, job.ID, status)
}

func (w *worker) createSeedJobs(job entities.Job) (jobs []scrapemate.IJob, err error) {
	for i := range job.Queries {
		jobs = append(jobs, gmaps.NewGmapJob(job.ID, "en", job.Queries[i], 10, false))
	}

	return jobs, nil
}
