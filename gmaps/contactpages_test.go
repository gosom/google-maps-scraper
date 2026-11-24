//nolint:testpackage // Exercise contact discovery and the queued enrichment lifecycle.
package gmaps

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/gosom/scrapemate"
	httpfetcher "github.com/gosom/scrapemate/adapters/fetchers/nethttp"
	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/exiter"
)

func TestDiscoverContactPages(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		base string
		html string
		want []string
	}{
		{
			name: "prioritize contact and bound candidates",
			base: "https://example.org/",
			html: `<a href="/about">About</a><a href="/team">Team</a>
				<a href="/contact">Contact</a><a href="/contact/#email">Contact again</a>
				<a href="/impressum">Impressum</a><a href="/staff">Staff</a>`,
			want: []string{"https://example.org/contact", "https://example.org/impressum", "https://example.org/about"},
		},
		{
			name: "resolve relative links against final homepage URL",
			base: "https://www.example.org/en/",
			html: `<a href="contact.html#email">Write to us</a><a href="../about">About</a>
				<a href="https://example.org/contact">Contact</a>`,
			want: []string{"https://www.example.org/en/contact.html", "https://example.org/contact", "https://www.example.org/about"},
		},
		{
			name: "ignore external unsafe irrelevant and self links",
			base: "https://example.org/",
			html: `<a href="https://example.org.evil.test/contact">Contact</a>
				<a href="https://other.example.org/contact">Contact</a>
				<a href="https://example.org:8443/contact">Contact</a>
				<a href="https://user@example.org/contact">Contact</a>
				<a href="mailto:contact@example.org">Contact</a>
				<a href="javascript:alert(1)">Contact</a><a href="#contact">Contact</a>
				<a href="/">Contact</a><a href="/products">Products</a>
				<a href="/%zz">Contact</a><a href="">Contact</a>`,
			want: []string{},
		},
		{
			name: "use link labels and retain meaningful query strings",
			base: "http://example.org/",
			html: `<a href="https://example.org/page?id=2">Contact us</a>
				<a href="https://example.org/page?id=3">About us</a>`,
			want: []string{"https://example.org/page?id=2", "https://example.org/page?id=3"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			base, err := url.Parse(tc.base)
			require.NoError(t, err)
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(tc.html))
			require.NoError(t, err)
			require.Equal(t, tc.want, discoverContactPages(doc, base))
		})
	}
}

func TestEmailContactFallback(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		home       string
		pages      map[string]string
		wantPaths  []string
		wantEmails []string
	}{
		{
			name:       "homepage email prevents fallback",
			home:       `<a href="mailto:home@example.org">Email</a><a href="/contact">Contact</a>`,
			wantPaths:  []string{"/"},
			wantEmails: []string{"home@example.org"},
		},
		{
			name: "find contact-only email and stop before about",
			home: `<a href="/about">About</a><a href="/contact">Contact</a> logo@2x.png`,
			pages: map[string]string{"/contact": `<a href="mailto:CONTACT@example.org">Email</a>
				<a href="mailto:contact@example.org">Duplicate</a><a href="mailto:user@sentry.io">Junk</a>`},
			wantPaths:  []string{"/", "/contact"},
			wantEmails: []string{"contact@example.org"},
		},
		{
			name:       "failed contact falls back to about",
			home:       `<a href="/contact">Contact</a><a href="/about">About</a>`,
			pages:      map[string]string{"/about": `Write to about@example.org`},
			wantPaths:  []string{"/", "/contact", "/about"},
			wantEmails: []string{"about@example.org"},
		},
		{
			name: "three page limit and no recursive discovery",
			home: `<a href="/contact">Contact</a><a href="/kontakt">Kontakt</a>
				<a href="/about">About</a><a href="/team">Team</a>`,
			pages:     map[string]string{"/contact": `<a href="/contact-more">Contact</a>`, "/about": `No email here`},
			wantPaths: []string{"/", "/contact", "/kontakt", "/about"},
		},
		{
			name:      "no candidate links preserves entry",
			home:      `<a href="/products">Products</a>`,
			wantPaths: []string{"/"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var paths []string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page, exists := tc.pages[r.URL.Path]
				if r.URL.Path == "/" {
					page, exists = tc.home, true
				}

				if !exists {
					http.Error(w, "failure@example.org", http.StatusInternalServerError)
					return
				}

				w.Header().Set("Content-Type", "text/html")

				_, err := w.Write([]byte(page))
				if err != nil {
					t.Errorf("write fixture: %v", err)
				}
			}))
			t.Cleanup(server.Close)
			fetcher := httpfetcher.New(server.Client())
			tracker := &emailCompletionCounter{}
			entry := &Entry{ID: "seed", Title: "Business", WebSite: server.URL}
			job := NewEmailJob("place", entry, WithEmailJobExitMonitor(tracker))

			for attempts := 0; ; attempts++ {
				require.LessOrEqual(t, attempts, 3, "fallback must be bounded")

				u, err := url.Parse(job.URL)
				require.NoError(t, err)

				path := u.Path
				if path == "" {
					path = "/"
				}

				paths = append(paths, path)

				resp := fetcher.Fetch(t.Context(), job)
				if resp.Error == nil {
					resp.Document, err = goquery.NewDocumentFromReader(strings.NewReader(string(resp.Body)))
					require.NoError(t, err)
				}

				result, next, err := job.Process(t.Context(), &resp)
				require.NoError(t, err)
				require.Nil(t, resp.Body)
				require.Nil(t, resp.Document)

				if len(next) == 0 {
					require.Same(t, entry, result)
					require.True(t, job.UseInResults())

					break
				}

				require.Nil(t, result)
				require.False(t, job.UseInResults())
				require.Zero(t, tracker.completed)
				require.Len(t, next, 1)
				require.Equal(t, job.ID, next[0].GetParentID())

				var ok bool

				job, ok = next[0].(*EmailExtractJob)
				require.True(t, ok)
				require.Equal(t, contactPageTimeout, job.GetTimeout())
			}

			require.Equal(t, tc.wantPaths, paths)
			require.Equal(t, tc.wantEmails, entry.Emails)
			require.Equal(t, 1, tracker.completed)
		})
	}
}

