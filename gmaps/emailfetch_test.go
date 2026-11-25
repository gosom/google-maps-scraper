//nolint:testpackage // Exercise redirect enforcement and fetch bounds before processing.
package gmaps

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEmailRedirectRejectedBeforeRequest(t *testing.T) {
	t.Parallel()

	var unrelatedHits atomic.Int32

	unrelated := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		unrelatedHits.Add(1)
	}))
	t.Cleanup(unrelated.Close)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, unrelated.URL, http.StatusFound)
	}))
	t.Cleanup(server.Close)

	job := NewEmailJob("place", &Entry{WebSite: server.URL})
	job.IsContactPage = true

	var err error

	job.ContactOrigin, err = url.Parse(server.URL)
	require.NoError(t, err)

	client := server.Client()
	client.CheckRedirect = job.checkEmailRedirect
	resp := job.fetchEmailPage(t.Context(), client)
	require.ErrorContains(t, resp.Error, "leaves the website")
	require.Zero(t, unrelatedHits.Load())
}

func TestEmailBrowserActionsBlockPrivateHomepage(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(server.Close)
	job := NewEmailJob("place", &Entry{WebSite: server.URL})
	resp := job.BrowserActions(t.Context(), nil)
	require.ErrorContains(t, resp.Error, "non-public")
	require.Zero(t, hits.Load())
	result, next, err := job.Process(t.Context(), &resp)
	require.NoError(t, err)
	require.Same(t, job.Entry, result)
	require.Empty(t, next)
	require.True(t, job.UseInResults())
}

func TestEmailFetchBoundsAndRedirects(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		handler   http.HandlerFunc
		wantError string
	}{
		{name: "oversized body", handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("x", maxEmailPageBytes+1))
		}, wantError: "size limit"},
		{name: "redirect loop", handler: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/contact", http.StatusFound)
		}, wantError: "too many"},
		{name: "same site redirect", handler: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				http.Redirect(w, r, "/contact", http.StatusFound)
				return
			}

			_, _ = io.WriteString(w, "contact@example.org")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(tc.handler)
			t.Cleanup(server.Close)
			job := NewEmailJob("place", &Entry{WebSite: server.URL})
			client := server.Client()
			client.CheckRedirect = job.checkEmailRedirect

			resp := job.fetchEmailPage(t.Context(), client)
			if tc.wantError != "" {
				require.ErrorContains(t, resp.Error, tc.wantError)
			} else {
				require.NoError(t, resp.Error)
				require.Equal(t, server.URL+"/contact", resp.URL)
				require.Equal(t, "contact@example.org", string(resp.Body))
			}
		})
	}
}

func TestEmailFetchCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	job := NewEmailJob("place", &Entry{WebSite: server.URL})

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	resp := job.fetchEmailPage(ctx, server.Client())
	require.ErrorIs(t, resp.Error, context.DeadlineExceeded)
}

func TestEmailRedirectRejectsDowngrade(t *testing.T) {
	t.Parallel()

	job := NewEmailJob("place", &Entry{WebSite: "https://example.org"})
	original, err := http.NewRequestWithContext(t.Context(), http.MethodGet, job.URL, http.NoBody)
	require.NoError(t, err)
	target, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.org/contact", http.NoBody)
	require.NoError(t, err)
	require.ErrorContains(t, job.checkEmailRedirect(target, []*http.Request{original}), "downgrades HTTPS")
}

func TestEmailProxyConfigurationFailsClosed(t *testing.T) {
	t.Parallel()

	job := NewEmailJob("place", &Entry{WebSite: "https://example.org"})
	proxies := []string{"ftp://proxy.example.org"}
	ctx := WithEmailProxies(t.Context(), proxies)
	proxies[0] = "http://other.example.org"
	resp := job.BrowserActions(ctx, nil)
	require.ErrorContains(t, resp.Error, "unsupported email proxy scheme")
	require.False(t, errors.Is(resp.Error, context.Canceled))
}

func TestEmailBrowserActionsBlockProxyRedirect(t *testing.T) {
	t.Parallel()

	for _, contact := range []bool{false, true} {
		t.Run(map[bool]string{false: "homepage", true: "contact"}[contact], func(t *testing.T) {
			t.Parallel()

			var internalHits atomic.Int32

			internal := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { internalHits.Add(1) }))
			t.Cleanup(internal.Close)

			var tunnels atomic.Int32

			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tunnels.Add(1)

				if r.Method != http.MethodConnect || r.Host != "93.184.216.34:80" {
					t.Error("proxy must receive pinned CONNECT")
					return
				}

				hijacker, ok := w.(http.Hijacker)
				if !ok {
					t.Error("proxy cannot hijack connection")
					return
				}

				conn, rw, err := hijacker.Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()

				if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Error(err)
					return
				}

				if _, err := rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
					t.Error(err)
					return
				}

				if err := rw.Flush(); err != nil {
					t.Error(err)
					return
				}

				inner, err := http.ReadRequest(rw.Reader)
				if err != nil {
					t.Error(err)
					return
				}
				defer inner.Body.Close()

				if _, err := rw.WriteString("HTTP/1.1 302 Found\r\nContent-Length: 0\r\nLocation: " + internal.URL + "\r\n\r\n"); err != nil {
					t.Error(err)
					return
				}

				if err := rw.Flush(); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(proxy.Close)

			job := NewEmailJob("place", &Entry{WebSite: "http://93.184.216.34"})
			job.IsContactPage = contact

			var err error

			job.ContactOrigin, err = url.Parse(job.URL)
			require.NoError(t, err)
			resp := job.BrowserActions(WithEmailProxies(t.Context(), []string{proxy.URL}), nil)
			require.Error(t, resp.Error)
			require.Zero(t, internalHits.Load())
			require.EqualValues(t, 1, tunnels.Load())
		})
	}
}
