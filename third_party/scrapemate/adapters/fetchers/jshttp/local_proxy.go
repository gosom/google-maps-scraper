package jshttp

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

type ProxyType int

const (
	ProxyTypeHTTP ProxyType = iota
	ProxyTypeSOCKS5
)

type AuthProxy struct {
	server     *http.Server
	port       int
	upstream   *url.URL
	auth       string
	proxyType  ProxyType
	client     *http.Client
	socks5Auth *proxy.Auth
	bufferPool sync.Pool
	logger     *log.Logger
	shutdownCh chan struct{}
	once       sync.Once
}

// HTTPClient returns a client configured to use this proxy
func (p *AuthProxy) HTTPClient() *http.Client {
	proxyURL, _ := url.Parse(fmt.Sprintf("http://localhost:%d", p.port))

	return &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxyURL),
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		Timeout: 120 * time.Second,
	}
}

// Port returns the port number the proxy is listening on
func (p *AuthProxy) Port() int {
	return p.port
}

func (p *AuthProxy) Address() string {
	return fmt.Sprintf("localhost:%d", p.port)
}

func createHTTPProxyClient(upstream *url.URL) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyURL(upstream),
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableKeepAlives:     false,
		},
		Timeout: 120 * time.Second,
	}
}

func createSOCKS5ProxyClient(upstream *url.URL, auth *proxy.Auth) *http.Client {
	dialer, err := proxy.SOCKS5("tcp", upstream.Host, auth, proxy.Direct)
	if err != nil {
		return &http.Client{Timeout: 120 * time.Second}
	}

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   false,
	}

	if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
		transport.DialContext = contextDialer.DialContext
	} else {
		transport.DialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}
	}

	return &http.Client{
		Transport: transport,
		Timeout:   120 * time.Second,
	}
}

// StartAuthProxy starts an HTTP proxy that authenticates against an upstream proxy (HTTP or SOCKS5)
func StartAuthProxy(proxyURL, username, password string) (*AuthProxy, error) {
	if proxyURL == "" {
		return nil, fmt.Errorf("proxy URL cannot be empty")
	}

	if username == "" || password == "" {
		return nil, fmt.Errorf("username and password are required")
	}

	upstreamURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %v", err)
	}

	var proxyType ProxyType

	switch upstreamURL.Scheme {
	case "http", "https":
		proxyType = ProxyTypeHTTP
	case "socks5", "socks5h":
		proxyType = ProxyTypeSOCKS5
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s (supported: http, https, socks5, socks5h)", upstreamURL.Scheme)
	}

	upstream := *upstreamURL
	if proxyType == ProxyTypeHTTP {
		upstream.User = url.UserPassword(username, password)
	}

	authProxy := &AuthProxy{
		upstream:   &upstream,
		auth:       base64.StdEncoding.EncodeToString([]byte(username + ":" + password)),
		proxyType:  proxyType,
		shutdownCh: make(chan struct{}),
		logger:     log.New(os.Stderr, "[AuthProxy] ", log.LstdFlags),
	}

	if proxyType == ProxyTypeSOCKS5 {
		authProxy.socks5Auth = &proxy.Auth{
			User:     username,
			Password: password,
		}
	}

	if proxyType == ProxyTypeSOCKS5 {
		authProxy.client = createSOCKS5ProxyClient(&upstream, authProxy.socks5Auth)
	} else {
		authProxy.client = createHTTPProxyClient(&upstream)
	}

	authProxy.bufferPool = sync.Pool{
		New: func() any {
			return make([]byte, 32*1024)
		},
	}

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, fmt.Errorf("failed to find free port: %v", err)
	}

	authProxy.port = listener.Addr().(*net.TCPAddr).Port //nolint:errcheck // always ok

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB max

		if r.Method == http.MethodConnect {
			authProxy.handleConnect(w, r)
		} else {
			authProxy.handleHTTP(w, r)
		}
	})

	authProxy.server = &http.Server{
		Handler:        handler,
		ReadTimeout:    60 * time.Second,
		WriteTimeout:   60 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	go func() {
		if err := authProxy.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			authProxy.logger.Printf("Server error: %v", err)
		}
	}()

	return authProxy, nil
}

func (p *AuthProxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Host == "" && r.URL.Scheme == "" {
		r.URL.Scheme = "http"

		if r.TLS != nil {
			r.URL.Scheme = "https"
		}

		r.URL.Host = r.Host
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusBadRequest)

		return
	}

	req.Header = r.Header.Clone()

	for _, h := range []string{"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailers", "Transfer-Encoding", "Upgrade"} {
		req.Header.Del(h)
	}

	if p.proxyType == ProxyTypeHTTP {
		req.Header.Set("Proxy-Authorization", "Basic "+p.auth)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		p.logger.Printf("HTTP request failed: %v", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)

		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(resp.StatusCode)

	buf := p.bufferPool.Get().([]byte) //nolint:errcheck // false positive
	defer p.bufferPool.Put(buf)        //nolint:staticcheck // false positive

	if _, err := io.CopyBuffer(w, resp.Body, buf); err != nil {
		p.logger.Printf("Failed to copy response body: %v", err)
	}
}

func (p *AuthProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	switch p.proxyType {
	case ProxyTypeHTTP:
		p.handleHTTPConnect(w, r)
	case ProxyTypeSOCKS5:
		p.handleSOCKS5Connect(w, r)
	}
}

