package gmaps_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/gmaps"
)

func TestNewPlaceJobSanitizesDotDotPlaceURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		expectedURL string
	}{
		{
			name:        "dot-dot place segment before data id",
			url:         "https://www.google.com/maps/place/../data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1",
			expectedURL: "https://www.google.com/maps/place/_/data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1",
		},
		{
			name:        "dot-dot place segment with percent-encoded data id",
			url:         "https://www.google.com/maps/place/../data=!4m7!3m6!1s0x89c2599b5a24d7fd:0x9e354f6cf514b9fc!8m2!3d40.7270884!4d-73.989382!16s%2Fg%2F11c3svpqld!19sChIJ_dckWptZwokR_LkU9WxPNZ4",
			expectedURL: "https://www.google.com/maps/place/_/data=!4m7!3m6!1s0x89c2599b5a24d7fd:0x9e354f6cf514b9fc!8m2!3d40.7270884!4d-73.989382!16s%2Fg%2F11c3svpqld!19sChIJ_dckWptZwokR_LkU9WxPNZ4",
		},
		{
			name:        "ordinary place url with name is unchanged",
			url:         "https://www.google.com/maps/place/Kipriakon/data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1",
			expectedURL: "https://www.google.com/maps/place/Kipriakon/data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1",
		},
		{
			name:        "ordinary search url is unchanged",
			url:         "https://www.google.com/maps/search/pizza+in+NYC",
			expectedURL: "https://www.google.com/maps/search/pizza+in+NYC",
		},
		{
			name:        "short url is unchanged",
			url:         "maps.app.goo.gl/abc123",
			expectedURL: "maps.app.goo.gl/abc123",
		},
		{
			name:        "dot-dot not on a place segment is unchanged",
			url:         "https://www.google.com/maps/myplace/../data=!4m2!3m1!1s0xabc",
			expectedURL: "https://www.google.com/maps/myplace/../data=!4m2!3m1!1s0xabc",
		},
		{
			name:        "dot-dot marker on another host is unchanged",
			url:         "https://example.com/maps/place/../data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1",
			expectedURL: "https://example.com/maps/place/../data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1",
		},
		{
			name:        "dot-dot marker in query is unchanged",
			url:         "https://www.google.com/maps/place/Kipriakon?next=/maps/place/../data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1",
			expectedURL: "https://www.google.com/maps/place/Kipriakon?next=/maps/place/../data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1",
		},
		{
			name:        "dot-dot marker in fragment is unchanged",
			url:         "https://www.google.com/maps/place/Kipriakon#/maps/place/../data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1",
			expectedURL: "https://www.google.com/maps/place/Kipriakon#/maps/place/../data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := gmaps.NewPlaceJob("parent-1", "en", tt.url, false, false)

			if job.URL != tt.expectedURL {
				t.Errorf("NewPlaceJob(..., %q, ...).URL = %q, want %q", tt.url, job.URL, tt.expectedURL)
			}
		})
	}
}

// TestNewPlaceJobDotDotURLSurvivesNormalization is the regression that fails
// without the fix: the raw "/maps/place/../data=" URL loses its "/maps/place/"
// marker under RFC 3986 remove_dot_segments, which is how the detail place is
// dropped. After sanitizing, the marker and the data id both survive.
func TestNewPlaceJobDotDotURLSurvivesNormalization(t *testing.T) {
	const raw = "https://www.google.com/maps/place/../data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1"

	job := gmaps.NewPlaceJob("parent-1", "en", raw, false, false)

	ref, err := url.Parse(job.URL)
	if err != nil {
		t.Fatalf("parse job URL %q: %v", job.URL, err)
	}

	base, _ := url.Parse("https://www.google.com/")
	normalized := base.ResolveReference(ref).String()

	if !strings.Contains(normalized, "/maps/place/") {
		t.Errorf("normalized URL %q dropped the /maps/place/ marker", normalized)
	}

	if !strings.Contains(normalized, "data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1") {
		t.Errorf("normalized URL %q dropped the data id", normalized)
	}
}
