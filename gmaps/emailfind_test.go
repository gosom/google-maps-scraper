//nolint:testpackage // tests the email finder's unexported internals
package gmaps

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pageDoc(t *testing.T, html string) *goquery.Document {
	t.Helper()

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	require.NoError(t, err)

	return doc
}

// cfSmile is "info@smiledental.com" as Cloudflare's email protection writes it (key 0x42).
const cfSmile = "422b2c242d02312f2b2e2726272c36232e6c212d2f"

func TestUsableEmailKeepsRealAddressesAndDropsJunk(t *testing.T) {
	for raw, want := range map[string]string{
		"Info@SmileDental.com":                                   "info@smiledental.com",
		"mailto:hello@clinic.co.uk?subject=Hi":                   "hello@clinic.co.uk",
		"front%2Edesk@clinic.com":                                "front.desk@clinic.com",
		"reception@clinic.com.":                                  "reception@clinic.com",
		"logo@2x.png":                                            "",
		"hero-image@3x.webp":                                     "",
		"you@example.com":                                        "",
		"info@yourdomain.com":                                    "",
		"8f3a9c2b7d6e4f1a0b5c8d7e6f9a0b1c@sentry.io":             "",
		"a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6@o1.ingest.sentry.io.x": "",
		"name@clinic.com":                                        "",
		"no-at-sign.com":                                         "",
		"two@@clinic.com":                                        "",
	} {
		got, ok := usableEmail(raw)
		if want == "" {
			assert.False(t, ok, "%q should be dropped, got %q", raw, got)

			continue
		}

		assert.True(t, ok, raw)
		assert.Equal(t, want, got, raw)
	}
}

func TestDecodeCloudflareEmail(t *testing.T) {
	got, err := decodeCloudflareEmail(cfSmile)
	require.NoError(t, err)
	assert.Equal(t, "info@smiledental.com", got)

	_, err = decodeCloudflareEmail("zz")
	assert.Error(t, err)
}

func TestEmailsOnPageFindsEveryForm(t *testing.T) {
	html := `<html><body>
		<a href="mailto:frontdesk@smiledental.com,billing@smiledental.com?subject=Appointment">Email us</a>
		<a href="/cdn-cgi/l/email-protection#` + cfSmile + `">[email&#160;protected]</a>
		<span class="__cf_email__" data-cfemail="` + cfSmile + `">[email protected]</span>
		<p>Or write to dr.patel@gmail.com</p>
		<img src="/img/logo@2x.png">
	</body></html>`

	got := emailsOnPage(pageDoc(t, html), []byte(html))

	assert.Subset(t, got, []string{
		"frontdesk@smiledental.com", "billing@smiledental.com", "info@smiledental.com", "dr.patel@gmail.com",
	})
	assert.NotContains(t, got, "logo@2x.png")
}

func TestRankEmailsPutsTheBusinessDomainFirstAndDedupes(t *testing.T) {
	got := rankEmails([]string{
		"dr.patel@gmail.com", "info@smiledental.com", "dr.patel@gmail.com", "team@mail.smiledental.com",
	}, "www.smiledental.com")

	assert.Equal(t, []string{"info@smiledental.com", "team@mail.smiledental.com", "dr.patel@gmail.com"}, got)
}

func TestRankEmailsCapsADirectoryPage(t *testing.T) {
	many := make([]string, 0, 40)
	for i := range 40 {
		many = append(many, "person"+string(rune('a'+i%26))+strings.Repeat("x", i/26)+"@clinic.com")
	}

	assert.Len(t, rankEmails(many, "clinic.com"), maxEmailsPerBusiness)
}

func TestRegistrableHost(t *testing.T) {
	for in, want := range map[string]string{
		"www.smiledental.com":     "smiledental.com",
		"booking.smiledental.com": "smiledental.com",
		"www.clinic.co.uk":        "clinic.co.uk",
		"clinic.de":               "clinic.de",
		"SmileDental.com:443":     "smiledental.com",
	} {
		assert.Equal(t, want, registrableHost(in), in)
	}
}

func TestContactPageLinksStayOnTheSiteAndRankContactFirst(t *testing.T) {
	html := `<html><body>
		<a href="/about-us">About</a>
		<a href="https://www.smiledental.com/contact">Contact</a>
		<a href="https://facebook.com/smiledental/contact">Facebook</a>
		<a href="/brochure.pdf">Contact brochure</a>
		<a href="/services">Services</a>
		<a href="/contact#form">Contact form</a>
		<a href="mailto:x@smiledental.com">mail</a>
	</body></html>`
	base := mustURL(t, "https://www.smiledental.com/")

	got := contactPageLinks(pageDoc(t, html), base, maxContactPages)

	assert.Equal(t, []string{"https://www.smiledental.com/contact", "https://www.smiledental.com/about-us"}, got)
}

func TestFindBusinessEmailsCollectsEveryAddressFromTheSite(t *testing.T) {
	// The front page already has one of the business's own addresses; the contact and team pages
	// have more. All of them are wanted.
	home := `<html><body><a href="/contact-us">Contact</a> <a href="/our-team">Team</a>
		info@smiledental.com <p>Web design by studio@agency.io</p></body></html>`
	pages := map[string]string{
		"https://smiledental.com/contact-us": `<a href="mailto:bookings@smiledental.com">Book</a> info@smiledental.com`,
		"https://smiledental.com/our-team":   `Dr Patel: dr.patel@smiledental.com, manager@gmail.com`,
	}

	var asked []string

	fetch := func(_ context.Context, u string) ([]byte, error) {
		asked = append(asked, u)

		if body, ok := pages[u]; ok {
			return []byte(body), nil
		}

		return nil, errors.New("not found")
	}

	got := findBusinessEmails(context.Background(), "https://smiledental.com/", pageDoc(t, home), []byte(home), fetch)

	assert.Equal(t, []string{
		"info@smiledental.com", "bookings@smiledental.com", "dr.patel@smiledental.com",
		"studio@agency.io", "manager@gmail.com",
	}, got)
	assert.ElementsMatch(t, []string{"https://smiledental.com/contact-us", "https://smiledental.com/our-team"}, asked)
}

