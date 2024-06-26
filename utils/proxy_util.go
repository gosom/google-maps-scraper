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

	return &playwright.Proxy{
		Server: u,
	}
}

func ToUrlProxy(proxyUrl string) *url.URL {
	if len(proxyUrl) <= 0 {
		return nil
	}
	return &url.URL{
		Scheme: "http",
		Host:   proxyUrl,
	}
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
