package proxy

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
)

// Server handles proxy authentication and forwarding with fallback support
type Server struct {
	proxies      []*WebshareProxy
	currentProxy int
	localPort    int
	listener     net.Listener
	running      bool
	wg           sync.WaitGroup
	mu           sync.RWMutex
}

// WebshareProxy represents the upstream proxy configuration
type WebshareProxy struct {
	Address  string
	Port     string
	Username string
	Password string
}

// NewServer creates a new proxy server that forwards to the webshare proxy
func NewServer(webshareURL string, localPort int) (*Server, error) {
	parsed, err := url.Parse(webshareURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}

	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy host:port: %w", err)
	}

	username := ""
	password := ""
	if parsed.User != nil {
		username = parsed.User.Username()
		password, _ = parsed.User.Password()
	}

	return &Server{
		proxies: []*WebshareProxy{{
			Address:  host,
			Port:     port,
			Username: username,
			Password: password,
		}},
		currentProxy: 0,
		localPort:    localPort,
	}, nil
}

// NewServerFromProxy creates a new proxy server from a WebshareProxy
func NewServerFromProxy(proxy *WebshareProxy, localPort int) (*Server, error) {
	return &Server{
		proxies:      []*WebshareProxy{proxy},
		currentProxy: 0,
		localPort:    localPort,
	}, nil
}

// NewServerWithFallback creates a new proxy server with multiple proxies for fallback
func NewServerWithFallback(proxyURLs []string, localPort int) (*Server, error) {
	if len(proxyURLs) == 0 {
		return nil, fmt.Errorf("no proxy URLs provided")
	}

	proxies := make([]*WebshareProxy, 0, len(proxyURLs))

	for i, proxyURL := range proxyURLs {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			log.Printf("⚠️ Skipping invalid proxy URL %d: %s (%v)", i+1, proxyURL, err)
			continue
		}

		host, port, err := net.SplitHostPort(parsed.Host)
		if err != nil {
			log.Printf("⚠️ Skipping invalid proxy host:port %d: %s (%v)", i+1, proxyURL, err)
			continue
		}

		username := ""
		password := ""
		if parsed.User != nil {
			username = parsed.User.Username()
			password, _ = parsed.User.Password()
		}

		proxies = append(proxies, &WebshareProxy{
			Address:  host,
			Port:     port,
			Username: username,
			Password: password,
		})
		log.Printf("✅ Added proxy %d: %s:%s", len(proxies), host, port)
	}

	if len(proxies) == 0 {
		return nil, fmt.Errorf("no valid proxy URLs provided")
	}

	log.Printf("🔧 Configured %d proxies for fallback", len(proxies))
	return &Server{
		proxies:      proxies,
		currentProxy: 0,
		localPort:    localPort,
	}, nil
}

// Start starts the proxy server
func (ps *Server) Start() error {
	var err error
	ps.listener, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", ps.localPort))
	if err != nil {
		return fmt.Errorf("failed to start proxy server: %w", err)
	}

	ps.running = true
	ps.mu.RLock()
	currentProxy := ps.proxies[ps.currentProxy]
	ps.mu.RUnlock()

	log.Printf("🔧 Proxy server started on 127.0.0.1:%d (forwarding to %s:%s)",
		ps.localPort, currentProxy.Address, currentProxy.Port)
	log.Printf("🔄 Fallback enabled with %d proxies", len(ps.proxies))

	ps.wg.Add(1)
	go ps.run()
	return nil
}

// Stop stops the proxy server
func (ps *Server) Stop() {
	ps.running = false
	if ps.listener != nil {
		ps.listener.Close()
	}
	ps.wg.Wait()
	log.Printf("🔧 Proxy server stopped")
}

// GetLocalURL returns the local proxy URL
func (ps *Server) GetLocalURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", ps.localPort)
}

func (ps *Server) run() {
	defer ps.wg.Done()
	for ps.running {
		conn, err := ps.listener.Accept()
		if err != nil {
			if ps.running {
				log.Printf("Proxy accept error: %v", err)
			}
			continue
		}
		go ps.handleConnection(conn)
	}
}

func (ps *Server) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	// Read the request
	reader := bufio.NewReader(clientConn)
	request, err := reader.ReadString('\n')
	if err != nil {
		return
	}

	parts := strings.Fields(request)
	if len(parts) < 3 {
		return
	}

	method := parts[0]
	target := parts[1]

	if method == "CONNECT" {
		ps.handleHTTPS(clientConn, target)
	} else {
		ps.handleHTTP(clientConn, reader, request)
	}
}

