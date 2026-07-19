package proxy

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/gosom/scrapemate"
)

type Rotator struct {
	proxies []scrapemate.Proxy
	current uint32
	cache   sync.Map
}

func New(proxies []string) *Rotator {
	if len(proxies) == 0 {
		panic("no proxies provided")
	}

	plist := make([]scrapemate.Proxy, len(proxies))

	for i := range proxies {
		p, err := scrapemate.NewProxy(proxies[i])
		if err != nil {
			panic(err)
		}

		plist[i] = p
	}

	return &Rotator{
		proxies: plist,
		current: 0,
	}
}

func (pr *Rotator) Proxies() []string {
	if pr == nil {
		return nil
	}

	ans := make([]string, len(pr.proxies))
	for i := range pr.proxies {
		ans[i] = pr.proxies[i].FullURL()
	}

	return ans
}

func (pr *Rotator) Next() scrapemate.Proxy {
	current := atomic.AddUint32(&pr.current, 1) - 1

	p := pr.proxies[current%uint32(len(pr.proxies))]

	return p
}

func (pr *Rotator) RoundTrip(req *http.Request) (*http.Response, error) {
	next := pr.Next()

	transport, ok := pr.cache.Load(next.URL)
	if !ok {
		proxyURL, err := url.Parse(next.URL)
		if err != nil {
			return nil, fmt.Errorf("error parsing proxy URL for %s: %v", next.URL, err)
		}

		if next.Username != "" && next.Password != "" {
			proxyURL.User = url.UserPassword(next.Username, next.Password)
		}

		fmt.Printf("using proxy: %s\n", proxyURL.String())

		transport = &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		}

		pr.cache.Store(next.URL, transport)
	}

	tr, ok := transport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("error casting transport for proxy %s", next.URL)
	}

	return tr.RoundTrip(req)
}
