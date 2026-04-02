package gmaps

import (
	"strings"
	"testing"
)

// TestGenerateURL_HexPlaceID verifies that generateURL correctly extracts a
// hex-format place ID (0x...:0x...) from the original job URL and produces a
// valid listugcposts RPC URL.  This is the happy path -- the format stored in
// the database and returned by j.GetURL().
func TestGenerateURL_HexPlaceID(t *testing.T) {
	t.Parallel()

	// This is the URL format stored in the DB (from search-results scraping).
	// The first !1s segment is the hex place ID.
	dbURL := "https://www.google.com/maps/place/Cappuccino+Grand+Caf%C3%A9+-+Mitte/" +
		"data=!4m7!3m6" +
		"!1s0x47a851e89f1fc219:0xe52af13d04a302f2" +
		"!8m2!3d52.5266063!4d13.3936742" +
		"!16s%2Fg%2F11bzyqf0mp" +
		"!19sChIJGcIfn-hRqEcR8gKjBD3xKuU?authuser=0&hl=en&rclk=1"

	f := &fetcher{params: fetchReviewsParams{}}
	got, err := f.generateURL(dbURL, "", 20, "testRequestID123")
	if err != nil {
		t.Fatalf("generateURL returned error for hex place ID URL: %v", err)
	}

	// The RPC URL must contain the hex place ID, not a photo/streetview ID.
	wantPlaceID := "0x47a851e89f1fc219%3A0xe52af13d04a302f2"
	if !strings.Contains(got, wantPlaceID) {
		t.Errorf("RPC URL does not contain expected hex place ID.\n  want substring: %s\n  got URL: %s", wantPlaceID, got)
	}

	if !strings.Contains(got, "listugcposts") {
		t.Errorf("RPC URL missing listugcposts endpoint: %s", got)
	}
}

// TestGenerateURL_Base64PhotoID_Bug demonstrates the bug: when Google Maps
// redirects the browser, page.URL() gains a streetview/photo data segment
// whose !1s value is a base64 photo ID (e.g. CIHM0ogKEICAgICOyf7YlgE).
// The regex `!1s([^!]+)` grabs the FIRST !1s match -- which is now the photo
// ID, not the place ID.  The RPC API rejects this with HTTP 400.
func TestGenerateURL_Base64PhotoID_Bug(t *testing.T) {
	t.Parallel()

	// This is the URL captured by page.URL() after Google Maps redirects.
	// Note the streetview/photo data block:
	//   !3m8!1e2!3m6!1sCIHM0ogKEICAgICOyf7YlgE!2e10!3e12!6s...
	// The first !1s match is the base64 photo ID, NOT the place ID.
	redirectedURL := "https://www.google.com/maps/place/Cappuccino+Grand+Caf%C3%A9+-+Mitte/" +
		"@52.5264618,13.3937686,3a,75y,90t/data=" +
		"!3m8!1e2!3m6" +
		"!1sCIHM0ogKEICAgICOyf7YlgE" + // <-- base64 photo ID (WRONG for reviews API)
		"!2e10!3e12" +
		"!6shttps:%2F%2Flh3.googleusercontent.com%2Fgps-cs-s%2FAHVAweppNfzWzLuQ-DfcwlhYHWN5a_rj6L_yRweYMKoUnf6dUjYZgERbA0YIqnVGw78KmDt7xJAOEn8x5r89aTr3hojXCSpKSbJhodD6rJndsulFq_PADKPYAP_JbEgcNn-THdpMO_Slug%3Dw129-h86-k-no" +
		"!7i4724!8i3149" +
		"!4m7!3m6" +
		"!1s0x47a851e89f1fc219:0xe52af13d04a302f2" + // <-- actual hex place ID (CORRECT)
		"!8m2!3d52.5266063!4d13.3936742!10e5" +
		"!16s%2Fg%2F11bzyqf0mp?authuser=0&hl=en&entry=ttu&g_ep=EgoyMDI2MDMzMC4wIKXMDSoASAFQAw%3D%3D"

	f := &fetcher{params: fetchReviewsParams{}}
	got, err := f.generateURL(redirectedURL, "", 20, "testRequestID123")
	if err != nil {
		t.Fatalf("generateURL returned error: %v", err)
	}

	// BUG: The regex extracts the FIRST !1s match, which is the base64 photo ID.
	// This causes the review API to return HTTP 400.
	badPhotoID := "CIHM0ogKEICAgICOyf7YlgE"
	correctPlaceID := "0x47a851e89f1fc219"

	if strings.Contains(got, badPhotoID) {
		t.Errorf("BUG CONFIRMED: generateURL extracted the base64 photo ID instead of the hex place ID.\n"+
			"  extracted (wrong): %s\n"+
			"  expected (right):  %s\n"+
			"  full RPC URL:      %s",
			badPhotoID, correctPlaceID, got)
	}

	if !strings.Contains(got, correctPlaceID) {
		t.Errorf("RPC URL does not contain the correct hex place ID %q.\n  got URL: %s",
			correctPlaceID, got)
	}
}

