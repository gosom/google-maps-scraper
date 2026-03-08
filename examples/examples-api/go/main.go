// Batch Maps scraper client.
//
// Submits keywords in parallel (up to 20 at a time), polls for results,
// and saves each completed job to a JSON file.
//
// Usage:
//
//	# Keywords as arguments
//	go run main.go -base-url https://example.com -api-key gms_... "cafes in athens" "hotels in berlin"
//
//	# Keywords from stdin (one per line)
//	cat keywords.txt | go run main.go -base-url https://example.com -api-key gms_...
//
//	# Custom output directory
//	go run main.go -base-url https://example.com -api-key gms_... -o results "cafes in athens"
//
//	# Skip TLS certificate verification (e.g. self-signed certs)
//	go run main.go -base-url https://example.com -api-key gms_... -insecure "cafes in athens"
package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type scrapeResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

type jobStatusResponse struct {
	JobID       string `json:"job_id"`
	Status      string `json:"status"`
	Keyword     string `json:"keyword"`
	Results     any    `json:"results"`
	ResultCount int    `json:"result_count"`
	Error       string `json:"error"`
}

var (
	reUnsafe = regexp.MustCompile(`[^\w\s-]`)
	reSpaces = regexp.MustCompile(`[\s]+`)
)

func safeFilename(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = reUnsafe.ReplaceAllString(s, "")
	s = reSpaces.ReplaceAllString(s, "_")
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}

func apiRequest(client *http.Client, baseURL, apiKey, method, path string, body any) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, strings.TrimRight(baseURL, "/")+path, reqBody)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	return respBody, resp.StatusCode, nil
}

func submitJob(client *http.Client, baseURL, apiKey, keyword, lang string, maxDepth int) (string, error) {
	body := map[string]any{"keyword": keyword, "lang": lang, "max_depth": maxDepth}
	respBody, code, err := apiRequest(client, baseURL, apiKey, "POST", "/api/v1/scrape", body)
	if err != nil {
		return "", err
	}
	if code != http.StatusAccepted {
		return "", fmt.Errorf("HTTP %d: %s", code, respBody)
	}

	var resp scrapeResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", err
	}
	return resp.JobID, nil
}

func pollJob(client *http.Client, baseURL, apiKey, jobID, keyword, outputDir string) error {
	for {
		respBody, code, err := apiRequest(client, baseURL, apiKey, "GET", "/api/v1/jobs/"+jobID, nil)
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return fmt.Errorf("HTTP %d: %s", code, respBody)
		}

		var resp jobStatusResponse
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return err
		}

		switch resp.Status {
		case "completed":
			fname := fmt.Sprintf("%s-%s.json", jobID, safeFilename(keyword))
			fpath := filepath.Join(outputDir, fname)

			results, _ := json.MarshalIndent(resp.Results, "", "  ")
			if err := os.WriteFile(fpath, results, 0o644); err != nil {
				return err
			}
			fmt.Printf("  [done] %q -> %d results -> %s\n", keyword, resp.ResultCount, fname)
			return nil

		case "failed":
			errMsg := resp.Error
			if errMsg == "" {
				errMsg = "unknown error"
			}
			fmt.Fprintf(os.Stderr, "  [fail] %q: %s\n", keyword, errMsg)
			return nil
		}

		time.Sleep(5 * time.Second)
	}
}

func processKeyword(client *http.Client, baseURL, apiKey, keyword, lang string, maxDepth int, outputDir string) {
	jobID, err := submitJob(client, baseURL, apiKey, keyword, lang, maxDepth)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [error] submit %q: %v\n", keyword, err)
		return
	}

	fmt.Printf("  [submitted] %q -> job %s\n", keyword, jobID)

	if err := pollJob(client, baseURL, apiKey, jobID, keyword, outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "  [error] poll %q: %v\n", keyword, err)
	}
}

func readStdin() []string {
	var keywords []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		kw := strings.TrimSpace(scanner.Text())
		if kw != "" {
			keywords = append(keywords, kw)
		}
	}
	return keywords
}

func main() {
	baseURL := flag.String("base-url", "", "API base URL (required)")
	apiKey := flag.String("api-key", "", "API key (required)")
	outputDir := flag.String("o", "map-outputs", "Output directory")
	workers := flag.Int("w", 20, "Max parallel jobs")
	lang := flag.String("lang", "en", "Language for results (e.g. en, de, el)")
	maxDepth := flag.Int("max-depth", 1, "Max scrape depth")
	insecure := flag.Bool("insecure", false, "Skip TLS certificate verification (e.g. for self-signed certs)")
	flag.Parse()

	if *baseURL == "" || *apiKey == "" {
		fmt.Fprintln(os.Stderr, "Usage: scrape -base-url URL -api-key KEY [keywords...]")
		os.Exit(1)
	}

	transport := http.DefaultTransport
	if *insecure {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: transport}

	keywords := flag.Args()
	if len(keywords) == 0 {
		fi, _ := os.Stdin.Stat()
		if fi.Mode()&os.ModeCharDevice != 0 {
			fmt.Fprintln(os.Stderr, "Provide keywords as arguments or pipe them via stdin")
			os.Exit(1)
		}
		keywords = readStdin()
	}

	if len(keywords) == 0 {
		fmt.Fprintln(os.Stderr, "No keywords provided")
		os.Exit(1)
	}

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create output dir: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Scraping %d keyword(s), max %d parallel, output -> %s/\n",
		len(keywords), *workers, *outputDir)

	sem := make(chan struct{}, *workers)
	var wg sync.WaitGroup

	for _, kw := range keywords {
		wg.Add(1)
		sem <- struct{}{}

		go func(keyword string) {
			defer wg.Done()
			defer func() { <-sem }()
			processKeyword(httpClient, *baseURL, *apiKey, keyword, *lang, *maxDepth, *outputDir)
		}(kw)
	}

	wg.Wait()
	fmt.Println("Done.")
}
