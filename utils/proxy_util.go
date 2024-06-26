package utils

import (
	"bufio"
	"net/url"
	"os"
	"strings"
	"sync/atomic"

	"github.com/playwright-community/playwright-go"
)

func ToPWProxy(u string) *playwright.Proxy {
	if len(u) <= 0 {
		return nil
	}
	//host:port:user:pass
	// TODO: parse proxyurl to playwright.Proxy,
	proxyUrl := strings.Split(u, ":")
	if len(proxyUrl) != 4 {
		return nil
	}
	return &playwright.Proxy{
		Server:   proxyUrl[0] + ":" + proxyUrl[1],
		Username: &proxyUrl[2],
		Password: &proxyUrl[3],
	}
}

// ToUrlProxy converts a proxy URL string in the format "host:port:user:pass" to a *url.URL object.
func ToUrlProxy(proxyUrl string) *url.URL {
	if len(proxyUrl) <= 0 {
		return nil
	}
	// TODO: parse proxyurl (host:port:user:pass) to url.URL
	// Split the proxy URL string by colon (':').
	parts := strings.Split(proxyUrl, ":")

	// Check if there are at least 2 parts (host and port).
	if len(parts) < 2 {
		return nil
	}

	// Create a new URL object.
	u := &url.URL{
		Scheme: "http",                    // Default to HTTP scheme.
		Host:   parts[0] + ":" + parts[1], // Combine host and port.
	}

	// If there are more than 2 parts, assume they are username and password.
	if len(parts) > 2 {
		u.User = url.UserPassword(parts[2], parts[3])
	}

	return u
}

var (
	currentProxyIndex int32
)

func GetRoundRobinInProxyUrl(urls []string) string {
	if len(urls) <= 0 {
		return ""
	}

	// Get the current proxy index and increment it atomically.
	index := atomic.AddInt32(&currentProxyIndex, 1)

	// Wrap around to the beginning of the slice if we reach the end.
	index %= int32(len(urls))

	// Return the proxy URL at the calculated index.
	return urls[index]
}

func GetProxiesFromTxtFile(txtFilePath string) []string {
	// TODO: read txt file and extract proxies (one proxy per line)
	file, err := os.Open(txtFilePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	var proxies []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		proxy := strings.TrimSpace(scanner.Text())
		if proxy != "" {
			proxies = append(proxies, proxy)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil
	}

	return proxies
}
