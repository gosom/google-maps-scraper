package resume

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
)

var (
	_ scrapemate.ResultWriter = (*csvAppendWriter)(nil)
	_ scrapemate.ResultWriter = (*jsonlAppendWriter)(nil)
)

type csvAppendWriter struct {
	w           *csv.Writer
	writeHeader bool
	headerDone  bool
	ids         *IdentitySet
	tracker     ResultTracker
	syncOutput  func() error
}

// ResultTracker acknowledges results after they have been persisted.
type ResultTracker interface {
	ResultPersisted(inputID string, syncOutput func() error) error
}

// NewCSVAppendWriter creates a CSV writer suitable for resume append mode.
func NewCSVAppendWriter(
	w *csv.Writer,
	writeHeader bool,
	ids *IdentitySet,
	tracker ResultTracker,
	syncOutput func() error,
) scrapemate.ResultWriter {
	return &csvAppendWriter{
		w:           w,
		writeHeader: writeHeader,
		ids:         ids,
		tracker:     tracker,
		syncOutput:  syncOutput,
	}
}

func (w *csvAppendWriter) Run(_ context.Context, in <-chan scrapemate.Result) error {
	for result := range in {
		entries, err := entriesFromResult(result.Data)
		if err != nil {
			return err
		}

		if len(entries) == 0 {
			continue
		}

		if err := w.writeHeaderIfNeeded(entries[0]); err != nil {
			return err
		}

		for _, entry := range entries {
			if w.ids != nil && w.ids.HasEntry(entry) {
				continue
			}

			if err := w.w.Write(entry.CsvRow()); err != nil {
				return err
			}

			if w.ids != nil {
				w.ids.AddEntry(entry)
			}
		}

		w.w.Flush()

		if err := w.w.Error(); err != nil {
			return err
		}

		for _, entry := range entries {
			if err := acknowledgeResult(w.tracker, entry, w.syncOutput); err != nil {
				return err
			}
		}
	}

	return w.w.Error()
}

func (w *csvAppendWriter) writeHeaderIfNeeded(entry *gmaps.Entry) error {
	if !w.writeHeader || w.headerDone {
		return nil
	}

	if err := w.w.Write(entry.CsvHeaders()); err != nil {
		return err
	}

	w.w.Flush()

	if err := w.w.Error(); err != nil {
		return err
	}

	w.headerDone = true

	return nil
}

type jsonlAppendWriter struct {
	enc        *json.Encoder
	ids        *IdentitySet
	tracker    ResultTracker
	syncOutput func() error
}

// NewJSONLAppendWriter creates a JSONL writer suitable for resume append mode.
func NewJSONLAppendWriter(
	w io.Writer,
	ids *IdentitySet,
	tracker ResultTracker,
	syncOutput func() error,
) scrapemate.ResultWriter {
	return &jsonlAppendWriter{
		enc:        json.NewEncoder(w),
		ids:        ids,
		tracker:    tracker,
		syncOutput: syncOutput,
	}
}

func (w *jsonlAppendWriter) Run(_ context.Context, in <-chan scrapemate.Result) error {
	for result := range in {
		entries, err := entriesFromResult(result.Data)
		if err != nil {
			return err
		}

		for _, entry := range entries {
			if w.ids == nil || !w.ids.HasEntry(entry) {
				if err := w.enc.Encode(entry); err != nil {
					return err
				}

				if w.ids != nil {
					w.ids.AddEntry(entry)
				}
			}

			if err := acknowledgeResult(w.tracker, entry, w.syncOutput); err != nil {
				return err
			}
		}
	}

	return nil
}

func acknowledgeResult(tracker ResultTracker, entry *gmaps.Entry, syncOutput func() error) error {
	if tracker == nil || entry == nil {
		return nil
	}

	if err := tracker.ResultPersisted(entry.ID, syncOutput); err != nil {
		return fmt.Errorf("acknowledge persisted result: %w", err)
	}

	return nil
}

func entriesFromResult(data any) ([]*gmaps.Entry, error) {
	if data == nil {
		return nil, nil
	}

	if entry, ok := data.(*gmaps.Entry); ok {
		return []*gmaps.Entry{entry}, nil
	}

	value := reflect.ValueOf(data)
	if value.Kind() != reflect.Slice {
		return nil, fmt.Errorf("unexpected resume writer data type: %T", data)
	}

	length := value.Len()
	entries := make([]*gmaps.Entry, 0, length)

	for i := 0; i < length; i++ {
		item := value.Index(i).Interface()
		entry, ok := item.(*gmaps.Entry)

		if !ok {
			return nil, fmt.Errorf("unexpected resume writer slice item type: %T", item)
		}

		entries = append(entries, entry)
	}

	return entries, nil
}
