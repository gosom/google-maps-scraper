// Package leadsdb provides a ResultWriter that saves leads to LeadsDB.
package leadsdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/gosom/go-leadsdb"
	"github.com/gosom/scrapemate"

	"github.com/gosom/google-maps-scraper/gmaps"
)

func New(apiKey string) scrapemate.ResultWriter {
	if apiKey == "" {
		apiKey = os.Getenv("LEADSDB_API_KEY")
	}

	if apiKey == "" {
		panic("LEADSDB_API_KEY environment variable or apiKey parameter not set")
	}

	ans := leadsDBWriter{
		client: leadsdb.New(apiKey),
	}

	return &ans
}

type leadsDBWriter struct {
	client *leadsdb.Client
}

func (l *leadsDBWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	const maxBatchSize = 100

	buff := make([]*leadsdb.Lead, 0, maxBatchSize)
	lastSave := time.Now().UTC()

	for result := range in {
		entry, ok := result.Data.(*gmaps.Entry)
		if !ok {
			return errors.New("invalid data type")
		}

		lead, err := convertToLead(entry)
		if err != nil {
			return err
		}

		buff = append(buff, lead)

		if len(buff) >= maxBatchSize || time.Now().UTC().Sub(lastSave) >= time.Minute {
			err := l.batchSave(ctx, buff)
			if err != nil {
				return err
			}

			buff = buff[:0]
		}
	}

	if len(buff) > 0 {
		err := l.batchSave(ctx, buff)
		if err != nil {
			return err
		}
	}

	return nil
}

func (l *leadsDBWriter) batchSave(ctx context.Context, leads []*leadsdb.Lead) error {
	if len(leads) == 0 {
		return nil
	}

	result, err := l.client.BulkCreate(ctx, leads)
	if err != nil {
		return fmt.Errorf("failed to bulk create leads: %w", err)
	}

	// Log failed leads but don't fail the entire operation
	if result.Failed > 0 {
		for _, e := range result.Errors {
			fmt.Printf("failed to create lead at index %d: %s\n", e.Index, e.Message)
		}
	}

	return nil
}

func convertToLead(entry *gmaps.Entry) (*leadsdb.Lead, error) {
	if entry == nil {
		return nil, errors.New("entry is nil")
	}

	if entry.Title == "" {
		return nil, errors.New("entry title is empty")
	}

	lead := &leadsdb.Lead{
		Name:        entry.Title,
		Source:      "google_maps",
		Description: entry.Description,
		Address:     entry.CompleteAddress.Street,
		City:        entry.CompleteAddress.City,
		State:       entry.CompleteAddress.State,
		Country:     entry.CompleteAddress.Country,
		PostalCode:  entry.CompleteAddress.PostalCode,
		Phone:       entry.Phone,
		Website:     entry.WebSite,
		Category:    entry.Category,
		SourceID:    entry.DataID,
		LogoURL:     entry.Thumbnail,
	}

	// Set coordinates if available
	if entry.Latitude != 0 || entry.Longtitude != 0 {
		lead.Latitude = leadsdb.Ptr(entry.Latitude)
		lead.Longitude = leadsdb.Ptr(entry.Longtitude)
	}

	// Set rating if available
	if entry.ReviewRating > 0 {
		lead.Rating = leadsdb.Ptr(entry.ReviewRating)
	}

	// Set review count if available
	if entry.ReviewCount > 0 {
		lead.ReviewCount = leadsdb.Ptr(entry.ReviewCount)
	}

	// Set email if available (take the first one)
	if len(entry.Emails) > 0 {
		lead.Email = entry.Emails[0]
	}

	// Set categories as tags
	if len(entry.Categories) > 0 {
		lead.Tags = entry.Categories
	}

	// Add additional data as attributes
	var attrs []leadsdb.Attribute

	if entry.Link != "" {
		attrs = append(attrs, leadsdb.TextAttr("google_maps_link", entry.Link))
	}

	if entry.PlusCode != "" {
		attrs = append(attrs, leadsdb.TextAttr("plus_code", entry.PlusCode))
	}

	if entry.Status != "" {
		attrs = append(attrs, leadsdb.TextAttr("status", entry.Status))
	}

	if entry.PriceRange != "" {
		attrs = append(attrs, leadsdb.TextAttr("price_range", entry.PriceRange))
	}

	if entry.Timezone != "" {
		attrs = append(attrs, leadsdb.TextAttr("timezone", entry.Timezone))
	}

	// Add full address as attribute if the street address is empty but full address exists
	if entry.Address != "" && lead.Address == "" {
		attrs = append(attrs, leadsdb.TextAttr("full_address", entry.Address))
	}

	// Add borough if available
	if entry.CompleteAddress.Borough != "" {
		attrs = append(attrs, leadsdb.TextAttr("borough", entry.CompleteAddress.Borough))
	}

	// Add reviews link
	if entry.ReviewsLink != "" {
		attrs = append(attrs, leadsdb.TextAttr("reviews_link", entry.ReviewsLink))
	}

	// Add owner info if available
	if entry.Owner.Name != "" {
		attrs = append(attrs, leadsdb.TextAttr("owner_name", entry.Owner.Name))
	}

	if entry.Owner.Link != "" {
		attrs = append(attrs, leadsdb.TextAttr("owner_link", entry.Owner.Link))
	}

	// Add menu link if available
	if entry.Menu.Link != "" {
		attrs = append(attrs, leadsdb.TextAttr("menu_link", entry.Menu.Link))
	}

	// Add additional emails as attribute if more than one
	if len(entry.Emails) > 1 {
		attrs = append(attrs, leadsdb.ListAttr("additional_emails", entry.Emails[1:]))
	}

	if len(attrs) > 0 {
		lead.Attributes = attrs
	}

	return lead, nil
}
