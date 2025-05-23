package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/gob"
	"fmt"
	"sync"
	"time"

	"github.com/gosom/scrapemate"

	"github.com/gosom/google-maps-scraper/gmaps"
)

const (
	statusNew    = "new"
	statusQueued = "queued"
	batchSize    = 10
)

var _ scrapemate.JobProvider = (*provider)(nil)

type provider struct {
	db        *sql.DB
	mu        *sync.Mutex
	jobc      chan scrapemate.IJob
	errc      chan error
	started   bool
	batchSize int
}

func NewProvider(db *sql.DB, opts ...ProviderOption) scrapemate.JobProvider {
	prov := provider{
		db:        db,
		mu:        &sync.Mutex{},
		errc:      make(chan error, 1),
		batchSize: batchSize,
	}

	for _, opt := range opts {
		opt(&prov)
	}

	prov.jobc = make(chan scrapemate.IJob, 2*prov.batchSize)

	return &prov
}

// ProviderOption allows configuring the provider
type ProviderOption func(*provider)

// WithBatchSize sets custom batch size
func WithBatchSize(size int) ProviderOption {
	return func(p *provider) {
		if size > 0 {
			p.batchSize = size
		}
	}
}

//nolint:gocritic // it contains about unnamed results
func (p *provider) Jobs(ctx context.Context) (<-chan scrapemate.IJob, <-chan error) {
	outc := make(chan scrapemate.IJob)
	errc := make(chan error, 1)

	p.mu.Lock()
	if !p.started {
		go p.fetchJobs(ctx)

		p.started = true
	}
	p.mu.Unlock()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case err := <-p.errc:
				errc <- err

				return
			case job, ok := <-p.jobc:
				if !ok {
					return
				}

				if job == nil || job.GetID() == "" {
					continue
				}

				select {
				case outc <- job:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return outc, errc
}

// Push pushes a job to the job provider
func (p *provider) Push(ctx context.Context, job scrapemate.IJob) error {
	q := `INSERT INTO gmaps_jobs
		(id, priority, payload_type, payload, created_at, status)
		VALUES
		($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	var payloadType string

	switch j := job.(type) {
	case *gmaps.GmapJob:
		payloadType = "search"

		if err := enc.Encode(j); err != nil {
			return err
		}
	case *gmaps.PlaceJob:
		payloadType = "place"

		if err := enc.Encode(j); err != nil {
			return err
		}
	case *gmaps.EmailExtractJob:
		payloadType = "email"

		if err := enc.Encode(j); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid job type %T", job)
	}

	_, err := p.db.ExecContext(ctx, q,
		job.GetID(), job.GetPriority(), payloadType, buf.Bytes(), time.Now().UTC(), statusNew,
	)

	return err
}

func (p *provider) fetchJobs(ctx context.Context) {
	defer close(p.jobc)
	defer close(p.errc)

	q := `
	WITH updated AS (
		UPDATE gmaps_jobs
		SET status = $1
		WHERE id IN (
			SELECT id from gmaps_jobs
			WHERE status = $2
			ORDER BY priority ASC, created_at ASC FOR UPDATE SKIP LOCKED 
		LIMIT $3
		)
		RETURNING *
	)
	SELECT payload_type, payload from updated ORDER by priority ASC, created_at ASC
	`

	baseDelay := time.Millisecond * 50
	maxDelay := time.Millisecond * 300
	factor := 2
	currentDelay := baseDelay

	jobs := make([]scrapemate.IJob, 0, p.batchSize)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		rows, err := p.db.QueryContext(ctx, q, statusQueued, statusNew, p.batchSize)
		if err != nil {
			p.errc <- err

			return
		}

		for rows.Next() {
			var (
				payloadType string
				payload     []byte
			)

			if err := rows.Scan(&payloadType, &payload); err != nil {
				p.errc <- err

				return
			}

			job, err := decodeJob(payloadType, payload)
			if err != nil {
				p.errc <- err

				return
			}

			jobs = append(jobs, job)
		}

		if err := rows.Err(); err != nil {
			p.errc <- err

			return
		}

		if err := rows.Close(); err != nil {
			p.errc <- err

			return
		}

		if len(jobs) > 0 {
			for _, job := range jobs {
				select {
				case p.jobc <- job:
				case <-ctx.Done():
					return
				}
			}

			jobs = jobs[:0]
		} else if len(jobs) == 0 {
			select {
			case <-time.After(currentDelay):
				currentDelay = time.Duration(float64(currentDelay) * float64(factor))
				if currentDelay > maxDelay {
					currentDelay = maxDelay
				}
			case <-ctx.Done():
				return
			}
		}
	}
}

type encjob struct {
	Type string
	Data scrapemate.IJob
}

func decodeJob(payloadType string, payload []byte) (scrapemate.IJob, error) {
	buf := bytes.NewBuffer(payload)
	dec := gob.NewDecoder(buf)

	switch payloadType {
	case "search":
		j := new(gmaps.GmapJob)
		if err := dec.Decode(j); err != nil {
			return nil, fmt.Errorf("failed to decode search job: %w", err)
		}

		return j, nil
	case "place":
		j := new(gmaps.PlaceJob)
		if err := dec.Decode(j); err != nil {
			return nil, fmt.Errorf("failed to decode place job: %w", err)
		}

		return j, nil
	case "email":
		j := new(gmaps.EmailExtractJob)
		if err := dec.Decode(j); err != nil {
			return nil, fmt.Errorf("failed to decode email job: %w", err)
		}

		return j, nil
	default:
		return nil, fmt.Errorf("invalid payload type: %s", payloadType)
	}
}