func (ps *Server) handleHTTPS(clientConn net.Conn, target string) {
	ps.mu.RLock()
	currentProxy := ps.proxies[ps.currentProxy]
	ps.mu.RUnlock()

	// Try current proxy first
	proxyConn, err := ps.tryConnectToProxy(currentProxy, target)
	if err != nil {
		// Try fallback proxies
		proxyConn, err = ps.tryFallbackProxies(target)
		if err != nil {
			log.Printf("❌ All proxies failed for HTTPS %s: %v", target, err)
			_, _ = clientConn.Write([]byte("HTTP/1.1 500 All proxies failed\r\n\r\n"))
			return
		}
	}
	defer proxyConn.Close()

	// Send CONNECT request to webshare proxy with auth
	auth := base64.StdEncoding.EncodeToString([]byte(currentProxy.Username + ":" + currentProxy.Password))
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\nProxy-Connection: keep-alive\r\n\r\n",
		target, target, auth)

	_, err = proxyConn.Write([]byte(connectReq))
	if err != nil {
		return
	}

	// Read response from proxy
	response := make([]byte, 1024)
	n, err := proxyConn.Read(response)
	if err != nil {
		return
	}

	// Forward response to client
	_, err = clientConn.Write(response[:n])
	if err != nil {
		return
	}

	// Tunnel the data
	ps.tunnelData(clientConn, proxyConn)
}

func (ps *Server) handleHTTP(clientConn net.Conn, reader *bufio.Reader, firstLine string) {
	// Read the full request
	var request strings.Builder
	request.WriteString(firstLine)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		request.WriteString(line)
		if line == "\r\n" {
			break
		}
	}

	ps.mu.RLock()
	currentProxy := ps.proxies[ps.currentProxy]
	ps.mu.RUnlock()

	// Try current proxy first
	proxyConn, err := ps.tryConnectToProxy(currentProxy, "")
	if err != nil {
		// Try fallback proxies
		proxyConn, err = ps.tryFallbackProxies("")
		if err != nil {
			log.Printf("❌ All proxies failed for HTTP: %v", err)
			return
		}
	}
	defer proxyConn.Close()

	// Add proxy authentication to the request
	auth := base64.StdEncoding.EncodeToString([]byte(currentProxy.Username + ":" + currentProxy.Password))
	modifiedRequest := strings.Replace(request.String(), "\r\n\r\n", "\r\nProxy-Authorization: Basic "+auth+"\r\n\r\n", 1)

	// Send request to proxy
	_, err = proxyConn.Write([]byte(modifiedRequest))
	if err != nil {
		return
	}

	// Forward response back to client
	_, _ = io.Copy(clientConn, proxyConn)
}

func (ps *Server) tunnelData(clientConn, proxyConn net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	// Client to proxy
	go func() {
		defer wg.Done()
		_, _ = io.Copy(proxyConn, clientConn)
		proxyConn.Close()
	}()

	// Proxy to client
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, proxyConn)
		clientConn.Close()
	}()

	wg.Wait()
}

// tryConnectToProxy attempts to connect to a specific proxy
func (ps *Server) tryConnectToProxy(proxy *WebshareProxy, target string) (net.Conn, error) {
	address := net.JoinHostPort(proxy.Address, proxy.Port)
	conn, err := net.Dial("tcp", address)
	if err != nil {
		log.Printf("⚠️ Failed to connect to proxy %s:%s: %v", proxy.Address, proxy.Port, err)
		return nil, err
	}
	return conn, nil
}

// tryFallbackProxies tries all available proxies as fallbacks
func (ps *Server) tryFallbackProxies(target string) (net.Conn, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// Try all proxies starting from the next one
	for i := 1; i < len(ps.proxies); i++ {
		nextIndex := (ps.currentProxy + i) % len(ps.proxies)
		proxy := ps.proxies[nextIndex]

		conn, err := ps.tryConnectToProxy(proxy, target)
		if err == nil {
			// Success! Switch to this proxy
			oldProxy := ps.proxies[ps.currentProxy]
			ps.currentProxy = nextIndex
			log.Printf("🔄 Switched to fallback proxy %d: %s:%s (was %s:%s)",
				nextIndex+1, proxy.Address, proxy.Port, oldProxy.Address, oldProxy.Port)
			return conn, nil
		}
	}

	return nil, fmt.Errorf("all %d proxies failed", len(ps.proxies))
}

// GetCurrentProxy returns the currently active proxy
func (ps *Server) GetCurrentProxy() *WebshareProxy {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.proxies[ps.currentProxy]
}

// MarkProxyBlocked marks the current proxy as blocked
func (ps *Server) MarkProxyBlocked() {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	currentProxy := ps.proxies[ps.currentProxy]
	proxyKey := fmt.Sprintf("%s:%s", currentProxy.Address, currentProxy.Port)
	log.Printf("🚫 Marking proxy %s as blocked", proxyKey)

	// This would need to be called from the pool to actually block it
	// For now, just log it
}

// GetProxyCount returns the total number of configured proxies
func (ps *Server) GetProxyCount() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.proxies)
}
