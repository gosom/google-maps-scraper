package stealth

import (
	"bytes"
	"context"
	"net/http"
	"sync"

	"github.com/gosom/scrapemate"

	"github.com/Noooste/azuretls-client"
)

type proxyFetcher interface {
	Next() scrapemate.Proxy
}

type stealthFetch struct {
	browserSettings settings
	rotator         proxyFetcher
}

func New(browser string, rotator proxyFetcher) scrapemate.HTTPFetcher {
	ans := stealthFetch{}

	if browser != "" {
		ans.browserSettings = newSettings(browser)
	}

	if rotator != nil {
		ans.rotator = rotator
	}

	return &ans
}

func (o *stealthFetch) Close() error {
	return nil
}

func (o *stealthFetch) Fetch(ctx context.Context, job scrapemate.IJob) scrapemate.Response {
	u := job.GetFullURL()
	reqBody := getBuffer()

	defer putBuffer(reqBody)

	if len(job.GetBody()) > 0 {
		reqBody.Write(job.GetBody())
	}

	session := azuretls.NewSessionWithContext(ctx)
	defer session.Close()

	if o.rotator != nil {
		session.InsecureSkipVerify = true

		proxy := o.rotator.Next()

		if err := session.SetProxy(proxy.FullURL()); err != nil {
			return scrapemate.Response{
				Error: err,
			}
		}
	}

	session.Browser = o.browserSettings.browser
	session.OrderedHeaders = o.browserSettings.headers

	req := azuretls.Request{
		Method:  job.GetMethod(),
		Url:     u,
		TimeOut: job.GetTimeout(),
	}

	req.SetContext(ctx)

	var ans scrapemate.Response

	resp, err := session.Do(&req)
	if err != nil {
		ans.Error = err

		return ans
	}

	ans.StatusCode = resp.StatusCode
	ans.Headers = http.Header{}

	for k, v := range resp.Header {
		ans.Headers[k] = v
	}

	ans.Body = resp.Body
	ans.URL = u

	return ans
}

var bufferPool = sync.Pool{
	New: func() interface{} {
		return new(bytes.Buffer)
	},
}

func getBuffer() *bytes.Buffer {
	//nolint:errcheck // we don't care about errors here
	b := bufferPool.Get().(*bytes.Buffer)
	b.Reset()

	return b
}

func putBuffer(buf *bytes.Buffer) {
	bufferPool.Put(buf)
}
