package gmaps

import (
	"context"
	"time"

	"github.com/Noooste/azuretls-client"
)

// rpcTransport makes one request as one identity. Split from `identityClient` so rotation is
// tested without a network, and the real client is tested through a proxy of the test's own.
type rpcTransport interface {
	do(ctx context.Context, id *reviewIdentity, url string) (rpcResponse, error)
}

const reviewHTTPTimeout = 30 * time.Second

// azuretlsTransport sends requests with a browser's TLS fingerprint, through the identity's proxy,
// carrying the identity's cookies. Redirects are not followed: a 302 to /sorry/ must reach
// `classifyRPC` as a redirect, not as whatever page it leads to.
type azuretlsTransport struct{}

func (azuretlsTransport) do(ctx context.Context, id *reviewIdentity, url string) (rpcResponse, error) {
	if id.session == nil {
		s := azuretls.NewSessionWithContext(ctx)
		s.SetTimeout(reviewHTTPTimeout)

		s.UserAgent = id.userAgent
		// An identity without a proxy is the machine's own address (collector v2 without
		// proxies); the review route never makes one.
		if id.proxy != "" {
			if err := s.SetProxy(id.proxy); err != nil {
				s.Close()
				return rpcResponse{}, err
			}
		}

		id.session = s
	}

	resp, err := id.session.Do(&azuretls.Request{
		Method:           "GET",
		Url:              url,
		DisableRedirects: true,
		TimeOut:          reviewHTTPTimeout,
		OrderedHeaders: azuretls.OrderedHeaders{
			{"accept", "*/*"},
			{"accept-language", "en-US,en;q=0.9"},
			{"referer", "https://www.google.com/maps/"},
			{"user-agent", id.userAgent},
		},
	})
	if err != nil {
		return rpcResponse{}, err
	}

	final := resp.Url

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if loc := resp.Header.Get("Location"); loc != "" {
			final = loc
		}
	}

	return rpcResponse{Status: resp.StatusCode, FinalURL: final, Body: resp.Body}, nil
}

func (c *identityClient) fetchPage(ctx context.Context, url string) (rpcResponse, error) {
	return c.tr.do(ctx, c.pool.current(), url)
}
