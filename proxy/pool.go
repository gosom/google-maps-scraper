package proxy

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"sync"
	"time"
)

// Pool manages multiple proxy servers with load balancing
type Pool struct {
	proxies        []*WebshareProxy
	servers        []*Server
	portStart      int
	portEnd        int
	currentPort    int
	nextProxyIndex int // For round-robin selection
	mu             sync.RWMutex
	blocked        map[string]time.Time // Track blocked proxies
}

// NewPool creates a new proxy pool
func NewPool(proxyURLs []string, portStart, portEnd int) (*Pool, error) {
	if len(proxyURLs) == 0 {
		return nil, fmt.Errorf("no proxy URLs provided")
	}

	proxies := make([]*WebshareProxy, 0, len(proxyURLs))

	for i, proxyURL := range proxyURLs {
		parsed, err := parseProxyURL(proxyURL)
		if err != nil {
			log.Printf("⚠️ Skipping invalid proxy URL %d: %s (%v)", i+1, proxyURL, err)
			continue
		}
		proxies = append(proxies, parsed)
		log.Printf("✅ Added proxy %d: %s:%s", len(proxies), parsed.Address, parsed.Port)
	}

	if len(proxies) == 0 {
		return nil, fmt.Errorf("no valid proxy URLs provided")
	}

	log.Printf("🔧 Configured %d proxies in pool", len(proxies))
	return &Pool{
		proxies:     proxies,
		portStart:   portStart,
		portEnd:     portEnd,
		currentPort: portStart,
		blocked:     make(map[string]time.Time),
	}, nil
}

// GetServerForJob returns a dedicated proxy server for a job
func (p *Pool) GetServerForJob(jobID string) (*Server, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	log.Printf("DEBUG: Pool - Job %s requesting proxy from %d available proxies", jobID, len(p.proxies))

	// Find an available proxy using round-robin (not blocked)
	var availableProxy *WebshareProxy
	startIndex := p.nextProxyIndex

	for attempt := 0; attempt < len(p.proxies); attempt++ {
		index := (startIndex + attempt) % len(p.proxies)
		proxy := p.proxies[index]
		proxyKey := fmt.Sprintf("%s:%s", proxy.Address, proxy.Port)

		if blockedTime, isBlocked := p.blocked[proxyKey]; isBlocked {
			// Check if block has expired (5 minutes)
			if time.Since(blockedTime) > 5*time.Minute {
				delete(p.blocked, proxyKey)
				log.Printf("🔄 Proxy %s unblocked after timeout", proxyKey)
			} else {
				log.Printf("DEBUG: Pool - Proxy %d (%s) is blocked, skipping", index+1, proxyKey)
				continue
			}
		}

		availableProxy = proxy
		p.nextProxyIndex = (index + 1) % len(p.proxies) // Move to next proxy for next job
		log.Printf("DEBUG: Pool - Selected proxy %d (%s) for job %s (round-robin)", index+1, proxyKey, jobID)
		break
	}

	if availableProxy == nil {
		log.Printf("❌ Pool - No available proxies for job %s (all %d proxies blocked)", jobID, len(p.proxies))
		return nil, fmt.Errorf("no available proxies (all blocked)")
	}

	// Find an available port
	port := p.findAvailablePort()
	if port == -1 {
		return nil, fmt.Errorf("no available ports in range %d-%d", p.portStart, p.portEnd)
	}

	// Create server for this specific proxy
	server, err := NewServerFromProxy(availableProxy, port)
	if err != nil {
		return nil, fmt.Errorf("failed to create proxy server: %w", err)
	}

	if err := server.Start(); err != nil {
		return nil, fmt.Errorf("failed to start proxy server: %w", err)
	}

	log.Printf("🎯 Job %s assigned proxy %s:%s on port %d",
		jobID, availableProxy.Address, availableProxy.Port, port)

	return server, nil
}

// MarkProxyBlocked marks a proxy as blocked
func (p *Pool) MarkProxyBlocked(proxy *WebshareProxy) {
	p.mu.Lock()
	defer p.mu.Unlock()

	proxyKey := fmt.Sprintf("%s:%s", proxy.Address, proxy.Port)
	p.blocked[proxyKey] = time.Now()
	log.Printf("🚫 Proxy %s marked as blocked", proxyKey)
}

// findAvailablePort finds an available port in the range
func (p *Pool) findAvailablePort() int {
	for port := p.currentPort; port <= p.portEnd; port++ {
		if p.isPortAvailable(port) {
			p.currentPort = port + 1
			return port
		}
	}

	// Reset and try again
	for port := p.portStart; port < p.currentPort; port++ {
		if p.isPortAvailable(port) {
			p.currentPort = port + 1
			return port
		}
	}

	return -1
}

// isPortAvailable checks if a port is available
func (p *Pool) isPortAvailable(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

// GetStats returns pool statistics
func (p *Pool) GetStats() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return map[string]interface{}{
		"total_proxies":     len(p.proxies),
		"blocked_proxies":   len(p.blocked),
		"available_proxies": len(p.proxies) - len(p.blocked),
		"port_range":        fmt.Sprintf("%d-%d", p.portStart, p.portEnd),
	}
}

// parseProxyURL parses a proxy URL into WebshareProxy
func parseProxyURL(proxyURL string) (*WebshareProxy, error) {
	parsed, err := url.Parse(proxyURL)
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

	return &WebshareProxy{
		Address:  host,
		Port:     port,
		Username: username,
		Password: password,
	}, nil
}
