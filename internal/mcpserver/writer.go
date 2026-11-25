package mcpserver

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"

	"github.com/gosom/scrapemate"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/runner/resume"
)

type jobWriter struct {
	// Use the detached job's context for enrichment. The cell runner may cancel
	// its own context after the final search result reaches this writer.
	ctx       context.Context
	dir       string
	job       *job
	ids       *resume.IdentitySet
	json      *json.Encoder
	csv       *csv.Writer
	err       error
	cellLimit int
	overflow  []*gmaps.Entry
}

func (w *jobWriter) Run(_ context.Context, results <-chan scrapemate.Result) error {
	for result := range results {
		// Drain on an output failure so no producer is left sending to an
		// unread channel. The worker stops before starting another cell.
		if w.err == nil && w.job.Count < w.job.Plan.Limit {
			w.err = w.writeResult(result.Data)
		}
	}

	return w.err
}

func (w *jobWriter) writeResult(data any) error {
	switch value := data.(type) {
	case *gmaps.Entry:
		return w.writeEntry(value)
	case []*gmaps.Entry:
		for _, entry := range value {
			if w.job.Count >= w.job.Plan.Limit {
				break
			}

			if err := w.writeEntry(entry); err != nil {
				return err
			}
		}

		return nil
	default:
		return fmt.Errorf("expected a place entry or batch, got %T", data)
	}
}

func (w *jobWriter) writeEntry(entry *gmaps.Entry) error {
	if entry == nil || entry.Title == "" || !w.job.Plan.contains(entry) || w.ids.HasEntry(entry) {
		return nil
	}

	if resume.EntryIdentity(entry) == "" {
		fallback := fmt.Sprintf("%s|%s|%f|%f", entry.Title, entry.Address, entry.Latitude, entry.Longtitude)
		if !w.ids.AddIfNotExists(w.ctx, fallback) {
			return nil
		}
	}

	w.ids.AddEntry(entry)

	if w.cellLimit > 0 && w.job.Count >= w.cellLimit {
		if len(w.overflow) < w.job.Plan.Limit {
			w.overflow = append(w.overflow, entry)
		}

		return nil
	}

	return w.publish(entry)
}

func (w *jobWriter) writeOverflow() error {
	for _, entry := range w.overflow {
		if w.job.Count >= w.job.Plan.Limit || w.ctx.Err() != nil {
			break
		}

		if err := w.publish(entry); err != nil {
			return err
		}
	}

	return nil
}

func (w *jobWriter) publish(entry *gmaps.Entry) error {
	if w.job.Plan.Email && entry.WebSite != "" && len(entry.Emails) == 0 && w.ctx.Err() == nil {
		gmaps.EnrichWebsiteEmails(w.ctx, entry)
	}

	if err := w.csv.Write(entry.CsvRow()); err != nil {
		return err
	}

	w.csv.Flush()

	if err := w.csv.Error(); err != nil {
		return err
	}

	if err := w.json.Encode(entry); err != nil {
		return err
	}

	w.job.Count++

	if len(entry.Emails) > 0 {
		w.job.EmailCount++
	}

	return writeState(w.dir, w.job)
}
