// Diagnostic binary that exercises the exact production review-RPC fetch path.
// Reads google_cookies.json the same way gmaps.LoadGoogleCookies does,
// constructs the listugcposts URL the same way fetcher.generateURL does,
// fires the request the same way fetchWithCookies does, then dumps the full
// response (status, headers, body bytes) so we can see exactly what Google
// is sending back.
//
// Usage:
//
//	go run ./scripts/debug_review_rpc \
//	  -cookies ./google_cookies.json \
//	  -place-url "https://www.google.com/maps/place/Distrikt+coffee/data=!4m7!3m6!1s0x47a851efbc1de6fb:0xeca1daa589fab359!8m2!3d52.531603!4d13.3941146!16s%2Fg%2F11bbrgx8km!19sChIJ--YdvO9RqEcRWbP6iaXaoew?authuser=0&hl=en&rclk=1"
//
// Optional:
//
//	-no-cookies     fire without Cookie header (control: what an unauth request looks like)
//	-proxy URL      route through HTTP proxy (e.g. one of the Decodo endpoints)
//	-out DIR        write raw bytes + metadata to DIR (default /tmp/review-rpc-diag-<ts>)
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type cookieEntry struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expirationDate,omitempty"`
	Secure   bool    `json:"secure"`
	HttpOnly bool    `json:"httpOnly"`
	SameSite string  `json:"sameSite,omitempty"`
}

func main() {
	cookiesPath := flag.String("cookies", "./google_cookies.json", "path to cookies JSON")
	placeURL := flag.String("place-url",
		"https://www.google.com/maps/place/Distrikt+coffee/data=!4m7!3m6!1s0x47a851efbc1de6fb:0xeca1daa589fab359!8m2!3d52.531603!4d13.3941146!16s%2Fg%2F11bbrgx8km!19sChIJ--YdvO9RqEcRWbP6iaXaoew?authuser=0&hl=en&rclk=1",
		"Google Maps place URL (must contain !1s<placeID>)")
	noCookies := flag.Bool("no-cookies", false, "control run: skip Cookie header")
	proxyURL := flag.String("proxy", "", "HTTP proxy URL (e.g. http://user:pass@host:port)")
	outDirFlag := flag.String("out", "", "output dir (default /tmp/review-rpc-diag-<ts>)")
	pageSize := flag.Int("page-size", 20, "page size param (production uses 20)")
	lang := flag.String("hl", "en", "language code")
	flag.Parse()

	outDir := *outDirFlag
	if outDir == "" {
		outDir = filepath.Join(os.TempDir(), "review-rpc-diag-"+time.Now().Format("20060102-150405"))
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatalf("mkdir %s: %v", outDir, err)
	}
	fmt.Printf("Output directory: %s\n", outDir)

	// Load cookies the same way LoadGoogleCookies does: read the JSON, filter
	// to entries whose domain contains "google" (case-insensitive in our case).
	cookies, err := loadCookies(*cookiesPath)
	if err != nil {
		fatalf("loadCookies: %v", err)
	}
	fmt.Printf("Loaded %d google-domain cookies from %s\n", len(cookies), *cookiesPath)

	// Build the Cookie header exactly like gmaps.GetCookieHeader does.
	cookieHeader := buildCookieHeader(cookies)
	if *noCookies {
		cookieHeader = ""
		fmt.Println("Control run: Cookie header WILL NOT be sent")
	} else {
		fmt.Printf("Cookie header length: %d chars, %d cookies joined\n", len(cookieHeader), len(cookies))
	}

	// Generate a request ID the same way the production fetcher does (random 21 chars).
	reqID, err := generateRandomID(21)
	if err != nil {
		fatalf("generateRandomID: %v", err)
	}

	// Construct the listugcposts URL the same way generateURL does.
	rpcURL, err := buildReviewURL(*placeURL, "", *pageSize, reqID, *lang)
	if err != nil {
		fatalf("buildReviewURL: %v", err)
	}
	fmt.Printf("Request URL: %s\n", rpcURL)

	// Save the constructed URL for reference.
	_ = os.WriteFile(filepath.Join(outDir, "request_url.txt"), []byte(rpcURL+"\n"), 0o644)

	// Build the http.Client. If -proxy is set, use it.
	transport := &http.Transport{}
	if *proxyURL != "" {
		pu, err := url.Parse(*proxyURL)
		if err != nil {
			fatalf("parse proxy: %v", err)
		}
		transport.Proxy = http.ProxyURL(pu)
		fmt.Printf("Proxy: %s\n", *proxyURL)
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}

	// Build the request the same way fetchWithCookies does.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", rpcURL, nil)
	if err != nil {
		fatalf("NewRequest: %v", err)
	}
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	// Save the request headers for reference.
	dumpHeadersToFile(filepath.Join(outDir, "request_headers.txt"), req.Header, cookieHeader, true)

	fmt.Println("\n--- firing request ---")
	t0 := time.Now()
	resp, err := client.Do(req)
	dur := time.Since(t0)
	if err != nil {
		fatalf("client.Do (after %s): %v", dur, err)
	}
	defer resp.Body.Close()

	fmt.Printf("Status: %s\n", resp.Status)
	fmt.Printf("Duration: %s\n", dur)
	fmt.Printf("Response headers:\n")
	for k, vv := range resp.Header {
		for _, v := range vv {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}
	dumpHeadersToFile(filepath.Join(outDir, "response_headers.txt"), resp.Header, "", false)
	_ = os.WriteFile(filepath.Join(outDir, "response_status.txt"), []byte(fmt.Sprintf("%s\n", resp.Status)), 0o644)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fatalf("read body: %v", err)
	}

	// Save the raw body (binary-safe).
	_ = os.WriteFile(filepath.Join(outDir, "response_body.bin"), body, 0o644)

	fmt.Printf("\nBody length: %d bytes\n", len(body))
	fmt.Println("\nHex dump (full body):")
	fmt.Println(hexDump(body))
	fmt.Println("\nAs UTF-8 (control chars escaped):")
	fmt.Println(escapeForDisplay(body))

	// Try to interpret the body as XSSI-prefixed JSON.
	if interpreted, ok := interpretXSSI(body); ok {
		fmt.Println("\nInterpreted as XSSI-prefixed JSON:")
		fmt.Println(interpreted)
		_ = os.WriteFile(filepath.Join(outDir, "response_body_decoded.json"), []byte(interpreted+"\n"), 0o644)
	}

	fmt.Printf("\nArtifacts saved in: %s\n", outDir)
}