func TestFindBusinessEmailsTriesTheUsualContactPagesWhenNoneAreLinked(t *testing.T) {
	home := `<html><body><p>Welcome</p></body></html>`

	var asked []string

	fetch := func(_ context.Context, u string) ([]byte, error) {
		asked = append(asked, u)

		if u == "https://smiledental.com/contact" {
			return []byte(`hello@smiledental.com`), nil
		}

		return nil, errors.New("not found")
	}

	got := findBusinessEmails(context.Background(), "https://smiledental.com/", pageDoc(t, home), []byte(home), fetch)

	assert.Equal(t, []string{"hello@smiledental.com"}, got)
	assert.Equal(t, []string{
		"https://smiledental.com/contact", "https://smiledental.com/contact-us",
		"https://smiledental.com/about", "https://smiledental.com/about-us",
	}, asked)
}

func TestFindBusinessEmailsSurvivesAContactPageThatFails(t *testing.T) {
	home := `<html><body><a href="/contact">Contact</a></body></html>`
	fetch := func(context.Context, string) ([]byte, error) { return nil, errors.New("timeout") }

	got := findBusinessEmails(context.Background(), "https://smiledental.com/", pageDoc(t, home), []byte(home), fetch)

	assert.Empty(t, got)
}

func TestSocialProfilesAreNotSearchedForEmails(t *testing.T) {
	for site, want := range map[string]bool{
		"https://www.smiledental.com":           true,
		"https://www.instagram.com/smiledental": false,
		"https://facebook.com/smiledental":      false,
		"https://linktr.ee/smiledental":         false,
		"":                                      false,
	} {
		e := &Entry{WebSite: site}
		assert.Equal(t, want, e.IsWebsiteValidForEmail(), site)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	u, err := url.Parse(raw)
	require.NoError(t, err)

	return u
}

func TestTheEmailJobFetchesTheSiteDirectlyNotThroughTheBrowser(t *testing.T) {
	var asked []string

	job := NewEmailJob("parent", &Entry{WebSite: "https://smiledental.com/"})
	job.fetchPage = func(_ context.Context, u string) ([]byte, error) {
		asked = append(asked, u)

		return []byte(`<a href="mailto:info@smiledental.com">mail</a>`), nil
	}

	// A nil page: the browser (and its proxy) is never touched.
	resp := job.BrowserActions(context.Background(), nil)

	require.NoError(t, resp.Error)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, string(resp.Body), "info@smiledental.com")
	assert.Equal(t, []string{"https://smiledental.com/"}, asked)

	job.fetchPage = func(context.Context, string) ([]byte, error) { return nil, errors.New("HTTP 403") }
	assert.Error(t, job.BrowserActions(context.Background(), nil).Error)
}

func TestFindBusinessEmailsWithNoContactPagesReadsOnlyTheFrontPage(t *testing.T) {
	front := `<a href="/contact">Contact</a> <a href="mailto:front@site.com">x</a>`

	var asked []string

	got := findBusinessEmailsOpts(context.Background(), "https://site.com/", pageDoc(t, front), []byte(front),
		func(_ context.Context, u string) ([]byte, error) {
			asked = append(asked, u)

			return []byte(`contact@site.com`), nil
		}, emailFindOptions{})

	assert.Equal(t, []string{"front@site.com"}, got)
	assert.Empty(t, asked, "no page beyond the front page, and no guessed /contact either")
}

func TestFindBusinessEmailsWaitsBetweenContactPages(t *testing.T) {
	front := `<a href="/contact">Contact</a> <a href="/about">About</a>`

	var waits []time.Duration

	findBusinessEmailsOpts(context.Background(), "https://site.com/", pageDoc(t, front), []byte(front),
		func(context.Context, string) ([]byte, error) { return []byte("x"), nil },
		emailFindOptions{MaxContactPages: 2, Delay: 3 * time.Second, Sleep: func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)

			return nil
		}})

	assert.Equal(t, []time.Duration{3 * time.Second, 3 * time.Second}, waits)
}

func TestSiteFetcherCanRefuseToFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/home", http.StatusMovedPermanently)

			return
		}

		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<a href="mailto:a@b.com">x</a>`))
	}))
	defer srv.Close()

	body, err := newSiteFetcher(time.Second, true)(context.Background(), srv.URL+"/")
	require.NoError(t, err)
	assert.Contains(t, string(body), "a@b.com")

	_, err = newSiteFetcher(time.Second, false)(context.Background(), srv.URL+"/")
	require.Error(t, err)
	assert.Equal(t, "HTTP 301", err.Error())
	assert.Equal(t, "redirect", fetchFailReason(err))
}

func TestFetchFailReason(t *testing.T) {
	for err, want := range map[error]string{
		errors.New("HTTP 404"):                    "http_4xx",
		errors.New("HTTP 503"):                    "http_5xx",
		errors.New("HTTP 302"):                    "redirect",
		errors.New("not a page (image/png)"):      "not_html",
		context.DeadlineExceeded:                  "timeout",
		errors.New("dial tcp: no such host"):      "other",
		errors.New("stopped: too many redirects"): "redirect",
	} {
		assert.Equal(t, want, fetchFailReason(err), err.Error())
	}

	assert.Empty(t, fetchFailReason(nil))
}