// TestGenerateURL_OriginalJobURL_Workaround shows that using j.GetURL()
// (the original job URL from the database) instead of page.URL() would
// fix the bug, because it always has the hex place ID as the first !1s.
func TestGenerateURL_OriginalJobURL_Workaround(t *testing.T) {
	t.Parallel()

	// Simulates j.GetURL() -- the original URL created at job time, stored
	// in the Job.URL field. This never has streetview/photo data prepended.
	originalJobURL := "https://www.google.com/maps/place/Cappuccino+Grand+Caf%C3%A9+-+Mitte/" +
		"data=!4m7!3m6" +
		"!1s0x47a851e89f1fc219:0xe52af13d04a302f2" +
		"!8m2!3d52.5266063!4d13.3936742" +
		"!16s%2Fg%2F11bzyqf0mp" +
		"!19sChIJGcIfn-hRqEcR8gKjBD3xKuU?authuser=0&hl=en&rclk=1"

	f := &fetcher{params: fetchReviewsParams{}}
	got, err := f.generateURL(originalJobURL, "", 20, "testRequestID123")
	if err != nil {
		t.Fatalf("generateURL returned error: %v", err)
	}

	wantPlaceID := "0x47a851e89f1fc219%3A0xe52af13d04a302f2"
	if !strings.Contains(got, wantPlaceID) {
		t.Fatalf("WORKAROUND FAILED: even original URL doesn't produce correct place ID.\n  got: %s", got)
	}

	t.Logf("WORKAROUND WORKS: using j.GetURL() produces correct RPC URL with hex place ID")
}

// TestGenerateURL_AnotherBase64Example verifies the bug with a different
// place to rule out coincidence.
func TestGenerateURL_AnotherBase64Example(t *testing.T) {
	t.Parallel()

	// Strandbad Mitte -- from the same failing job run.
	redirectedURL := "https://www.google.com/maps/place/Strandbad+Mitte/" +
		"@52.527351,13.3967289,3a,75y,90t/data=" +
		"!3m8!1e2!3m6" +
		"!1sCIHM0ogKEICAgICEzaSOMw" + // base64 photo ID
		"!2e10!3e12" +
		"!6shttps:%2F%2Flh3.googleusercontent.com%2Fgps-cs-s%2FAHVAweqeb-jD4Mu73tyFGF_9LCLywQZ_xIgM0eIrMoKoJrol7qS-SIPVYFhS0qx521HNmjJuI9sufpZbtdyMOuMLWm-Z-UzASd6VpI5daPNOLx6pszb3uMeSbAz_PJsJD2kjH-pdpVYG%3Dw114-h86-k-no" +
		"!7i4032!8i3024" +
		"!4m7!3m6" +
		"!1s0x47a851e673c68925:0x9b91bbf48e591a1a" + // correct hex place ID
		"!8m2!3d52.5273205!4d13.3966553!10e5" +
		"!16s%2Fg%2F1thg9h43?authuser=0&hl=en&entry=ttu&g_ep=EgoyMDI2MDMzMC4wIKXMDSoASAFQAw%3D%3D"

	f := &fetcher{params: fetchReviewsParams{}}
	got, err := f.generateURL(redirectedURL, "", 20, "testRequestID123")
	if err != nil {
		t.Fatalf("generateURL returned error: %v", err)
	}

	badPhotoID := "CIHM0ogKEICAgICEzaSOMw"
	correctPlaceID := "0x47a851e673c68925"

	if strings.Contains(got, badPhotoID) {
		t.Errorf("BUG CONFIRMED (Strandbad Mitte): extracted photo ID %q instead of place ID %q.\n  RPC URL: %s",
			badPhotoID, correctPlaceID, got)
	}
}
