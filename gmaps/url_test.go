package gmaps_test

import (
	"testing"

	"github.com/gosom/google-maps-scraper/gmaps"
)

func TestNewGmapJobBuildsURLFromQuery(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		expectedURL string
	}{
		{
			name:        "regular search query",
			query:       "pizza in NYC",
			expectedURL: "https://www.google.com/maps/search/pizza+in+NYC",
		},
		{
			name:        "search URL with maps path",
			query:       "https://www.google.com/maps/search/pizza",
			expectedURL: "https://www.google.com/maps/search/pizza",
		},
		{
			name:        "place URL",
			query:       "https://www.google.com/maps/place/Empire+State+Building/@40.7484405,-73.9856632",
			expectedURL: "https://www.google.com/maps/place/Empire+State+Building/@40.7484405,-73.9856632",
		},
		{
			name:        "short URL",
			query:       "maps.app.goo.gl/abc123",
			expectedURL: "maps.app.goo.gl/abc123",
		},
		{
			name:        "google.com/maps without scheme",
			query:       "google.com/maps/search/pizza",
			expectedURL: "https://www.google.com/maps/search/google.com%2Fmaps%2Fsearch%2Fpizza",
		},
		{
			name:        "maps.google.com URL",
			query:       "https://maps.google.com/maps?z=16&q=Empire+State+Building",
			expectedURL: "https://maps.google.com/maps?z=16&q=Empire+State+Building",
		},
		{
			name:        "empty string",
			query:       "",
			expectedURL: "https://www.google.com/maps/search/",
		},
		{
			name:        "invalid URL",
			query:       "://invalid",
			expectedURL: "https://www.google.com/maps/search/%3A%2F%2Finvalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := gmaps.NewGmapJob("", "en", tt.query, 0, false, "", 0)

			if job.URL != tt.expectedURL {
				t.Errorf("NewGmapJob(..., %q, ...).URL = %q, want %q", tt.query, job.URL, tt.expectedURL)
			}
		})
	}
}
