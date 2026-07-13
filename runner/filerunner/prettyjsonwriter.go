package filerunner

import (
	"context"
	"encoding/json"
	"io"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
)

type prettyJSONWriter struct {
	w io.Writer
}

func newPrettyJSONWriter(w io.Writer) scrapemate.ResultWriter {
	return &prettyJSONWriter{w: w}
}

func (p *prettyJSONWriter) Run(_ context.Context, in <-chan scrapemate.Result) error {
	for result := range in {
		items := jsonWriterAsSlice(result.Data)

		for i := range items {
			if e, ok := items[i].(*gmaps.Entry); ok {
				if len(e.Albums) == 0 || len(e.UserReviewsExtended) == 0 {
					continue
				}
			}

			b, err := json.MarshalIndent(items[i], "", "  ")
			if err != nil {
				return err
			}

			if _, err := p.w.Write(b); err != nil {
				return err
			}

			if _, err := p.w.Write([]byte("\n")); err != nil {
				return err
			}
		}
	}

	return nil
}

func jsonWriterAsSlice(t any) []any {
	if s, ok := t.([]any); ok {
		return s
	}

	return []any{t}
}
