package gmaps

import (
	"context"
	"net/url"
	"strings"
	"sync"
)

// googleGetter is how collector v2 asks Google for one page. Split out so the collector is tested
// without a network.
type googleGetter interface {
	get(ctx context.Context, url string) (rpcResponse, error)
	// next moves to the next identity: after every search, and after a refusal.
	next()
	close()
}

// steadyGoogle sends v2's Google requests with a browser's TLS fingerprint, one identity per
// proxy, taking the next identity for each search so the searches are spread evenly over the
// proxies. Without proxies it has one identity on the machine's own address.
type steadyGoogle struct {
	mu  sync.Mutex
	ids []*reviewIdentity
	cur int
	tr  rpcTransport
}

func newSteadyGoogle(proxies []string) (*steadyGoogle, error) {
	g := &steadyGoogle{tr: azuretlsTransport{}}

	if len(proxies) == 0 {
		g.ids = []*reviewIdentity{{userAgent: reviewUserAgents[0]}}

		return g, nil
	}

	pool, err := newIdentityPool(proxies)
	if err != nil {
		return nil, err
	}

	g.ids = pool.ids

	return g, nil
}

func (g *steadyGoogle) get(ctx context.Context, u string) (rpcResponse, error) {
	g.mu.Lock()
	id := g.ids[g.cur]
	g.mu.Unlock()

	return g.tr.do(ctx, id, u)
}

func (g *steadyGoogle) next() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.cur = (g.cur + 1) % len(g.ids)
}

func (g *steadyGoogle) close() {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, id := range g.ids {
		if id.session != nil {
			id.session.Close()
			id.session = nil
		}
	}
}

// googleRefusal is why a Google response is a refusal, or "" when it is not: a 403 or 429, the
// /sorry/ page, or the "unusual traffic" page served with a 200.
func googleRefusal(resp rpcResponse) string {
	if why := placeBlocked(resp.Status, resp.FinalURL); why != "" {
		return why
	}

	if refusedByContent(string(resp.Body)) {
		return "Google's unusual-traffic page"
	}

	return ""
}

// consentWall is a redirect to Google's cookie-consent page, which a signed-out visitor from some
// countries is sent to first. It is not a refusal, and v2 does not click through it.
func consentWall(resp rpcResponse) bool {
	u, err := url.Parse(resp.FinalURL)

	return err == nil && strings.HasPrefix(u.Host, "consent.")
}
