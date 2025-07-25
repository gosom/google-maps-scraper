package gmaps_test

import (
	"testing"

	"github.com/gosom/google-maps-scraper/gmaps"
)

func TestExtractDataIDFromURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "Blue Bottle Coffee - actual URL from Phase 8.1",
			url:      "https://www.google.com/maps/place/Blue+Bottle+Coffee/data=!4m7!3m6!1s0x80858098babc2d4b:0xbeedd659cc698c92!8m2!3d37.7763342!4d-122.4232375!16s%2Fm%2F03p12r2!19sChIJSy28upiAhYARkoxpzFnW7b4?authuser=0&hl=en&rclk=1",
			expected: "0x80858098babc2d4b:0xbeedd659cc698c92",
		},
		{
			name:     "Four Barrel Coffee - actual URL from Phase 8.1",
			url:      "https://www.google.com/maps/place/Four+Barrel+Coffee/data=!4m7!3m6!1s0x808f7e218f9bf6ff:0xd739722d19b32b9b!8m2!3d37.7670234!4d-122.4217806!16s%2Fg%2F1yl57jwyn!19sChIJ__abjyF-j4ARmyuzGS1yOdc?authuser=0&hl=en&rclk=1",
			expected: "0x808f7e218f9bf6ff:0xd739722d19b32b9b",
		},
		{
			name:     "Ritual Coffee Roasters - actual URL from Phase 8.1",
			url:      "https://www.google.com/maps/place/Ritual+Coffee+Roasters/data=!4m7!3m6!1s0x808580a2085a4bef:0x97508fc44679a00e!8m2!3d37.7763909!4d-122.4241939!16s%2Fg%2F1td13hjn!19sChIJ70taCKKAhYARDqB5RsSPUJc?authuser=0&hl=en&rclk=1",
			expected: "0x808580a2085a4bef:0x97508fc44679a00e",
		},
		{
			name:     "Test data example - Kipriakon from entry_test.go",
			url:      "https://www.google.com/maps/place/Kipriakon/data=!4m2!3m1!1s0x14e732fd76f0d90d:0xe5415928d6702b47!10m1!1e1",
			expected: "0x14e732fd76f0d90d:0xe5415928d6702b47",
		},
		{
			name:     "URL without DataID pattern",
			url:      "https://www.google.com/maps/search/coffee+shop/@37.7749,-122.4194,15z",
			expected: "",
		},
		{
			name:     "Empty URL",
			url:      "",
			expected: "",
		},
		{
			name:     "Invalid URL",
			url:      "not-a-valid-url",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gmaps.ExtractDataIDFromURL(tt.url)
			if result != tt.expected {
				t.Errorf("ExtractDataIDFromURL(%q) = %q, expected %q", tt.url, result, tt.expected)
			}
		})
	}
}

func TestConvertCIDToDataID(t *testing.T) {
	tests := []struct {
		name     string
		cid      string
		expected string
	}{
		{
			name:     "Test CID from entry_test.go",
			cid:      "16519582940102929223",
			expected: "cid:16519582940102929223",
		},
		{
			name:     "Empty CID",
			cid:      "",
			expected: "cid:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gmaps.ConvertCIDToDataID(tt.cid)
			if result != tt.expected {
				t.Errorf("ConvertCIDToDataID(%q) = %q, expected %q", tt.cid, result, tt.expected)
			}
		})
	}
}