func loadCookies(path string) ([]cookieEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var all []cookieEntry
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	// Filter to google-domain only — matches LoadGoogleCookies behavior.
	var filtered []cookieEntry
	for _, c := range all {
		if strings.Contains(strings.ToLower(c.Domain), "google") {
			filtered = append(filtered, c)
		}
	}
	return filtered, nil
}

func buildCookieHeader(cookies []cookieEntry) string {
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		parts = append(parts, fmt.Sprintf("%s=%s", c.Name, c.Value))
	}
	return strings.Join(parts, "; ")
}

func generateRandomID(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	max := big.NewInt(int64(len(charset)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = charset[n.Int64()]
	}
	return string(b), nil
}

// buildReviewURL mirrors gmaps.fetcher.generateURL exactly.
func buildReviewURL(mapURL, pageToken string, pageSize int, requestID, lang string) (string, error) {
	placeIDRegex := regexp.MustCompile(`!1s([^!]+)`)
	m := placeIDRegex.FindStringSubmatch(mapURL)
	if len(m) < 2 {
		return "", fmt.Errorf("no !1s<placeID> in mapURL: %s", mapURL)
	}
	rawPlaceID, err := url.QueryUnescape(m[1])
	if err != nil {
		rawPlaceID = m[1]
	}
	encodedPlaceID := url.QueryEscape(rawPlaceID)
	encodedPageToken := url.QueryEscape(pageToken)

	pb := []string{
		fmt.Sprintf("!1m6!1s%s", encodedPlaceID),
		"!6m4!4m1!1e1!4m1!1e3",
		fmt.Sprintf("!2m2!1i%d!2s%s", pageSize, encodedPageToken),
		fmt.Sprintf("!5m2!1s%s!7e81", requestID),
		"!8m9!2b1!3b1!5b1!7b1",
		"!12m4!1b1!2b1!4m1!1e1!11m0!13m1!1e1",
	}
	return fmt.Sprintf(
		"https://www.google.com/maps/rpc/listugcposts?authuser=0&hl=%s&pb=%s",
		lang, strings.Join(pb, ""),
	), nil
}

func dumpHeadersToFile(path string, h http.Header, cookieHeader string, isRequest bool) {
	var b strings.Builder
	for k, vv := range h {
		for _, v := range vv {
			if k == "Cookie" && len(v) > 80 {
				v = v[:40] + "...[REDACTED " + fmt.Sprint(len(v)-80) + " chars]..." + v[len(v)-40:]
			}
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\n")
		}
	}
	if isRequest && cookieHeader != "" {
		b.WriteString(fmt.Sprintf("# Cookie header was sent (length=%d)\n", len(cookieHeader)))
	}
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}

func hexDump(b []byte) string {
	var out strings.Builder
	for i := 0; i < len(b); i += 16 {
		end := i + 16
		if end > len(b) {
			end = len(b)
		}
		chunk := b[i:end]
		out.WriteString(fmt.Sprintf("%08x  ", i))
		for j := 0; j < 16; j++ {
			if j < len(chunk) {
				out.WriteString(fmt.Sprintf("%02x ", chunk[j]))
			} else {
				out.WriteString("   ")
			}
			if j == 7 {
				out.WriteString(" ")
			}
		}
		out.WriteString(" |")
		for _, c := range chunk {
			if c >= 32 && c < 127 {
				out.WriteByte(c)
			} else {
				out.WriteByte('.')
			}
		}
		out.WriteString("|\n")
	}
	return out.String()
}

func escapeForDisplay(b []byte) string {
	var out strings.Builder
	for _, c := range b {
		switch {
		case c == '\n':
			out.WriteString("\\n\n")
		case c == '\r':
			out.WriteString("\\r")
		case c == '\t':
			out.WriteString("\\t")
		case c < 32 || c == 127:
			out.WriteString(fmt.Sprintf("\\x%02x", c))
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// interpretXSSI strips the )]}' prefix Google uses and returns the rest.
func interpretXSSI(b []byte) (string, bool) {
	s := strings.TrimSpace(string(b))
	const prefix = ")]}'"
	if strings.HasPrefix(s, prefix) {
		return strings.TrimSpace(s[len(prefix):]), true
	}
	return "", false
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FATAL: "+format+"\n", args...)
	os.Exit(1)
}
