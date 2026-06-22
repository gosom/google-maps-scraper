package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/gmaps"
)

// ====================================================================
// searchText request / response types
// ====================================================================

type searchTextRequest struct {
	TextQuery           string               `json:"textQuery"`
	LocationRestriction *locationRestriction `json:"locationRestriction,omitempty"`
	PageToken           string               `json:"pageToken,omitempty"`
}

type locationRestriction struct {
	Rectangle struct {
		Low  latLng `json:"low"`
		High latLng `json:"high"`
	} `json:"rectangle"`
}

type latLng struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type searchTextResponse struct {
	Places        []*googlePlace `json:"places"`
	NextPageToken string         `json:"nextPageToken,omitempty"`
}

// ====================================================================
// search result cache
// ====================================================================

const (
	searchPageSize = 20
	searchCacheTTL = 5 * time.Minute
)

type cachedSearch struct {
	places    []*googlePlace
	createdAt time.Time
}

var (
	searchResultCache sync.Map // sha256key -> *cachedSearch
	searchTokenCache  sync.Map // UUID token -> tokenEntry
)

type tokenEntry struct {
	key    string
	offset int
}

func searchCacheKey(textQuery string, lr *locationRestriction) string {
	h := sha256.Sum256([]byte(textQuery + "|" + jsonMust(lr)))
	return hex.EncodeToString(h[:])
}

func jsonMust(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func newPageToken(cacheKey string, offset int) string {
	token := uuid.New().String()
	searchTokenCache.Store(token, tokenEntry{key: cacheKey, offset: offset})
	return token
}

func lookupPageToken(token string) (key string, offset int, ok bool) {
	v, exists := searchTokenCache.Load(token)
	if !exists {
		return
	}
	t := v.(tokenEntry)
	return t.key, t.offset, true
}

func respondSearchPage(w http.ResponseWriter, places []*googlePlace, key string, offset int) {
	end := offset + searchPageSize
	if end > len(places) {
		end = len(places)
	}
	resp := searchTextResponse{Places: places[offset:end]}
	if end < len(places) {
		resp.NextPageToken = newPageToken(key, end)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// haversineDist returns the great-circle distance in km between two lat/lon points.
func haversineDist(a, b latLng) float64 {
	const R = 6371.0
	dLat := (b.Latitude - a.Latitude) * math.Pi / 180
	dLon := (b.Longitude - a.Longitude) * math.Pi / 180
	x := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(a.Latitude*math.Pi/180)*math.Cos(b.Latitude*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	return R * 2 * math.Atan2(math.Sqrt(x), math.Sqrt(1-x))
}

// ====================================================================
// POST /v1/places:searchText handler
// ====================================================================

func searchTextHandler(eng *httpEngine, langCode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req searchTextRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TextQuery == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Serve from page-token cache.
		if req.PageToken != "" {
			if key, offset, ok := lookupPageToken(req.PageToken); ok {
				if v, ok2 := searchResultCache.Load(key); ok2 {
					c := v.(*cachedSearch)
					if time.Since(c.createdAt) < searchCacheTTL {
						respondSearchPage(w, c.places, key, offset)
						return
					}
				}
			}
			// token expired or unknown — fall through to fresh search
		}

		// Build location params.
		params := &gmaps.MapSearchParams{Query: req.TextQuery, Hl: langCode}
		if req.LocationRestriction != nil {
			rect := req.LocationRestriction.Rectangle
			params.Location = gmaps.MapLocation{
				Lat:     (rect.Low.Latitude + rect.High.Latitude) / 2,
				Lon:     (rect.Low.Longitude + rect.High.Longitude) / 2,
				ZoomLvl: 14,
				Radius:  haversineDist(rect.Low, rect.High) / 2 * 1000, // km → meters
			}
		} else {
			params.Location = gmaps.MapLocation{Lat: 0, Lon: 0, ZoomLvl: 2, Radius: 20_037_000}
		}

		entries, err := eng.scrapeSearch(r.Context(), params)
		if err != nil {
			log.Printf("[searchText] scrape error: %v", err)
		}

		places := make([]*googlePlace, 0, len(entries))
		for _, e := range entries {
			places = append(places, convertEntryToPlace(e))
		}

		key := searchCacheKey(req.TextQuery, req.LocationRestriction)
		searchResultCache.Store(key, &cachedSearch{places: places, createdAt: time.Now()})
		respondSearchPage(w, places, key, 0)
	}
}
