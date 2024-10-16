package memory

import (
	"context"
	"maps"
	"sort"
	"sync"

	"github.com/gosom/google-maps-scraper/web"
)

type repo struct {
	mu    *sync.RWMutex
	items map[string]web.Job
}

func New() (web.JobRepository, error) {
	ans := repo{
		mu:    &sync.RWMutex{},
		items: make(map[string]web.Job),
	}

	return &ans, nil
}

func (r *repo) Get(ctx context.Context, id string) (web.Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	job, ok := r.items[id]
	if !ok {
		return web.Job{}, web.ErrNotFound
	}

	return job, nil
}

func (r *repo) Create(ctx context.Context, job *web.Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.items[job.ID]; ok {
		return web.ErrAlreadyExists
	}

	r.items[job.ID] = *job

	return nil
}

func (r *repo) Delete(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.items[id]; !ok {
		return web.ErrNotFound
	}

	delete(r.items, id)

	return nil
}

func (r *repo) Select(ctx context.Context, params web.SelectParams) ([]web.Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	items := maps.Values(r.items)
	filtered := make([]web.Job, 0, len(r.items))

	for item := range items {
		if params.Status != "" && item.Status == params.Status {
			if item.Status == params.Status {
				filtered = append(filtered, item)
			}
		} else if params.Status == "" {
			filtered = append(filtered, item)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Date.Before(filtered[j].Date)
	})

	return filtered, nil
}

func (r *repo) Update(ctx context.Context, job *web.Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.items[job.ID]; !ok {
		return web.ErrNotFound
	}

	r.items[job.ID] = *job

	return nil
}
