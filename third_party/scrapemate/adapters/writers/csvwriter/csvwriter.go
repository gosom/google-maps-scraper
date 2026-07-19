package csvwriter

import (
	"context"
	"encoding/csv"
	"fmt"
	"reflect"
	"sync"

	"github.com/gosom/scrapemate"
)

var _ scrapemate.ResultWriter = (*csvWriter)(nil)

type csvWriter struct {
	w    *csv.Writer
	once sync.Once
}

// NewCsvWriter creates a new csv writer
func NewCsvWriter(w *csv.Writer) scrapemate.ResultWriter {
	return &csvWriter{w: w}
}

// Run runs the writer.
func (c *csvWriter) Run(_ context.Context, in <-chan scrapemate.Result) error {
	for result := range in {
		elements, err := c.getCsvCapable(result.Data)
		if err != nil {
			return err
		}

		if len(elements) == 0 {
			continue
		}

		c.once.Do(func() {
			// I don't like this, but I don't know how to do it better
			_ = c.w.Write(elements[0].CsvHeaders())
		})

		for _, element := range elements {
			if err := c.w.Write(element.CsvRow()); err != nil {
				return err
			}
		}

		c.w.Flush()
	}

	return c.w.Error()
}

func (c *csvWriter) getCsvCapable(data any) ([]scrapemate.CsvCapable, error) {
	var elements []scrapemate.CsvCapable

	if interfaceIsSlice(data) {
		s := reflect.ValueOf(data)

		for i := 0; i < s.Len(); i++ {
			val := s.Index(i).Interface()
			if element, ok := val.(scrapemate.CsvCapable); ok {
				elements = append(elements, element)
			} else {
				return nil, fmt.Errorf("%w: unexpected data type: %T", scrapemate.ErrorNotCsvCapable, val)
			}
		}
	} else if element, ok := data.(scrapemate.CsvCapable); ok {
		elements = append(elements, element)
	} else {
		return nil, fmt.Errorf("%w: unexpected data type: %T", scrapemate.ErrorNotCsvCapable, data)
	}

	return elements, nil
}

func interfaceIsSlice(t any) bool {
	//nolint:exhaustive // we only need to check for slices
	switch reflect.TypeOf(t).Kind() {
	case reflect.Slice:
		return true
	default:
		return false
	}
}