func (p *AuthProxy) handleHTTPConnect(w http.ResponseWriter, r *http.Request) {
	if r.Host == "" {
		http.Error(w, "Missing host in CONNECT request", http.StatusBadRequest)
		return
	}

	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}

	if err = p.checkPrivateNetwork(host); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var d net.Dialer

	upstreamConn, err := d.DialContext(ctx, "tcp", p.upstream.Host)
	if err != nil {
		p.logger.Printf("Failed to connect to upstream: %v", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)

		return
	}

	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n",
		r.Host, r.Host, p.auth)
	if _, err = upstreamConn.Write([]byte(connectReq)); err != nil {
		p.logger.Printf("Failed to send CONNECT request: %v", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		upstreamConn.Close()

		return
	}

	resp, err := http.ReadResponse(bufio.NewReader(upstreamConn), r)
	if err != nil {
		p.logger.Printf("Failed to parse CONNECT response: %v", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		upstreamConn.Close()

		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		p.logger.Printf("Upstream proxy rejected CONNECT: %s", resp.Status)
		http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
		upstreamConn.Close()

		return
	}

	p.tunnelConnection(w, r, upstreamConn)
}

func (p *AuthProxy) handleSOCKS5Connect(w http.ResponseWriter, r *http.Request) {
	if r.Host == "" {
		http.Error(w, "Missing host in CONNECT request", http.StatusBadRequest)
		return
	}

	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}

	if p.upstream.Scheme != "socks5h" {
		if err = p.checkPrivateNetwork(host); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)

			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	dialer, err := proxy.SOCKS5("tcp", p.upstream.Host, p.socks5Auth, proxy.Direct)
	if err != nil {
		p.logger.Printf("Failed to create SOCKS5 dialer: %v", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)

		return
	}

	var upstreamConn net.Conn
	if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
		upstreamConn, err = contextDialer.DialContext(ctx, "tcp", r.Host)
	} else {
		upstreamConn, err = dialer.Dial("tcp", r.Host)
	}

	if err != nil {
		p.logger.Printf("Failed to connect through SOCKS5 proxy: %v", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)

		return
	}

	p.tunnelConnection(w, r, upstreamConn)
}

func (p *AuthProxy) checkPrivateNetwork(host string) error {
	ips, err := net.LookupIP(host)
	if err == nil {
		for _, ip := range ips {
			if ip.IsLoopback() || ip.IsPrivate() {
				return fmt.Errorf("connection to private networks not allowed")
			}
		}
	}

	return nil
}

func (p *AuthProxy) tunnelConnection(w http.ResponseWriter, _ *http.Request, upstreamConn net.Conn) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Connection hijacking not supported", http.StatusInternalServerError)
		upstreamConn.Close()

		return
	}

	clientConn, _, err := hj.Hijack()
	if err != nil {
		p.logger.Printf("Failed to hijack client connection: %v", err)
		upstreamConn.Close()

		return
	}

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	if tcpConn, ok := clientConn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}

	if tcpConn, ok := upstreamConn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}

	var wg sync.WaitGroup

	wg.Add(2)

	// Client -> Upstream
	go func() {
		defer wg.Done()

		buf := p.bufferPool.Get().([]byte) //nolint:errcheck // false positive
		defer p.bufferPool.Put(buf)        //nolint:staticcheck // false positive

		_, err := io.CopyBuffer(upstreamConn, clientConn, buf)
		if err != nil && !isExpectedNetworkError(err) {
			p.logger.Printf("client->upstream error: %v", err)
		}

		upstreamConn.Close()
	}()

	// Upstream -> Client
	go func() {
		defer wg.Done()

		buf := p.bufferPool.Get().([]byte) //nolint:errcheck // false positive
		defer p.bufferPool.Put(buf)        //nolint:staticcheck // false positive

		_, err := io.CopyBuffer(clientConn, upstreamConn, buf)
		if err != nil && !isExpectedNetworkError(err) {
			p.logger.Printf("client->upstream error: %v", err)
		}

		clientConn.Close()
	}()

	wg.Wait()
}

func (p *AuthProxy) SetLogger(logger *log.Logger) {
	if logger != nil {
		p.logger = logger
	} else {
		p.logger = log.New(io.Discard, "[AuthProxy] ", log.LstdFlags)
	}
}

func (p *AuthProxy) Close() error {
	var err error

	p.once.Do(func() {
		close(p.shutdownCh)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err = p.server.Shutdown(ctx)
		if err == context.DeadlineExceeded {
			err = p.server.Close()
		}
	})

	return err
}

// ProxyType returns the type of upstream proxy being used
func (p *AuthProxy) ProxyType() ProxyType {
	return p.proxyType
}

// ProxyTypeString returns a string representation of the proxy type
func (p *AuthProxy) ProxyTypeString() string {
	switch p.proxyType {
	case ProxyTypeHTTP:
		return "HTTP"
	case ProxyTypeSOCKS5:
		return "SOCKS5"
	default:
		return "Unknown"
	}
}

func isExpectedNetworkError(err error) bool {
	if err == nil || err == io.EOF {
		return true
	}

	errStr := err.Error()

	return strings.Contains(errStr, "use of closed network connection") ||
		strings.Contains(errStr, "connection reset by peer") ||
		strings.Contains(errStr, "broken pipe")
}
