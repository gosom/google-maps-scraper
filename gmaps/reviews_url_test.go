package gmaps

import (
	"strings"
	"testing"
)

func TestGenerateURL_HexPlaceID(t *testing.T) {
	t.Parallel()
	dbURL := "https://www.google.com/maps/place/Cappuccino+Grand+Caf%C3%A9+-+Mitte/" +
		"data=!4m7!3m6!1s0x47a851e89f1fc219:0xe52af13d04a302f2" +
		"!8m2!3d52.5266063!4d13.3936742!16s%2Fg%2F11bzyqf0mp" +
		"!19sChIJGcIfn-hRqEcR8gKjBD3xKuU?authuser=0&hl=en&rclk=1"
	f := &fetcher{params: fetchReviewsParams{langCode: "en"}}
	got, err := f.generateURL(dbURL, "", 20, "testID")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(got, "0x47a851e89f1fc219%3A0xe52af13d04a302f2") {
		t.Errorf("missing hex place ID in URL: %s", got)
	}
	if !strings.Contains(got, "hl=en") {
		t.Errorf("missing hl=en in URL: %s", got)
	}
}

func TestGenerateURL_RedirectedURL_ExtractsFirstMatch(t *testing.T) {
	t.Parallel()
	// Redirected URL has base64 photo ID as FIRST !1s — documents known regex behavior
	redirectedURL := "https://www.google.com/maps/place/Test/@52,13,3a,75y,90t/data=" +
		"!3m8!1e2!3m6!1sCIHM0ogKEICAgICOyf7YlgE!2e10!3e12!6shttps:%2F%2Fexample.com!7i4724!8i3149" +
		"!4m7!3m6!1s0x47a851e89f1fc219:0xe52af13d04a302f2!8m2!3d52!4d13!10e5!16s%2Fg%2F11test"
	f := &fetcher{params: fetchReviewsParams{langCode: "en"}}
	got, err := f.generateURL(redirectedURL, "", 20, "testID")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Known behavior: regex grabs FIRST !1s (photo ID). Fix: don't pass redirected URLs.
	if !strings.Contains(got, "CIHM0ogKEICAgICOyf7YlgE") {
		t.Errorf("expected first !1s (photo ID) to be extracted: %s", got)
	}
}

func TestGenerateURL_OriginalJobURL_ProducesCorrectID(t *testing.T) {
	t.Parallel()
	// j.GetURL() always has hex place ID as first !1s — this is the fix
	jobURL := "https://www.google.com/maps/place/Test/data=!4m7!3m6" +
		"!1s0x47a851e89f1fc219:0xe52af13d04a302f2!8m2!3d52!4d13" +
		"!16s%2Fg%2F11test?authuser=0&hl=en&rclk=1"
	f := &fetcher{params: fetchReviewsParams{langCode: "en"}}
	got, err := f.generateURL(jobURL, "", 20, "testID")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(got, "0x47a851e89f1fc219%3A0xe52af13d04a302f2") {
		t.Fatalf("fix broken: missing hex place ID: %s", got)
	}
}

func TestGenerateURL_SecondPlace(t *testing.T) {
	t.Parallel()
	jobURL := "https://www.google.com/maps/place/Strandbad+Mitte/data=!4m7!3m6" +
		"!1s0x47a851e673c68925:0x9b91bbf48e591a1a!8m2!3d52!4d13" +
		"!16s%2Fg%2F1thg9h43?authuser=0&hl=de&rclk=1"
	f := &fetcher{params: fetchReviewsParams{langCode: "de"}}
	got, err := f.generateURL(jobURL, "", 20, "testID")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(got, "0x47a851e673c68925") {
		t.Errorf("missing place ID: %s", got)
	}
	if !strings.Contains(got, "hl=de") {
		t.Errorf("missing hl=de: %s", got)
	}
}