type emailCompletionCounter struct {
	exiter.Exiter
	completed int
}

func (c *emailCompletionCounter) IncrPlacesCompleted(n int) { c.completed += n }

func TestEmailFallbackFailuresAndCompletion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		response scrapemate.Response
		cancel   bool
	}{
		{name: "network failure", response: scrapemate.Response{Error: errors.New("unreachable")}},
		{name: "missing document"},
		{name: "cancelled", cancel: true},
		{name: "offsite redirect", response: scrapemate.Response{URL: "https://unrelated.org/contact"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tracker := &emailCompletionCounter{}
			entry := &Entry{WebSite: "https://example.org"}
			job := NewEmailJob("place", entry, WithEmailJobExitMonitor(tracker), WithEmailJobWriterManagedCompletion())
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(`<a href="/contact">Contact</a><a href="/about">About</a>`))
			require.NoError(t, err)
			_, next, err := job.Process(t.Context(), &scrapemate.Response{Document: doc})
			require.NoError(t, err)
			require.Len(t, next, 1)
			contact, ok := next[0].(*EmailExtractJob)
			require.True(t, ok)
			require.True(t, contact.WriterManagedCompletion)

			resp := tc.response
			if resp.URL != "" {
				resp.Document, err = goquery.NewDocumentFromReader(strings.NewReader(`wrong@example.org`))
				require.NoError(t, err)

				resp.Body = []byte(`wrong@example.org`)
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if tc.cancel {
				cancel()
			}

			result, next, err := contact.Process(ctx, &resp)
			require.NoError(t, err)

			if tc.cancel {
				require.Same(t, entry, result)
				require.Empty(t, next)
			} else {
				require.Nil(t, result)
				require.Len(t, next, 1)
				last, ok := next[0].(*EmailExtractJob)
				require.True(t, ok)
				require.True(t, last.WriterManagedCompletion)
				result, next, err = last.Process(ctx, &scrapemate.Response{Error: errors.New("failed")})
				require.NoError(t, err)
				require.Same(t, entry, result)
				require.Empty(t, next)
			}

			require.Empty(t, entry.Emails)
			require.Zero(t, tracker.completed, "writer owns completion")
		})
	}
}

func TestEmailFallbackSurvivesQueueSerialization(t *testing.T) {
	t.Parallel()

	entry := &Entry{ID: "seed", WebSite: "https://example.org/"}
	job := NewEmailJob("place", entry)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`<a href="/contact">Contact</a><a href="/about">About</a>`))
	require.NoError(t, err)
	_, next, err := job.Process(t.Context(), &scrapemate.Response{Document: doc})
	require.NoError(t, err)
	require.Len(t, next, 1)

	var payload bytes.Buffer
	require.NoError(t, gob.NewEncoder(&payload).Encode(next[0]))

	var restored EmailExtractJob
	require.NoError(t, gob.NewDecoder(&payload).Decode(&restored))
	require.True(t, restored.IsContactPage)
	require.Equal(t, []string{"https://example.org/about"}, restored.ContactPages)
	require.Equal(t, "https://example.org/", restored.ContactOrigin.String())

	// A failing contact page must still reach the queued about page after decoding.
	result, next, err := restored.Process(t.Context(), &scrapemate.Response{Error: errors.New("timeout")})
	require.NoError(t, err)
	require.Nil(t, result)
	require.Len(t, next, 1)

	body := `<a href="/contact-again">Contact</a> contact@example.org`
	doc, err = goquery.NewDocumentFromReader(strings.NewReader(body))
	require.NoError(t, err)
	result, next, err = next[0].Process(t.Context(), &scrapemate.Response{Document: doc, Body: []byte(body)})
	require.NoError(t, err)
	require.Empty(t, next)

	got, ok := result.(*Entry)
	require.True(t, ok)
	require.Equal(t, entry.ID, got.ID)
	require.Equal(t, []string{"contact@example.org"}, got.Emails)
}

func TestEmailFallbackHomepageRedirect(t *testing.T) {
	t.Parallel()

	job := NewEmailJob("place", &Entry{WebSite: "http://example.org"})
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`<a href="contact">Contact</a>`))
	require.NoError(t, err)
	_, next, err := job.Process(t.Context(), &scrapemate.Response{
		URL: "https://www.example.org/en/", Document: doc,
	})
	require.NoError(t, err)
	require.Len(t, next, 1)
	require.Equal(t, "https://www.example.org/en/contact", next[0].GetURL())

	body := "redirected@example.org"
	doc, err = goquery.NewDocumentFromReader(strings.NewReader(body))
	require.NoError(t, err)
	result, next, err := next[0].Process(t.Context(), &scrapemate.Response{
		URL: "https://example.org/en/contact/", Document: doc, Body: []byte(body),
	})
	require.NoError(t, err)
	require.Empty(t, next)
	require.Same(t, job.Entry, result)
	require.Equal(t, []string{"redirected@example.org"}, job.Entry.Emails)
}
