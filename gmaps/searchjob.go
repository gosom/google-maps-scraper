package gmaps

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/scrapemate"
)

type SearchJobOptions func(*SearchJob)

type MapLocation struct {
	Lat     float64
	Lon     float64
	ZoomLvl float64
	Radius  float64
}

type MapSearchParams struct {
	Location  MapLocation
	Query     string
	ViewportW int
	ViewportH int
	Hl        string
}

type SearchJob struct {
	scrapemate.Job

	params      *MapSearchParams
	ExitMonitor exiter.Exiter
}

func NewSearchJob(params *MapSearchParams, opts ...SearchJobOptions) *SearchJob {
	const (
		defaultPrio       = scrapemate.PriorityMedium
		defaultMaxRetries = 3
		baseURL           = "https://www.google.com/maps"
	)

	// Ensure params are not nil
	if params == nil {
		params = &MapSearchParams{
			Hl: "en",
		}
	}
	
	// Special handling for the problematic URL pattern seen in logs
	if params.Query != "" && strings.Contains(params.Query, "https://www.google.com/maps/place/Your+Business/@xx.xxxx,yy.yyyy,17z") {
		// This is a template URL, not a real query - replace with a simple business search
		fmt.Println("Detected template URL pattern, replacing with simpler query")
		params.Query = "business"
	}
	
	// Clean up the query if it's a URL
	if params.Query != "" {
		// Check if the query itself is a URL (especially a Google Maps URL)
		if strings.HasPrefix(params.Query, "http") {
			// Extract just the business name or location if it's a maps URL
			if strings.Contains(params.Query, "google.com/maps") {
				// Try to extract just the search term or business name
				parts := strings.Split(params.Query, "/")
				if len(parts) > 0 {
					// Get the last non-empty part that's not coordinates
					for i := len(parts) - 1; i >= 0; i-- {
						part := parts[i]
						if part != "" && 
						   !strings.HasPrefix(part, "@") && 
						   !strings.Contains(part, ",") &&
						   !strings.Contains(part, ".") {
							params.Query = part
							fmt.Printf("Extracted query from URL: %s\n", params.Query)
							break
						}
					}
					
					// If we couldn't find a good part, just use a generic term
					if strings.HasPrefix(params.Query, "http") {
						params.Query = "business"
					}
				}
			} else {
				// For other URLs, just use the domain as the search term
				u, err := url.Parse(params.Query)
				if err == nil && u.Host != "" {
					params.Query = u.Host
					fmt.Printf("Using domain as query: %s\n", params.Query)
				}
			}
		}
	}
	
	// Set default query if empty
	if params.Query == "" && params.Location.Lat != 0 && params.Location.Lon != 0 {
		// Use coordinates in the search if no query
		params.Query = fmt.Sprintf("%.6f,%.6f", params.Location.Lat, params.Location.Lon)
	}
	
	// Final sanity check - if query still starts with http, just use a simple term
	if strings.HasPrefix(params.Query, "http") {
		params.Query = "business"
		fmt.Println("Query still a URL after cleaning, using 'business' instead")
	}

	job := SearchJob{
		Job: scrapemate.Job{
			ID:         uuid.New().String(),
			Method:     http.MethodGet,
			URL:        baseURL,
			URLParams:  buildGoogleMapsParams(params),
			MaxRetries: defaultMaxRetries,
			Priority:   defaultPrio,
		},
		params: params,
	}
	
	// Log the final URL for debugging
	fmt.Printf("Created search job with URL: %s, params: %v\n", job.URL, job.URLParams)

	for _, opt := range opts {
		opt(&job)
	}

	return &job
}

func WithSearchJobExitMonitor(exitMonitor exiter.Exiter) SearchJobOptions {
	return func(j *SearchJob) {
		j.ExitMonitor = exitMonitor
	}
}

func (j *SearchJob) Process(_ context.Context, resp *scrapemate.Response) (any, []scrapemate.IJob, error) {
	defer func() {
		resp.Document = nil
		resp.Body = nil
		resp.Meta = nil
	}()

	if resp.Body == nil {
		return nil, nil, fmt.Errorf("response body is nil")
	}

	body := removeFirstLine(resp.Body)
	if len(body) == 0 {
		if page, ok := resp.Document.(scrapemate.PlaywrightPage); ok {
			content, err := page.Page().Content()
			if err == nil && content != "" {
				body = []byte(content)
			} else {
				return nil, nil, fmt.Errorf("empty response body and failed to get page content: %w", err)
			}
		} else {
			return nil, nil, fmt.Errorf("empty response body and document is not a Playwright page")
		}
	}

	entries, err := ParseSearchResults(body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse search results: %w", err)
	}

	entries = filterAndSortEntriesWithinRadius(entries,
		j.params.Location.Lat,
		j.params.Location.Lon,
		j.params.Location.Radius,
	)

	if j.ExitMonitor != nil {
		j.ExitMonitor.IncrSeedCompleted(1)
		j.ExitMonitor.IncrPlacesFound(len(entries))
		j.ExitMonitor.IncrPlacesCompleted(len(entries))
	}

	return entries, nil, nil
}

func removeFirstLine(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	index := bytes.IndexByte(data, '\n')
	if index == -1 {
		return []byte{}
	}

	return data[index+1:]
}

func buildGoogleMapsParams(params *MapSearchParams) map[string]string {
	if params.ViewportH == 0 {
		params.ViewportH = 800
	}
	
	if params.ViewportW == 0 {
		params.ViewportW = 600
	}
	
	if params.Hl == "" {
		params.Hl = "en"
	}

	// Make sure the query is not a URL
	if params.Query != "" {
		// If it still looks like a URL after previous cleaning, extract just alphanumeric parts
		if strings.HasPrefix(params.Query, "http") || strings.Contains(params.Query, "://") {
			// Simplify to just the domain or last path component
			parts := strings.Split(params.Query, "/")
			if len(parts) > 2 {
				domainPart := parts[2] // domain is usually the 3rd part (after http: and //)
				if domainPart != "" {
					params.Query = domainPart
				} else {
					// Find the first non-empty part
					for _, part := range parts {
						if part != "" && !strings.Contains(part, ":") {
							params.Query = part
							break
						}
					}
				}
			}
		}
	}

	ans := map[string]string{
		"authuser": "0",
		"hl":       params.Hl,
	}

	// Add query parameter only if it exists and is not a URL
	if params.Query != "" {
		ans["q"] = params.Query
	}
	
	// If we have latitude and longitude, use them in the URL
	if params.Location.Lat != 0 && params.Location.Lon != 0 {
		// Standard Google Maps search format
		pb := fmt.Sprintf("!4m12!1m3!1d%.8f!2d%.8f!3d%.8f!2m3!1f0!2f0!3f0!3m2!1i%d!2i%d!4f%.1f!10b1",
			0.001,
			params.Location.Lon,
			params.Location.Lat,
			params.ViewportW,
			params.ViewportH,
			params.Location.ZoomLvl,
		)
		
		ans["pb"] = pb
	}

	return ans
}
