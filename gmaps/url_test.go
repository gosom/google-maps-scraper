package gmaps

import (
	"testing"
)

func TestIsGoogleMapsURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "regular search query - should NOT be detected as URL",
			input:    "pizza in NYC",
			expected: false,
		},
		{
			name:     "search URL with maps path",
			input:    "https://www.google.com/maps/search/pizza",
			expected: true,
		},
		{
			name:     "place URL",
			input:    "https://www.google.com/maps/place/Empire+State+Building/@40.7484405,-73.9856632",
			expected: true,
		},
		{
			name:     "short URL",
			input:    "maps.app.goo.gl/abc123",
			expected: true,
		},
		{
			name:     "google.com/maps without scheme - should NOT be detected",
			input:    "google.com/maps/search/pizza",
			expected: false,
		},
		{
			name:     "maps.google.com URL",
			input:    "https://maps.google.com/maps?z=16&q=Empire+State+Building",
			expected: true,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "invalid URL",
			input:    "://invalid",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isGoogleMapsURL(tt.input)
			if result != tt.expected {
				t.Errorf("isGoogleMapsURL(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
