package tlmt

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v4/host"
)

var (
	once       sync.Once
	identifier machineIdentifier
)

type Event struct {
	AnonymousID string
	Name        string
	Properties  map[string]any
}

func NewEvent(name string, props map[string]any) Event {
	mid := generateMachineID()

	// Copy meta to avoid mutating the shared cached map.
	merged := make(map[string]any, len(mid.meta)+len(props))
	for k, v := range mid.meta {
		merged[k] = v
	}
	for k, v := range props {
		merged[k] = v
	}

	return Event{
		AnonymousID: mid.id,
		Name:        name,
		Properties:  merged,
	}
}

type Telemetry interface {
	Send(ctx context.Context, event Event) error
	Close() error
}

type machineIdentifier struct {
	id   string
	meta map[string]any
}

func generateMachineID() machineIdentifier {
	once.Do(func() {
		id := loadOrCreateMachineID()
		if id == "" {
			id = legacyMachineID()
		}

		meta := make(map[string]any)

		info, err := host.Info()
		if err == nil {
			meta["os"] = info.OS
			meta["platform"] = info.Platform
			meta["platform_family"] = info.PlatformFamily
			meta["platform_version"] = info.PlatformVersion
		}

		identifier.id = id
		identifier.meta = meta
	})

	return identifier
}

// loadOrCreateMachineID returns a stable UUID persisted to disk.
// Returns "" on any error so the caller can fall back.
func loadOrCreateMachineID() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".config", "brezel")
	path := filepath.Join(dir, ".machine-id")

	data, err := os.ReadFile(path)
	if err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			if _, parseErr := uuid.Parse(id); parseErr == nil {
				return id
			}
			// File corrupted; fall through to regenerate.
		}
	}

	id := uuid.New().String()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		return ""
	}
	return id
}

// legacyMachineID computes a machine ID from external IP + arch (unstable fallback).
func legacyMachineID() string {
	ip := fetchExternalIP()
	if ip == "" {
		ip = uuid.New().String()
	}

	hash := sha256.New()
	hash.Write([]byte(ip))
	hash.Write([]byte(runtime.GOARCH))
	hash.Write([]byte(runtime.GOOS))
	hash.Write([]byte(runtime.Version()))

	return fmt.Sprintf("%x", hash.Sum(nil))
}

func fetchExternalIP() string {
	endpoints := []string{
		"https://api.ipify.org",
		"https://ifconfig.me",
		"https://icanhazip.com",
		"https://ident.me",
		"https://ifconfig.co",
	}

	rand.Shuffle(len(endpoints), func(i, j int) {
		endpoints[i], endpoints[j] = endpoints[j], endpoints[i]
	})

	client := http.Client{
		Timeout: 5 * time.Second,
	}

	for _, endpoint := range endpoints {
		ip := func(u string) string {
			req, err := http.NewRequest(http.MethodGet, u, http.NoBody)
			if err != nil {
				return ""
			}

			resp, err := client.Do(req)
			if err != nil {
				return ""
			}

			defer func() {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}()

			if resp.StatusCode != http.StatusOK {
				return ""
			}

			ip, err := io.ReadAll(resp.Body)
			if err != nil {
				return ""
			}

			return strings.TrimSpace(string(ip))
		}(endpoint)

		if ip != "" {
			return ip
		}
	}

	return ""
}
