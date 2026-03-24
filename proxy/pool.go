package proxy

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// Pool manages multiple proxy servers with load balancing
type Pool struct {
	proxies        []*WebshareProxy
	portStart      int
	portEnd        int
	portPool       chan int      // buffered channel as O(1) port pool
	nextProxyIndex atomic.Int64  // Lock-free round-robin advancement

	// blockMu protects the blocked map (proxy block/unblock decisions).
	blockMu sync.Mutex
	blocked map[string]time.Time

	// activeMu protects activeServers (per-job server lifecycle tracking).
	activeMu      sync.Mutex
	activeServers map[string]*Server // keyed by jobID

	logger *slog.Logger
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

	portCount := portEnd - portStart + 1
	portPool := make(chan int, portCount)
	for port := portStart; port <= portEnd; port++ {
		portPool <- port
	}

	return &Pool{
		proxies:       proxies,
		portStart:     portStart,
		portEnd:       portEnd,
		portPool:      portPool,
		blocked:       make(map[string]time.Time),
		activeServers: make(map[string]*Server),
		logger:        logger,
	}, nil
}

// GetServerForJob returns a dedicated proxy server for a job.
// The proxy selection (round-robin + block check) is done under blockMu,
// while port binding uses a buffered channel pool -- the two never nest,
// so concurrent jobs only serialize on the short critical sections.
func (p *Pool) GetServerForJob(jobID string) (*Server, error) {
	p.logger.Debug("pool_proxy_request", slog.String("job_id", jobID), slog.Int("available_proxies", len(p.proxies)))

	// Step 1: Select a proxy (lock-free index + short blockMu for block check).
	availableProxy, err := p.selectProxy(jobID)
	if err != nil {
		return nil, err
	}

	// Step 2: Bind a port -- I/O happens here, only portMu is held.
	server, port, err := p.tryStartOnAvailablePort(availableProxy)
	if err != nil {
		return nil, err
	}

	// Step 3: Register the server for lifecycle tracking.
	p.activeMu.Lock()
	p.activeServers[jobID] = server
	p.activeMu.Unlock()

	p.logger.Info("job_proxy_assigned", slog.String("job_id", jobID), slog.String("host", availableProxy.Address), slog.String("port", availableProxy.Port), slog.Int("local_port", port))

	return server, nil
}

// ReturnServer stops and removes the proxy server associated with a job.
// Safe to call multiple times or with an unknown jobID.
func (p *Pool) ReturnServer(jobID string) {
	p.activeMu.Lock()
	server, ok := p.activeServers[jobID]
	if ok {
		delete(p.activeServers, jobID)
	}
	p.activeMu.Unlock()

	if ok && server != nil {
		port := server.localPort
		server.Stop()
		p.portPool <- port
		p.logger.Info("job_proxy_returned", slog.String("job_id", jobID))
	}
}

// Close stops all active proxy servers. Call during graceful shutdown.
func (p *Pool) Close() {
	p.activeMu.Lock()
	servers := make(map[string]*Server, len(p.activeServers))
	for k, v := range p.activeServers {
		servers[k] = v
	}
	p.activeServers = make(map[string]*Server)
	p.activeMu.Unlock()

	for jobID, server := range servers {
		if server != nil {
			port := server.localPort
			server.Stop()
			p.portPool <- port
			p.logger.Info("pool_close_stopped_server", slog.String("job_id", jobID))
		}
	}
	p.logger.Info("pool_closed", slog.Int("servers_stopped", len(servers)))
}

// selectProxy picks the next non-blocked proxy using atomic round-robin.
func (p *Pool) selectProxy(jobID string) (*WebshareProxy, error) {
	numProxies := len(p.proxies)
	startIndex := int(p.nextProxyIndex.Add(1) - 1) // Atomically claim an index

	p.blockMu.Lock()
	defer p.blockMu.Unlock()

	for attempt := 0; attempt < numProxies; attempt++ {
		index := (startIndex + attempt) % numProxies
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

		p.logger.Debug("pool_proxy_selected", slog.Int("index", index+1), slog.String("proxy", proxyKey), slog.String("job_id", jobID))
		return proxy, nil
	}

	p.logger.Error("pool_no_available_proxies", slog.String("job_id", jobID), slog.Int("total_proxies", numProxies))
	return nil, fmt.Errorf("no available proxies (all blocked)")
}

// MarkProxyBlocked marks a proxy as blocked
func (p *Pool) MarkProxyBlocked(proxy *WebshareProxy) {
	p.blockMu.Lock()
	defer p.blockMu.Unlock()

	proxyKey := fmt.Sprintf("%s:%s", proxy.Address, proxy.Port)
	p.blocked[proxyKey] = time.Now()
	p.logger.Warn("proxy_marked_blocked", slog.String("proxy", proxyKey))
}

// tryStartOnAvailablePort acquires a port from the channel pool and attempts
// to start a proxy server on it. O(1) acquire/release via buffered channel.
func (p *Pool) tryStartOnAvailablePort(proxy *WebshareProxy) (*Server, int, error) {
	poolSize := cap(p.portPool)
	for i := 0; i < poolSize; i++ {
		var port int
		select {
		case port = <-p.portPool:
		default:
			return nil, -1, fmt.Errorf("no available ports in range %d-%d (all in use)", p.portStart, p.portEnd)
		}

		server, err := NewServerFromProxy(proxy, port, p.logger)
		if err != nil {
			p.portPool <- port // return port on failure
			continue
		}
		if err := server.Start(); err != nil {
			p.portPool <- port // return port on failure
			continue
		}
		return server, port, nil
	}
	return nil, -1, fmt.Errorf("no available ports in range %d-%d", p.portStart, p.portEnd)
}

// GetStats returns pool statistics
func (p *Pool) GetStats() map[string]interface{} {
	p.blockMu.Lock()
	blockedCount := len(p.blocked)
	p.blockMu.Unlock()

	p.activeMu.Lock()
	activeCount := len(p.activeServers)
	p.activeMu.Unlock()

	return map[string]interface{}{
		"total_proxies":     len(p.proxies),
		"blocked_proxies":   blockedCount,
		"available_proxies": len(p.proxies) - blockedCount,
		"active_servers":    activeCount,
		"available_ports":   len(p.portPool),
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
