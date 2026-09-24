package gmaps

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestCleanEmail(t *testing.T) {
	valid := []struct {
		raw  string
		want string
	}{
		{"contact@example.org", "contact@example.org"},
		{`"contact@example.org"`, "contact@example.org"},
		{`'sales@example.org'`, "sales@example.org"},
		{"<info@example.org>", "info@example.org"},
		{"  info@example.org  ", "info@example.org"},
		{"Contact@Example.ORG", "contact@example.org"},
	}
	for _, tc := range valid {
		got, ok := cleanEmail(tc.raw)
		if !ok {
			t.Errorf("cleanEmail(%q) = rejected, want %q", tc.raw, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("cleanEmail(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}

	rejected := []string{
		// asset/media filenames shaped like name@2x.ext
		"logo-300x112@2x.avif",
		"header@2x.png",
		"hero@2x.webp",
		"photo@2x.jpg",
		"icon@2x.svg",
		"style@2x.css",
		"app@2x.js",
		// telemetry/tracker domains
		"user@sentry.example.com",
		"user@sentry.wixpress.com",
		"alerts@telemetry.example.com",
		"user@sentry.io",
		// control characters
		"a\x00b@example.org",
		"contact\x07@example.org",
		// invalid email formats
		"not-an-email",
		"@nodomain",
		"",
	}
	for _, raw := range rejected {
		if got, ok := cleanEmail(raw); ok {
			t.Errorf("cleanEmail(%q) = %q, want rejected", raw, got)
		}
	}
}

func TestCleanEmailControlChars(t *testing.T) {
	for _, raw := range []string{"a\x00@example.org", "a\x1fb@example.org", "a\x7fb@example.org"} {
		if got, ok := cleanEmail(raw); ok {
			t.Errorf("cleanEmail(%q) = %q, want rejected", raw, got)
		}
	}
}

func TestDocEmailExtractor(t *testing.T) {
	html := `<html><body>
		<a href="mailto:contact@example.org">mail</a>
		<a href="mailto:&quot;quoted@example.org&quot;">q</a>
		<a href="mailto:logo@2x.avif">asset</a>
		<a href="mailto:user@sentry.example.com">telemetry</a>
		<a href="mailto:contact@example.org">dupe</a>
	</body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	emails := docEmailExtractor(doc)
	if len(emails) != 2 {
		t.Fatalf("got %v, want 2 emails", emails)
	}
	if emails[0] != "contact@example.org" || emails[1] != "quoted@example.org" {
		t.Fatalf("got %v, want [contact@example.org quoted@example.org]", emails)
	}
}

func TestRegexEmailExtractor(t *testing.T) {
	body := []byte(`contact contact@example.org or "sales@example.org" <info@example.org>,
		ignore logo-300x112@2x.avif header@2x.png hero@2x.webp user@sentry.wixpress.com`)
	emails := regexEmailExtractor(body)
	seen := map[string]bool{}
	for _, e := range emails {
		seen[e] = true
	}
	for _, want := range []string{"contact@example.org", "sales@example.org", "info@example.org"} {
		if !seen[want] {
			t.Errorf("missing %q in %v", want, emails)
		}
	}
	for _, junk := range []string{"logo-300x112@2x.avif", "header@2x.png", "hero@2x.webp", "user@sentry.wixpress.com"} {
		if seen[junk] {
			t.Errorf("junk %q leaked into %v", junk, emails)
		}
	}
}
