package gmaps

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/gosom/scrapemate"

	"github.com/gosom/google-maps-scraper/internal/safehttp"
)

const (
	maxEmailPageBytes = 2 << 20
	maxEmailRedirects = 10
)

type emailProxiesKey struct{}

// WithEmailProxies configures email requests with the same explicit proxy list
// as the scraper. It is scoped to a run, including jobs restored from a queue.
func WithEmailProxies(ctx context.Context, proxies []string) context.Context {
	return context.WithValue(ctx, emailProxiesKey{}, slices.Clone(proxies))
}

// BrowserActions fetches email pages through a restricted HTTP transport.
// Website scripts and subresources must not bypass the destination checks.
func (j *EmailExtractJob) BrowserActions(ctx context.Context, _ scrapemate.BrowserPage) scrapemate.Response {
	proxies, _ := ctx.Value(emailProxiesKey{}).([]string)

	client, err := safehttp.NewClient(proxies, contactPageTimeout)
	if err != nil {
		return scrapemate.Response{Error: err}
	}

	client.CheckRedirect = j.checkEmailRedirect

	return j.fetchEmailPage(ctx, client)
}

func (j *EmailExtractJob) checkEmailRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxEmailRedirects {
		return errors.New("too many email page redirects")
	}

	if len(via) > 0 && via[len(via)-1].URL.Scheme == urlSchemeHTTPS && req.URL.Scheme != urlSchemeHTTPS {
		return errors.New("email page redirect downgrades HTTPS")
	}

	if j.IsContactPage && !sameContactSite(j.ContactOrigin, req.URL) {
		return errors.New("contact page redirect leaves the website")
	}

	return nil
}

func (j *EmailExtractJob) fetchEmailPage(ctx context.Context, client *http.Client) scrapemate.Response {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.GetFullURL(), http.NoBody)
	if err != nil {
		return scrapemate.Response{Error: err}
	}

	// Validate the initial continuation too, including restored queue state.
	if j.IsContactPage && !sameContactSite(j.ContactOrigin, req.URL) {
		return scrapemate.Response{Error: errors.New("contact page leaves the website")}
	}

	resp, err := client.Do(req)
	if err != nil {
		return scrapemate.Response{Error: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxEmailPageBytes+1))
	if len(body) > maxEmailPageBytes {
		body = nil
		err = errors.New("email page exceeds size limit")
	}

	return scrapemate.Response{
		URL:        resp.Request.URL.String(),
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       body,
		Error:      err,
	}
}
