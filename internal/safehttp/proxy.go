package safehttp

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
)

func (t *publicTransport) dialHTTPTunnel(ctx context.Context, network, target string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	port := t.proxyURL.Port()
	if port == "" {
		port = "80"
		if t.proxyURL.Scheme == schemeHTTPS {
			port = "443"
		}
	}

	conn, err := t.dial(ctx, network, net.JoinHostPort(t.proxyURL.Hostname(), port))
	if err != nil {
		return nil, err
	}

	// Cancel blocked proxy handshakes as well as the initial TCP dial.
	raw := conn

	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()

	if t.proxyURL.Scheme == schemeHTTPS {
		secure := tls.Client(conn, &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: t.proxyURL.Hostname(),
		})
		if err := secure.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("email proxy TLS handshake: %w", err)
		}

		conn = secure
	}

	tunnel, err := t.connectTunnel(ctx, conn, target)
	if err != nil {
		conn.Close()
		return nil, err
	}

	return tunnel, nil
}

func (t *publicTransport) connectTunnel(ctx context.Context, conn net.Conn, target string) (net.Conn, error) {
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: target},
		Host:   target,
		Header: make(http.Header),
	}

	if credentials := t.proxyURL.User; credentials != nil {
		password, _ := credentials.Password()
		token := base64.StdEncoding.EncodeToString([]byte(credentials.Username() + ":" + password))
		req.Header.Set("Proxy-Authorization", "Basic "+token)
	}

	if err := req.WithContext(ctx).Write(conn); err != nil {
		return nil, fmt.Errorf("write email proxy CONNECT: %w", err)
	}

	limited := &io.LimitedReader{R: conn, N: maxResponseHeaderBytes}
	reader := bufio.NewReader(limited)
	// The successful CONNECT body is the tunnel; its connection is owned and
	// closed by the caller's HTTP transport, not as a separate response body.
	resp, err := http.ReadResponse(reader, req) //nolint:bodyclose // Closing this body would close the tunnel.
	if err != nil {
		return nil, fmt.Errorf("read email proxy CONNECT: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("email proxy CONNECT returned status %d", resp.StatusCode)
	}

	if limited.N == 0 {
		return nil, errors.New("email proxy response headers exceed limit")
	}

	// Preserve any buffered tunnel bytes without retaining the header-size cap
	// on the response body (the email fetcher enforces its own body limit).
	limited.N = math.MaxInt64

	return &bufferedConn{Conn: conn, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(data []byte) (int, error) {
	return c.reader.Read(data)
}
