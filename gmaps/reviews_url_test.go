package gmaps

import (
	"strings"
	"testing"
)

// TestGenerateURL_HexPlaceID verifies that generateURL correctly extracts a
// hex-format place ID from the original job URL and produces a valid RPC URL.
func TestGenerateURL_HexPlaceID(t *testing.T) {
	t.Parallel()

	dbURL := "https://www.google.com/maps/place/Cappuccino+Grand+Caf%C3%A9+-+Mitte/" +
		"data=!4m7!3m6" +
		"!1s0x47a851e89f1fc219:0xe52af13d04a302f2" +
		"!8m2!3d52.5266063!4d13.3936742" +
		"!16s%2Fg%2F11bzyqf0mp" +
		"!19sChIJGcIfn-hRqEcR8gKjBD3xKuU?authuser=0&hl=en&rclk=1"

	f := &fetcher{params: fetchReviewsParams{langCode: "en"}}
	got, err := f.generateURL(dbURL, "", 20, "testRequestID123")
	if err != nil {
		t.Fatalf("generateURL returned error: %v", err)
	}

	wantPlaceID := "0x47a851e89f1fc219%3A0xe52af13d04a302f2"
	if !strings.Contains(got, wantPlaceID) {
		t.Errorf("RPC URL missing hex place ID.\n  want: %s\n  got:  %s", wantPlaceID, got)
	}
	if !strings.Contains(got, "hl=en") {
		t.Errorf("RPC URL missing language parameter hl=en: %s", got)
	}
}

// TestGenerateURL_RedirectedURL_ExtractsFirstID documents that generateURL
// extracts the FIRST !1s match. When given a redirected URL with a photo
// segment, this is the photo ID (not the place ID). This is a known limitation —
// the fix is to pass j.GetURL() (original URL) instead of page.URL() (redirected).
func TestGenerateURL_RedirectedURL_ExtractsFirstID(t *testing.T) {
	t.Parallel()

	redirectedURL := "https://www.google.com/maps/place/Cappuccino+Grand+Caf%C3%A9+-+Mitte/" +
		"@52.5264618,13.3937686,3a,75y,90t/data=" +
		"!3m8!1e2!3m6" +
		"!1sCIHM0ogKEICAgICOyf7YlgE" + // base64 photo ID (first !1s match)
		"!2e10!3e12!6shttps:%2F%2Fexample.com!7i4724!8i3149" +
		"!4m7!3m6" +
		"!1s0x47a851e89f1fc219:0xe52af13d04a302f2" + // hex place ID (second !1s match)
		"!8m2!3d52.5266063!4d13.3936742!10e5!16s%2Fg%2F11bzyqf0mp"

	f := &fetcher{params: fetchReviewsParams{langCode: "en"}}
	got, err := f.generateURL(redirectedURL, "", 20, "testRequestID123")
	if err != nil {
		t.Fatalf("generateURL returned error: %v", err)
	}

	// Known behavior: regex grabs the FIRST !1s which is the photo ID.
	// This is why the fix uses j.GetURL() (which never has photo segments).
	photoID := "CIHM0ogKEICAgICOyf7YlgE"
	if !strings.Contains(got, photoID) {
		t.Errorf("Expected first !1s match (photo ID %q) to be extracted.\n  got: %s", photoID, got)
	}
}

// TestGenerateURL_OriginalJobURL_Fix verifies that the original job URL
// (from j.GetURL()) always produces a correct RPC URL — this is the fix.
func TestGenerateURL_OriginalJobURL_Fix(t *testing.T) {
	t.Parallel()

	originalJobURL := "https://www.google.com/maps/place/Cappuccino+Grand+Caf%C3%A9+-+Mitte/" +
		"data=!4m7!3m6" +
		"!1s0x47a851e89f1fc219:0xe52af13d04a302f2" +
		"!8m2!3d52.5266063!4d13.3936742" +
		"!16s%2Fg%2F11bzyqf0mp" +
		"!19sChIJGcIfn-hRqEcR8gKjBD3xKuU?authuser=0&hl=en&rclk=1"

	f := &fetcher{params: fetchReviewsParams{langCode: "en"}}
	got, err := f.generateURL(originalJobURL, "", 20, "testRequestID123")
	if err != nil {
		t.Fatalf("generateURL returned error: %v", err)
	}

	wantPlaceID := "0x47a851e89f1fc219%3A0xe52af13d04a302f2"
	if !strings.Contains(got, wantPlaceID) {
		t.Fatalf("Fix broken: j.GetURL() did not produce correct place ID.\n  want: %s\n  got:  %s", wantPlaceID, got)
	}
}

// TestGenerateURL_SecondPlace verifies the fix with a different place.
func TestGenerateURL_SecondPlace(t *testing.T) {
	t.Parallel()

	originalJobURL := "https://www.google.com/maps/place/Strandbad+Mitte/" +
		"data=!4m7!3m6" +
		"!1s0x47a851e673c68925:0x9b91bbf48e591a1a" +
		"!8m2!3d52.5273205!4d13.3966553" +
		"!16s%2Fg%2F1thg9h43?authuser=0&hl=en&rclk=1"

	f := &fetcher{params: fetchReviewsParams{langCode: "de"}}
	got, err := f.generateURL(originalJobURL, "", 20, "testRequestID123")
	if err != nil {
		t.Fatalf("generateURL returned error: %v", err)
	}

	if !strings.Contains(got, "0x47a851e673c68925") {
		t.Errorf("Missing hex place ID in RPC URL: %s", got)
	}
	if !strings.Contains(got, "hl=de") {
		t.Errorf("Missing language hl=de in RPC URL: %s", got)
	}
}
