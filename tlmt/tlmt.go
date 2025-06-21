package tlmt

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
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
	ev := Event{
		AnonymousID: generateMachineID().id,
		Name:        name,
		Properties:  generateMachineID().meta,
	}

	for k, v := range props {
		ev.Properties[k] = v
	}

	return ev
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
		ip := fetchExternalIP()
		if ip == "" {
			ip = uuid.New().String()
		}

		hash := sha256.New()
		hash.Write([]byte(ip))
		hash.Write([]byte(runtime.GOARCH))
		hash.Write([]byte(runtime.GOOS))
		hash.Write([]byte(runtime.Version()))

		id := fmt.Sprintf("%x", hash.Sum(nil))

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
