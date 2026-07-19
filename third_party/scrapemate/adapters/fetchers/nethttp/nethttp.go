package nethttp

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"sync"

	"github.com/gosom/scrapemate"
)

var _ scrapemate.HTTPFetcher = (*httpFetch)(nil)

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

func New(netClient HTTPClient) scrapemate.HTTPFetcher {
	return &httpFetch{
		netClient: netClient,
	}
}

type httpFetch struct {
	netClient HTTPClient
}

func (o *httpFetch) Close() error {
	return nil
}

func (o *httpFetch) Fetch(ctx context.Context, job scrapemate.IJob) scrapemate.Response {
	u := job.GetFullURL()
	reqBody := getBuffer()

	defer putBuffer(reqBody)

	if len(job.GetBody()) > 0 {
		reqBody.Write(job.GetBody())
	}

	var ans scrapemate.Response

	req, err := http.NewRequestWithContext(ctx, job.GetMethod(), u, reqBody)
	if err != nil {
		ans.Error = err
		return ans
	}

	for k, v := range job.GetHeaders() {
		req.Header.Add(k, v)
	}

	resp, err := o.netClient.Do(req)
	if err != nil {
		ans.Error = err
		return ans
	}

	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	ans.StatusCode = resp.StatusCode
	ans.Headers = http.Header{}

	for k, v := range resp.Header {
		ans.Headers[k] = v
	}

	var reader io.ReadCloser

	switch resp.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err = gzip.NewReader(resp.Body)
		if err != nil {
			ans.Error = err
			return ans
		}
		defer reader.Close()
	default:
		reader = resp.Body
	}

	ans.Body, ans.Error = io.ReadAll(reader)
	ans.URL = resp.Request.URL.String()

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
