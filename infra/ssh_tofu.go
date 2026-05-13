package infra

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var knownHostsMu sync.Mutex

// NewTOFUHostKeyCallback returns a host key callback that uses trust-on-first-use
// with ~/.gmapssaas/known_hosts and enforces pinned keys on later connections.
func NewTOFUHostKeyCallback(addr string) (ssh.HostKeyCallback, error) {
	knownHostsPath, err := ensureKnownHostsFile()
	if err != nil {
		return nil, err
	}

	callback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load known_hosts: %w", err)
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if hostname == "" {
			hostname = addr
		}

		err := callback(hostname, remote, key)
		if err == nil {
			return nil
		}

		if !isUnknownHostError(err) {
			return err
		}

		if err := appendHostKeyIfMissing(knownHostsPath, hostname, remote, key); err != nil {
			return err
		}

		updatedCallback, err := knownhosts.New(knownHostsPath)
		if err != nil {
			return fmt.Errorf("failed to reload known_hosts: %w", err)
		}

		return updatedCallback(hostname, remote, key)
	}, nil
}

func ensureKnownHostsFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}

	dir := filepath.Join(home, ".gmapssaas")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create %q: %w", dir, err)
	}

	path := filepath.Join(dir, "known_hosts")

	file, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		return "", fmt.Errorf("failed to initialize %q: %w", path, err)
	}

	_ = file.Close()

	return path, nil
}

func isUnknownHostError(err error) bool {
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) {
		return false
	}

	return len(keyErr.Want) == 0
}

func appendHostKeyIfMissing(path, hostname string, remote net.Addr, key ssh.PublicKey) error {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()

	callback, err := knownhosts.New(path)
	if err != nil {
		return fmt.Errorf("failed to load known_hosts: %w", err)
	}

	err = callback(hostname, remote, key)
	if err == nil {
		return nil
	}

	if !isUnknownHostError(err) {
		return err
	}

	hosts := normalizedHostEntries(hostname, remote)

	line := knownhosts.Line(hosts, key)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open %q: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	if _, err := file.WriteString(line); err != nil {
		return fmt.Errorf("failed to append host key: %w", err)
	}

	return nil
}

func normalizedHostEntries(hostname string, remote net.Addr) []string {
	seen := map[string]struct{}{}

	add := func(host string) {
		if host == "" {
			return
		}

		normalized := knownhosts.Normalize(host)
		if normalized == "" {
			return
		}

		seen[normalized] = struct{}{}
	}

	add(hostname)

	if remote != nil {
		add(remote.String())
	}

	result := make([]string, 0, len(seen))
	for host := range seen {
		result = append(result, host)
	}

	if len(result) == 0 {
		result = append(result, knownhosts.Normalize(hostname))
	}

	return result
}
