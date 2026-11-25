//nolint:testpackage // Inject DNS and sockets to exercise the production transport without public network access.
package safehttp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublicIP(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "127.0.0.1", "169.254.169.254",
		"172.16.0.1", "192.168.1.1", "100.100.100.200", "168.63.129.16", "192.0.0.1", "192.0.2.1",
		"198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1",
		"255.255.255.255", "::", "::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1",
		"fe80::1", "fc00::1", "ff02::1", "64:ff9b::a00:1", "2002:7f00:1::",
		"2001::1", "2001:db8::1", "3fff::1", "2001:4860:4860::8888%eth0",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			require.False(t, isPublicIP(netip.MustParseAddr(raw)))
		})
	}

	for _, raw := range []string{"93.184.216.34", "8.8.8.8", "::ffff:8.8.8.8", "2001:4860:4860::8888", "2606:4700::1111"} {
		require.True(t, isPublicIP(netip.MustParseAddr(raw)), raw)
	}
}

func TestUnsafeDestinationsNeverDial(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		url       string
		addresses []netip.Addr
	}{
		{name: "loopback", url: "http://127.0.0.1", addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}},
		{name: "metadata", url: "http://metadata.test", addresses: []netip.Addr{netip.MustParseAddr("169.254.169.254")}},
		{name: "mixed answers", url: "https://business.test", addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.0.0.1")}},
		{name: "no addresses", url: "https://business.test"},
		{name: "credentials", url: "https://user@business.test"},
		{name: "file", url: "file:///etc/passwd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			transport := &publicTransport{
				lookup: func(context.Context, string, string) ([]netip.Addr, error) { return tc.addresses, nil },
				dial: func(context.Context, string, string) (net.Conn, error) {
					t.Error("unsafe destination reached the dialer")
					return nil, net.ErrClosed
				},
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tc.url, http.NoBody)
			require.NoError(t, err)

			resp, err := transport.RoundTrip(req)
			if resp != nil {
				resp.Body.Close()
			}

			require.Error(t, err)
			require.Nil(t, resp)
		})
	}
}

func TestPinnedDestinationAndRelativeRedirect(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)

		if r.Host != "business.test" {
			t.Errorf("incorrect Host header: %s", r.Host)
		}

		if r.URL.Path == "/" {
			http.Redirect(w, r, "/contact", http.StatusFound)
			return
		}

		_, _ = io.WriteString(w, "contact@business.test")
	}))
	t.Cleanup(server.Close)

	client := clientForServer(t, server)
	resp, err := client.Get("http://business.test/")
	require.NoError(t, err)

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "contact@business.test", string(body))
	require.Equal(t, "http://business.test/contact", resp.Request.URL.String())
	require.EqualValues(t, 2, hits.Load())
}

func TestRedirectRechecksDNSBeforeDial(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/private", http.StatusFound)
	}))
	t.Cleanup(server.Close)

	client := clientForServer(t, server)
	transport, ok := client.Transport.(*publicTransport)
	require.True(t, ok)

	var lookups atomic.Int32

	transport.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		if lookups.Add(1) == 1 {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}

		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}

	resp, err := client.Get("http://business.test/")
	require.ErrorContains(t, err, "non-public")

	if resp != nil {
		resp.Body.Close()
	}

	require.EqualValues(t, 1, hits.Load())
}

func TestHTTPProxyReceivesPinnedAddress(t *testing.T) {
	t.Parallel()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "93.184.216.34:80" {
			t.Errorf("proxy received URL=%s Host=%s", r.URL, r.Host)
		}

		if r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpwYXNz" {
			t.Error("missing proxy credentials")
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

		if inner.Host != "business.test" || inner.Header.Get("Proxy-Authorization") != "" {
			t.Error("tunnel changed the website host or leaked proxy credentials")
		}

		if _, err := rw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 7\r\n\r\nproxied"); err != nil {
			t.Error(err)
			return
		}

		if err := rw.Flush(); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(proxy.Close)

	client, err := NewClient([]string{strings.Replace(proxy.URL, "://", "://user:pass@", 1)}, time.Second)
	require.NoError(t, err)

	transport, ok := client.Transport.(*publicTransport)
	require.True(t, ok)

	transport.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	resp, err := client.Get("http://business.test/contact")
	require.NoError(t, err)

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "proxied", string(body))
}

