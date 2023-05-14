package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/gob"
	"errors"
	"time"

	"github.com/gosom/scrapemate"

	"github.com/gosom/google-maps-scraper/gmaps"
)

const (
	statusNew    = "new"
	statusQueued = "queued"
)

var _ scrapemate.JobProvider = (*provider)(nil)

type provider struct {
	db *sql.DB
}

//nolint:gocritic // it contains about unnamed results
func (p *provider) Jobs(ctx context.Context) (<-chan scrapemate.IJob, <-chan error) {
	outc := make(chan scrapemate.IJob)
	errc := make(chan error, 1)
	q := `
	WITH updated AS (
		UPDATE gmaps_jobs
		SET status = $1
		WHERE id IN (
			SELECT id from gmaps_jobs
			WHERE status = $2
			ORDER BY priority ASC, created_at ASC FOR UPDATE SKIP LOCKED LIMIT 1
		)
		RETURNING *
	)
	SELECT payload_type, payload from updated ORDER by priority ASC, created_at ASC
	`

	go func() {
		defer close(outc)
		defer close(errc)

		const tickEvery = 100 * time.Millisecond

		ticker := time.NewTicker(tickEvery)

		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			rows, err := p.db.QueryContext(ctx, q, statusQueued, statusNew)
			if err != nil {
				errc <- err

				return
			}

			for rows.Next() {
				var (
					payloadType string
					payload     []byte
				)

				if err := rows.Scan(&payloadType, &payload); err != nil {
					errc <- err

					return
				}

				var buf bytes.Buffer

				buf.Write(payload)

				dec := gob.NewDecoder(&buf)

				var job scrapemate.IJob

				if payloadType == "search" {
					j := new(gmaps.GmapJob)

					if err := dec.Decode(j); err != nil {
						errc <- err

						return
					}

					job = j
				} else if payloadType == "place" {
					j := new(gmaps.PlaceJob)

					if err := dec.Decode(&j); err != nil {
						errc <- err

						return
					}

					job = j
				} else {
					errc <- errors.New("invalid payload type")

					return
				}

				outc <- job
			}

			if err := rows.Err(); err != nil {
				errc <- err
				return
			}

			if err := rows.Close(); err != nil {
				errc <- err
				return
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
	default:
		return errors.New("invalid job type")
	}

	_, err := p.db.ExecContext(ctx, q,
		job.GetID(), job.GetPriority(), payloadType, buf.Bytes(), time.Now().UTC(), statusNew,
	)

	return err
}

func NewProvider(db *sql.DB) scrapemate.JobProvider {
	return &provider{db: db}
}

type encjob struct {
	Type string
	Data scrapemate.IJob
}
