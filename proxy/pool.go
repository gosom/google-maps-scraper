package proxy

import (
	"fmt"
	"log/slog"
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
	logger         *slog.Logger
}

// NewPool creates a new proxy pool
func NewPool(proxyURLs []string, portStart, portEnd int, logger *slog.Logger) (*Pool, error) {
	if len(proxyURLs) == 0 {
		return nil, fmt.Errorf("no proxy URLs provided")
	}

	proxies := make([]*WebshareProxy, 0, len(proxyURLs))

	for i, proxyURL := range proxyURLs {
		parsed, err := parseProxyURL(proxyURL)
		if err != nil {
			logger.Warn("skipping_invalid_proxy_url", slog.Int("index", i+1), slog.String("url", sanitizeProxyURL(proxyURL)), slog.Any("error", err))
			continue
		}
		proxies = append(proxies, parsed)
		logger.Info("proxy_added", slog.Int("index", len(proxies)), slog.String("host", parsed.Address), slog.String("port", parsed.Port))
	}

	if len(proxies) == 0 {
		return nil, fmt.Errorf("no valid proxy URLs provided")
	}

	logger.Info("proxies_configured_in_pool", slog.Int("count", len(proxies)))
	return &Pool{
		proxies:     proxies,
		portStart:   portStart,
		portEnd:     portEnd,
		currentPort: portStart,
		blocked:     make(map[string]time.Time),
		logger:      logger,
	}, nil
}

// GetServerForJob returns a dedicated proxy server for a job
func (p *Pool) GetServerForJob(jobID string) (*Server, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.logger.Debug("pool_proxy_request", slog.String("job_id", jobID), slog.Int("available_proxies", len(p.proxies)))

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
				p.logger.Info("proxy_unblocked_after_timeout", slog.String("proxy", proxyKey))
			} else {
				p.logger.Debug("pool_proxy_blocked_skipping", slog.Int("index", index+1), slog.String("proxy", proxyKey))
				continue
			}
		}

		availableProxy = proxy
		p.nextProxyIndex = (index + 1) % len(p.proxies) // Move to next proxy for next job
		p.logger.Debug("pool_proxy_selected", slog.Int("index", index+1), slog.String("proxy", proxyKey), slog.String("job_id", jobID))
		break
	}

	if availableProxy == nil {
		p.logger.Error("pool_no_available_proxies", slog.String("job_id", jobID), slog.Int("total_proxies", len(p.proxies)))
		return nil, fmt.Errorf("no available proxies (all blocked)")
	}

	// Try ports directly, avoiding TOCTOU race by combining port check with server start
	server, port, err := p.tryStartOnAvailablePort(availableProxy)
	if err != nil {
		return nil, err
	}

	p.logger.Info("job_proxy_assigned", slog.String("job_id", jobID), slog.String("host", availableProxy.Address), slog.String("port", availableProxy.Port), slog.Int("local_port", port))

	return server, nil
}

// MarkProxyBlocked marks a proxy as blocked
func (p *Pool) MarkProxyBlocked(proxy *WebshareProxy) {
	p.mu.Lock()
	defer p.mu.Unlock()

	proxyKey := fmt.Sprintf("%s:%s", proxy.Address, proxy.Port)
	p.blocked[proxyKey] = time.Now()
	p.logger.Warn("proxy_marked_blocked", slog.String("proxy", proxyKey))
}

// tryStartOnAvailablePort attempts to create and start a proxy server on each
// port in the range, returning the first one that succeeds. This avoids the
// TOCTOU race of checking port availability separately from binding.
func (p *Pool) tryStartOnAvailablePort(proxy *WebshareProxy) (*Server, int, error) {
	try := func(port int) (*Server, error) {
		server, err := NewServerFromProxy(proxy, port, p.logger)
		if err != nil {
			return nil, err
		}
		if err := server.Start(); err != nil {
			return nil, err
		}
		return server, nil
	}

	// Try from currentPort to portEnd
	for port := p.currentPort; port <= p.portEnd; port++ {
		server, err := try(port)
		if err != nil {
			continue
		}
		p.currentPort = port + 1
		return server, port, nil
	}

	// Wrap around: try from portStart to currentPort
	for port := p.portStart; port < p.currentPort; port++ {
		server, err := try(port)
		if err != nil {
			continue
		}
		p.currentPort = port + 1
		return server, port, nil
	}

	return nil, -1, fmt.Errorf("no available ports in range %d-%d", p.portStart, p.portEnd)
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
