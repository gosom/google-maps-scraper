//go:build plugin
// +build plugin

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
)

var _ scrapemate.ResultWriter = (*exampleWriter)(nil)

var DummyPrinter scrapemate.ResultWriter = newWriter("dummy.txt")

type exampleWriter struct {
	w *bufio.Writer
}

func newWriter(fname string) scrapemate.ResultWriter {
	fd, err := os.Create(fname)
	if err != nil {
		panic(err)
	}

	return &exampleWriter{
		w: bufio.NewWriter(fd),
	}
}

// Run is the main function of the writer
// we we write the job id and the title of the entries in a file
// notice the asSlice function that converts the data to a slice of *gmaps.Entry
func (e *exampleWriter) Run(_ context.Context, in <-chan scrapemate.Result) error {
	defer e.w.Flush()

	for result := range in {
		job, ok := result.Job.(scrapemate.IJob)
		if !ok {
			return fmt.Errorf("cannot cast %T to IJob", result.Job)
		}

		items, err := asSlice(result.Data)
		if err != nil {
			return err
		}

		for _, item := range items {
			_, err := fmt.Fprintf(e.w, "Job %s: %s\n", job.GetID(), item.Title)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func asSlice(t any) ([]*gmaps.Entry, error) {
	var elements []*gmaps.Entry

	isSlice, ok := t.([]any)
	if ok {
		elements := make([]*gmaps.Entry, len(isSlice))
		for i, v := range isSlice {
			elements[i], ok = v.(*gmaps.Entry)
			if !ok {
				return nil, fmt.Errorf("cannot cast %T to *gmaps.Entry", v)
			}
		}
	} else {
		element, ok := t.(*gmaps.Entry)
		if !ok {
			return nil, fmt.Errorf("cannot cast %T to *gmaps.Entry", t)
		}

		elements = append(elements, element)
	}

	return elements, nil
}