func TestTLSUsesWebsiteNameAndRejectsUntrustedCertificate(t *testing.T) {
	t.Parallel()

	sni := make(chan string, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("untrusted TLS endpoint received an HTTP request")
	}))
	server.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
			sni <- info.ServerName
			return nil, nil
		},
	}
	server.StartTLS()
	t.Cleanup(server.Close)

	client := clientForServer(t, server)
	resp, err := client.Get("https://business.test/")
	require.Error(t, err)

	if resp != nil {
		resp.Body.Close()
	}

	require.Equal(t, "business.test", <-sni)
}

func TestInvalidProxyFailsClosed(t *testing.T) {
	t.Parallel()

	for _, proxy := range []string{"http://%zz", "http:///", "file:///tmp", "ftp://proxy.test"} {
		client, err := NewClient([]string{proxy}, time.Second)
		require.Error(t, err)
		require.Nil(t, client)
	}
}

func TestSOCKSProxyReceivesPinnedIP(t *testing.T) {
	t.Parallel()

	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			t.Parallel()

			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() { listener.Close() })

			done := make(chan struct{})
			go func() {
				defer close(done)

				conn, err := listener.Accept()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()

				if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Error(err)
					return
				}

				greeting := make([]byte, 3)
				if _, err := io.ReadFull(conn, greeting); err != nil {
					t.Error(err)
					return
				}

				if _, err := conn.Write([]byte{5, 0}); err != nil {
					t.Error(err)
					return
				}

				request := make([]byte, 10)
				if _, err := io.ReadFull(conn, request); err != nil {
					t.Error(err)
					return
				}

				if !bytes.Equal(request, []byte{5, 1, 0, 1, 93, 184, 216, 34, 0, 80}) {
					t.Errorf("SOCKS must receive a pinned IPv4 address, got %v", request)
					return
				}

				if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
					t.Error(err)
					return
				}

				req, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					t.Error(err)
					return
				}
				defer req.Body.Close()

				if req.Host != "business.test" {
					t.Error("lost website Host header")
				}

				if _, err := io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"); err != nil {
					t.Error(err)
				}
			}()

			client, err := NewClient([]string{scheme + "://" + listener.Addr().String()}, 3*time.Second)
			require.NoError(t, err)

			transport, ok := client.Transport.(*publicTransport)
			require.True(t, ok)

			transport.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}
			resp, err := client.Get("http://business.test/")
			require.NoError(t, err)

			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, "ok", string(body))
			<-done
		})
	}
}

func TestHTTPSProxyRejectsUntrustedCertificate(t *testing.T) {
	t.Parallel()

	proxy := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("untrusted HTTPS proxy received a CONNECT request")
	}))
	t.Cleanup(proxy.Close)
	client, err := NewClient([]string{proxy.URL}, time.Second)
	require.NoError(t, err)

	resp, err := client.Get("https://93.184.216.34/")
	if resp != nil {
		resp.Body.Close()
	}

	require.ErrorContains(t, err, "proxy TLS handshake")
}

func clientForServer(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()

	endpoint, err := url.Parse(server.URL)
	require.NoError(t, err)

	dialer := &net.Dialer{}

	return &http.Client{
		Timeout: time.Second,
		Transport: &publicTransport{
			timeout: time.Second,
			lookup: func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			},
			dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(address)
				if err != nil || host != "93.184.216.34" {
					t.Errorf("dial must use the validated IP, got %q", address)
					return nil, net.ErrClosed
				}

				return dialer.DialContext(ctx, network, endpoint.Host)
			},
		},
	}
}
