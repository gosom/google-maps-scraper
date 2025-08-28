//go:build plugin
// +build plugin

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
)

var _ scrapemate.ResultWriter = (*googleSheetsWriter)(nil)

// GoogleSheetsWriter is the exported plugin variable
var GoogleSheetsWriter scrapemate.ResultWriter = newGoogleSheetsWriter()

type googleSheetsWriter struct {
	client *GoogleSheetsClient
}

func newGoogleSheetsWriter() scrapemate.ResultWriter {
	client, err := NewGoogleSheetsClient()
	if err != nil {
		log.Fatalf("Failed to initialize Google Sheets client: %v", err)
	}

	return &googleSheetsWriter{
		client: client,
	}
}

// Run is the main function of the writer
// It processes results and sends them to Google Sheets via webhook
func (g *googleSheetsWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	for result := range in {
		job, ok := result.Job.(scrapemate.IJob)
		if !ok {
			log.Printf("Warning: cannot cast %T to IJob, skipping", result.Job)
			continue
		}

		entries, err := asSlice(result.Data)
		if err != nil {
			log.Printf("Error converting result data to entries for job %s: %v", job.GetID(), err)
			continue
		}

		for _, entry := range entries {
			if err := g.client.SendEntry(ctx, entry); err != nil {
				log.Printf("Error sending entry to Google Sheets for job %s, title '%s': %v", 
					job.GetID(), entry.Title, err)
				// Continue processing other entries even if one fails
				continue
			}
			log.Printf("Successfully sent entry to Google Sheets: %s", entry.Title)
		}
	}

	return nil
}

// asSlice converts the result data to a slice of *gmaps.Entry
// This function is copied from the example plugin
func asSlice(t any) ([]*gmaps.Entry, error) {
	var elements []*gmaps.Entry

	isSlice, ok := t.([]any)
	if ok {
		elements = make([]*gmaps.Entry, len(isSlice))
		for i, v := range isSlice {
			element, ok := v.(*gmaps.Entry)
			if !ok {
				return nil, fmt.Errorf("cannot cast %T to *gmaps.Entry", v)
			}
			elements[i] = element
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