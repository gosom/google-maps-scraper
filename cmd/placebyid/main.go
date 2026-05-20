package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
	"github.com/gosom/scrapemate/scrapemateapp"
)

// ====================================================================
// Google Places API v1 output schema (matches the curl response shape)
// ====================================================================

type googlePlace struct {
	ID                       string               `json:"id"`
	Types                    []string             `json:"types,omitempty"`
	InternationalPhoneNumber string               `json:"internationalPhoneNumber,omitempty"`
	FormattedAddress         string               `json:"formattedAddress,omitempty"`
	AddressComponents        []googleAddrComp     `json:"addressComponents,omitempty"`
	Location                 *googleLocation      `json:"location,omitempty"`
	Rating                   float64              `json:"rating,omitempty"`
	GoogleMapsURI            string               `json:"googleMapsUri,omitempty"`
	WebsiteURI               string               `json:"websiteUri,omitempty"`
	RegularOpeningHours      *googleOpeningHours  `json:"regularOpeningHours,omitempty"`
	BusinessStatus           string               `json:"businessStatus,omitempty"`
	UserRatingCount          int                  `json:"userRatingCount,omitempty"`
	DisplayName              *googleLocalizedText `json:"displayName,omitempty"`
	Reviews                  []googleReview       `json:"reviews,omitempty"`
	Photos                   []googlePhoto        `json:"photos,omitempty"`
}

type googleLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type googleAddrComp struct {
	LongText     string   `json:"longText"`
	ShortText    string   `json:"shortText"`
	Types        []string `json:"types"`
	LanguageCode string   `json:"languageCode"`
}

type googleOpeningHours struct {
	WeekdayDescriptions []string `json:"weekdayDescriptions"`
}

type googleLocalizedText struct {
	Text         string `json:"text"`
	LanguageCode string `json:"languageCode,omitempty"`
}

type googleReview struct {
	Name                           string               `json:"name,omitempty"`
	RelativePublishTimeDescription string               `json:"relativePublishTimeDescription,omitempty"`
	Rating                         int                  `json:"rating"`
	Text                           *googleLocalizedText `json:"text,omitempty"`
	OriginalText                   *googleLocalizedText `json:"originalText,omitempty"`
	AuthorAttribution              *googleAuthorAttr    `json:"authorAttribution,omitempty"`
	PublishTime                    string               `json:"publishTime,omitempty"`
}

type googleAuthorAttr struct {
	DisplayName string `json:"displayName,omitempty"`
	URI         string `json:"uri,omitempty"`
	PhotoURI    string `json:"photoUri,omitempty"`
}

type googlePhoto struct {
	Name               string             `json:"name,omitempty"`
	WidthPx            int                `json:"widthPx,omitempty"`
	HeightPx           int                `json:"heightPx,omitempty"`
	AuthorAttributions []googleAuthorAttr `json:"authorAttributions,omitempty"`
	// Non-Google extension: directly-usable image URL. Google's API requires a
	// follow-up /v1/{name}/media call; we already have the URL from the scrape.
	ImageURL string `json:"imageUrl,omitempty"`
}

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

// ====================================================================
// gmaps.Entry → googlePlace conversion
// ====================================================================

var (
	streetRe = regexp.MustCompile(`^\s*(\d+\w*)\s+(.+?)\s*$`)
	dateRe   = regexp.MustCompile(`^\d{4}-\d{1,2}-\d{1,2}$`)
)

func humanizeDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || !dateRe.MatchString(s) {
		return s
	}
	t, err := time.Parse("2006-1-2", s)
	if err != nil {
		return s
	}
	days := int(time.Since(t).Hours() / 24)
	switch {
	case days < 7:
		return fmt.Sprintf("%d days ago", days)
	case days < 30:
		return fmt.Sprintf("%d weeks ago", days/7)
	case days < 365:
		return fmt.Sprintf("%d months ago", days/30)
	default:
		return fmt.Sprintf("%d years ago", days/365)
	}
}

func toPublishTime(s string) string {
	s = strings.TrimSpace(s)
	t, err := time.Parse("2006-1-2", s)
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func convertEntry(e *gmaps.Entry, placeID string) *googlePlace {
	mapsURI := e.Link
	if e.Cid != "" {
		mapsURI = "https://maps.google.com/?cid=" + e.Cid
	}
	out := &googlePlace{
		ID:                       placeID,
		FormattedAddress:         e.Address,
		WebsiteURI:               e.WebSite,
		InternationalPhoneNumber: e.Phone,
		GoogleMapsURI:            mapsURI,
		Rating:                   e.ReviewRating,
		UserRatingCount:          e.ReviewCount,
	}

	if e.Title != "" {
		out.DisplayName = &googleLocalizedText{Text: e.Title, LanguageCode: "en"}
	}

	if e.Latitude != 0 || e.Longtitude != 0 {
		out.Location = &googleLocation{
			Latitude:  e.Latitude,
			Longitude: e.Longtitude, // scraper's typo, we map to canonical name
		}
	}

	// Default to OPERATIONAL when scraper leaves status blank but page rendered
	out.BusinessStatus = mapBusinessStatus(e.Status)

	out.Types = convertTypes(e.Categories, e.Category)

	if len(e.OpenHours) > 0 {
		out.RegularOpeningHours = &googleOpeningHours{
			WeekdayDescriptions: convertOpeningHours(e.OpenHours),
		}
	}

	if comps := convertAddress(e.CompleteAddress); len(comps) > 0 {
		out.AddressComponents = comps
	}

	// Reviews
	allReviews := append([]gmaps.Review{}, e.UserReviews...)
	allReviews = append(allReviews, e.UserReviewsExtended...)
	for i, r := range allReviews {
		gr := googleReview{
			Name:                           fmt.Sprintf("places/%s/reviews/scraped-%d", placeID, i),
			RelativePublishTimeDescription: humanizeDate(r.When),
			PublishTime:                    toPublishTime(r.When),
			Rating:                         r.Rating,
		}
		if r.Description != "" {
			gr.Text = &googleLocalizedText{Text: r.Description, LanguageCode: "en"}
			gr.OriginalText = &googleLocalizedText{Text: r.Description, LanguageCode: "en"}
		}
		if r.Name != "" || r.ProfilePicture != "" {
			gr.AuthorAttribution = &googleAuthorAttr{
				DisplayName: r.Name,
				PhotoURI:    r.ProfilePicture,
			}
		}
		out.Reviews = append(out.Reviews, gr)
	}

	// Photos — synthesize Name + put real URL in custom imageUrl
	if e.Thumbnail != "" {
		out.Photos = append(out.Photos, googlePhoto{
			Name:     fmt.Sprintf("places/%s/photos/thumbnail", placeID),
			ImageURL: e.Thumbnail,
		})
	}
	for i, img := range e.Images {
		if img.Image == "" {
			continue
		}
		out.Photos = append(out.Photos, googlePhoto{
			Name:     fmt.Sprintf("places/%s/photos/scraped-%d", placeID, i),
			ImageURL: img.Image,
		})
	}

	return out
}

func mapBusinessStatus(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return "OPERATIONAL" // scraper found the place but didn't fill status
	case strings.Contains(s, "permanently closed") || strings.Contains(s, "closed permanently"):
		return "CLOSED_PERMANENTLY"
	case strings.Contains(s, "temporarily closed"):
		return "CLOSED_TEMPORARILY"
	case strings.Contains(s, "open") || strings.Contains(s, "operational"):
		return "OPERATIONAL"
	default:
		return strings.ToUpper(strings.ReplaceAll(s, " ", "_"))
	}
}

func convertTypes(categories []string, primary string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		snake := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", "_"))
		if snake == "" || seen[snake] {
			return
		}
		seen[snake] = true
		out = append(out, snake)
	}
	if primary != "" {
		add(primary)
	}
	for _, c := range categories {
		add(c)
	}
	// Common Google trailing types
	if len(out) > 0 {
		add("point_of_interest")
		add("establishment")
	}
	return out
}

func convertOpeningHours(oh map[string][]string) []string {
	days := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	var out []string
	for _, day := range days {
		slots, ok := oh[day]
		if !ok {
			for k, v := range oh { // case-insensitive fallback
				if strings.EqualFold(k, day) {
					slots = v
					ok = true
					break
				}
			}
		}
		if !ok {
			continue
		}
		if len(slots) == 0 {
			out = append(out, fmt.Sprintf("%s: Closed", day))
		} else {
			out = append(out, fmt.Sprintf("%s: %s", day, strings.Join(slots, ", ")))
		}
	}
	return out
}

func convertAddress(a gmaps.Address) []googleAddrComp {
	var out []googleAddrComp

	if a.Street != "" {
		if m := streetRe.FindStringSubmatch(a.Street); m != nil {
			out = append(out, googleAddrComp{
				LongText: m[1], ShortText: m[1],
				Types:        []string{"street_number"},
				LanguageCode: "en-US",
			})
			out = append(out, googleAddrComp{
				LongText: m[2], ShortText: shortRoute(m[2]),
				Types:        []string{"route"},
				LanguageCode: "en",
			})
		} else {
			out = append(out, googleAddrComp{
				LongText: a.Street, ShortText: a.Street,
				Types:        []string{"route"},
				LanguageCode: "en",
			})
		}
	}
	if a.City != "" {
		out = append(out, googleAddrComp{
			LongText: a.City, ShortText: a.City,
			Types:        []string{"locality", "political"},
			LanguageCode: "en",
		})
	}
	if a.State != "" {
		long, short := stateNames(a.State)
		out = append(out, googleAddrComp{
			LongText: long, ShortText: short,
			Types:        []string{"administrative_area_level_1", "political"},
			LanguageCode: "en",
		})
	}
	if a.Country != "" {
		long, short := countryNames(a.Country)
		out = append(out, googleAddrComp{
			LongText: long, ShortText: short,
			Types:        []string{"country", "political"},
			LanguageCode: "en",
		})
	}
	if a.PostalCode != "" {
		out = append(out, googleAddrComp{
			LongText: a.PostalCode, ShortText: a.PostalCode,
			Types:        []string{"postal_code"},
			LanguageCode: "en-US",
		})
	}
	return out
}

func shortRoute(route string) string {
	return strings.NewReplacer(
		"Avenue", "Ave", "Street", "St", "Road", "Rd", "Boulevard", "Blvd",
		"Drive", "Dr", "Court", "Ct", "Lane", "Ln", "Place", "Pl",
		"Highway", "Hwy", "Parkway", "Pkwy",
	).Replace(route)
}

// US-only state map; extend or replace with a full lib for international use.
func stateNames(s string) (long, short string) {
	m := map[string]string{
		"AL": "Alabama", "AK": "Alaska", "AZ": "Arizona", "AR": "Arkansas",
		"CA": "California", "CO": "Colorado", "CT": "Connecticut", "DE": "Delaware",
		"FL": "Florida", "GA": "Georgia", "HI": "Hawaii", "ID": "Idaho",
		"IL": "Illinois", "IN": "Indiana", "IA": "Iowa", "KS": "Kansas",
		"KY": "Kentucky", "LA": "Louisiana", "ME": "Maine", "MD": "Maryland",
		"MA": "Massachusetts", "MI": "Michigan", "MN": "Minnesota", "MS": "Mississippi",
		"MO": "Missouri", "MT": "Montana", "NE": "Nebraska", "NV": "Nevada",
		"NH": "New Hampshire", "NJ": "New Jersey", "NM": "New Mexico", "NY": "New York",
		"NC": "North Carolina", "ND": "North Dakota", "OH": "Ohio", "OK": "Oklahoma",
		"OR": "Oregon", "PA": "Pennsylvania", "RI": "Rhode Island", "SC": "South Carolina",
		"SD": "South Dakota", "TN": "Tennessee", "TX": "Texas", "UT": "Utah",
		"VT": "Vermont", "VA": "Virginia", "WA": "Washington", "WV": "West Virginia",
		"WI": "Wisconsin", "WY": "Wyoming", "DC": "District of Columbia",
	}
	up := strings.ToUpper(s)
	if v, ok := m[up]; ok {
		return v, up
	}
	// reverse lookup if full name given
	for k, v := range m {
		if strings.EqualFold(s, v) {
			return v, k
		}
	}
	return s, s
}

func countryNames(c string) (long, short string) {
	switch strings.ToUpper(strings.TrimSpace(c)) {
	case "US", "USA", "UNITED STATES":
		return "United States", "US"
	case "UK", "UNITED KINGDOM", "GB":
		return "United Kingdom", "GB"
	case "CANADA", "CA":
		return "Canada", "CA"
	default:
		return c, c
	}
}

// parseAddressString does best-effort parsing of a comma-separated address
// string (e.g. "10 Durand Rd, Maplewood, NJ 07040, United States") into
// googleAddrComp components matching the Google Places API shape.
func parseAddressString(addr string) []googleAddrComp {
	parts := strings.Split(addr, ", ")
	n := len(parts)
	if n < 2 {
		return nil
	}

	var comps []googleAddrComp
	prepend := func(c googleAddrComp) { comps = append([]googleAddrComp{c}, comps...) }

	// Country (last segment)
	countryLong, countryShort := countryNames(parts[n-1])
	prepend(googleAddrComp{
		LongText: countryLong, ShortText: countryShort,
		Types: []string{"country", "political"}, LanguageCode: "en",
	})

	remaining := parts[:n-1]

	// State + optional ZIP ("NJ 07040" or "NJ")
	if len(remaining) >= 1 {
		sv := remaining[len(remaining)-1]
		remaining = remaining[:len(remaining)-1]
		stateParts := strings.SplitN(sv, " ", 2)
		stateLong, stateShort := stateNames(stateParts[0])
		prepend(googleAddrComp{
			LongText: stateLong, ShortText: stateShort,
			Types: []string{"administrative_area_level_1", "political"}, LanguageCode: "en",
		})
		if len(stateParts) == 2 {
			zip := stateParts[1]
			prepend(googleAddrComp{
				LongText: zip, ShortText: zip,
				Types: []string{"postal_code"}, LanguageCode: "en-US",
			})
		}
	}

	// City
	if len(remaining) >= 1 {
		city := remaining[len(remaining)-1]
		remaining = remaining[:len(remaining)-1]
		prepend(googleAddrComp{
			LongText: city, ShortText: city,
			Types: []string{"locality", "political"}, LanguageCode: "en",
		})
	}

	// Street (anything left)
	if len(remaining) >= 1 {
		street := strings.Join(remaining, ", ")
		if m := streetRe.FindStringSubmatch(street); m != nil {
			prepend(googleAddrComp{
				LongText: m[2], ShortText: shortRoute(m[2]),
				Types: []string{"route"}, LanguageCode: "en",
			})
			prepend(googleAddrComp{
				LongText: m[1], ShortText: m[1],
				Types: []string{"street_number"}, LanguageCode: "en-US",
			})
		} else {
			prepend(googleAddrComp{
				LongText: street, ShortText: street,
				Types: []string{"route"}, LanguageCode: "en",
			})
		}
	}

	return comps
}

// dataIDToPlaceID converts a DataID (e.g. "0x89c3ab...:0xaea7...") into the
// standard Google Place ID (ChIJ...) format.
//
// The ChIJ encoding is a base64-encoded protobuf outer message:
//   field 1 (wire 2, len 18): inner message
//     field 1 (wire 1, fixed64): first hex value (little-endian)
//     field 2 (wire 1, fixed64): second hex value (little-endian)
func dataIDToPlaceID(dataID string) string {
	if !strings.HasPrefix(dataID, "0x") || !strings.Contains(dataID, ":") {
		return dataID
	}
	parts := strings.SplitN(dataID, ":", 2)
	v1, err1 := strconv.ParseUint(strings.TrimPrefix(parts[0], "0x"), 16, 64)
	v2, err2 := strconv.ParseUint(strings.TrimPrefix(parts[1], "0x"), 16, 64)
	if err1 != nil || err2 != nil {
		return dataID
	}

	inner := make([]byte, 18)
	inner[0] = 0x09 // field 1, wire type 1 (fixed64)
	binary.LittleEndian.PutUint64(inner[1:9], v1)
	inner[9] = 0x11 // field 2, wire type 1 (fixed64)
	binary.LittleEndian.PutUint64(inner[10:18], v2)

	outer := make([]byte, 20)
	outer[0] = 0x0A // field 1, wire type 2 (length-delimited)
	outer[1] = 0x12 // length = 18
	copy(outer[2:], inner)

	return base64.RawStdEncoding.EncodeToString(outer)
}

// convertEntryToPlace converts a search-result Entry (from SearchJob / ParseSearchResults)
// into a googlePlace. It only populates fields available from the search listing.
func convertEntryToPlace(e *gmaps.Entry) *googlePlace {
	id := dataIDToPlaceID(e.DataID)
	if id == "" {
		id = e.ID
	}
	p := &googlePlace{
		ID:               id,
		FormattedAddress: e.Address,
		BusinessStatus:   mapBusinessStatus(e.Status),
		Types:            convertTypes(e.Categories, e.Category),
		Rating:           e.ReviewRating,
		UserRatingCount:  e.ReviewCount,
	}
	if e.Title != "" {
		p.DisplayName = &googleLocalizedText{Text: e.Title, LanguageCode: "en"}
	}
	if e.Latitude != 0 || e.Longtitude != 0 {
		p.Location = &googleLocation{Latitude: e.Latitude, Longitude: e.Longtitude}
	}
	if comps := parseAddressString(e.Address); len(comps) > 0 {
		p.AddressComponents = comps
	}
	return p
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

func searchCacheKey(textQuery string, lr *locationRestriction) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%+v", textQuery, lr)))
	return hex.EncodeToString(h[:])
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

// ====================================================================
// searchWriter — collects []*gmaps.Entry from SearchJob results
// ====================================================================

type searchWriter struct {
	mu      sync.Mutex
	entries []*gmaps.Entry
}

func (sw *searchWriter) Run(_ context.Context, in <-chan scrapemate.Result) error {
	for result := range in {
		entries, ok := result.Data.([]*gmaps.Entry)
		if !ok {
			// SearchJob returns []*Entry wrapped as []any sometimes — handle both
			if raw, ok2 := result.Data.([]any); ok2 {
				for _, v := range raw {
					if e, ok3 := v.(*gmaps.Entry); ok3 {
						entries = append(entries, e)
					}
				}
			}
		}
		if len(entries) > 0 {
			sw.mu.Lock()
			sw.entries = append(sw.entries, entries...)
			sw.mu.Unlock()
		}
	}
	return nil
}

// ====================================================================
// scrapemate.ResultWriter that emits Google-format JSON Lines
// ====================================================================

type googleFormatWriter struct {
	mu sync.Mutex
	w  *bufio.Writer
}

func newGoogleFormatWriter(f *os.File) *googleFormatWriter {
	return &googleFormatWriter{w: bufio.NewWriter(f)}
}

func (g *googleFormatWriter) Run(_ context.Context, in <-chan scrapemate.Result) error {
	defer g.w.Flush()

	for result := range in {
		job, ok := result.Job.(scrapemate.IJob)
		if !ok {
			continue
		}
		placeID := job.GetParentID()
		if placeID == "" {
			placeID = job.GetID()
		}

		entries, err := asEntries(result.Data)
		if err != nil {
			log.Printf("[%s] convert: %v", placeID, err)
			continue
		}
		for _, e := range entries {
			gp := convertEntry(e, placeID)
			buf, err := json.Marshal(gp)
			if err != nil {
				log.Printf("[%s] marshal: %v", placeID, err)
				continue
			}
			g.mu.Lock()
			g.w.Write(buf)
			g.w.WriteByte('\n')
			g.w.Flush() // flush per record so streaming consumers see results live
			g.mu.Unlock()
		}
	}
	return nil
}

func asEntries(t any) ([]*gmaps.Entry, error) {
	if t == nil {
		return nil, nil
	}
	if s, ok := t.([]any); ok {
		var out []*gmaps.Entry
		for _, v := range s {
			if e, ok := v.(*gmaps.Entry); ok {
				out = append(out, e)
			}
		}
		return out, nil
	}
	if e, ok := t.(*gmaps.Entry); ok {
		return []*gmaps.Entry{e}, nil
	}
	return nil, nil // ignore non-Entry results (e.g. child review pages)
}

// ====================================================================
// In-memory ResultWriter for HTTP server mode
// ====================================================================

type memWriter struct {
	ch chan *googlePlace
}

func (m *memWriter) Run(_ context.Context, in <-chan scrapemate.Result) error {
	for result := range in {
		job, ok := result.Job.(scrapemate.IJob)
		if !ok {
			continue
		}
		pid := job.GetParentID()
		if pid == "" {
			pid = job.GetID()
		}
		entries, _ := asEntries(result.Data)
		for _, e := range entries {
			select {
			case m.ch <- convertEntry(e, pid):
			default:
			}
		}
	}
	return nil
}

// buildPlaceURL converts a place ID to the right Google Maps URL.
// Handles three formats:
//   - ChIJ...   → place_id: navigation
//   - 0xHEX:0xHEX  → CID decimal navigation (DataID format from SearchJob)
//   - anything else → place_id: navigation (fallback)
func buildPlaceURL(placeID string) string {
	if strings.HasPrefix(placeID, "0x") && strings.Contains(placeID, ":") {
		parts := strings.SplitN(placeID, ":", 2)
		hexCID := strings.TrimPrefix(parts[1], "0x")
		if cid, err := strconv.ParseUint(hexCID, 16, 64); err == nil {
			return fmt.Sprintf("https://maps.google.com/?cid=%d", cid)
		}
	}
	return fmt.Sprintf("https://www.google.com/maps/place/?q=%s",
		url.QueryEscape("place_id:"+placeID))
}

// ====================================================================
// HTTP server mode
// ====================================================================

func runServer(port, concurrency int, langCode string, extractEmail, extraReviews bool, proxies string, inactivity time.Duration) {
	sem := make(chan struct{}, concurrency)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/places/{placeId}", func(w http.ResponseWriter, r *http.Request) {
		placeID := r.PathValue("placeId")
		if placeID == "" {
			http.Error(w, "missing placeId", http.StatusBadRequest)
			return
		}

		// Enforce concurrency cap — return 429 if all slots busy.
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		default:
			http.Error(w, "too many concurrent requests", http.StatusTooManyRequests)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		mem := &memWriter{ch: make(chan *googlePlace, 1)}

		opts := []func(*scrapemateapp.Config) error{
			scrapemateapp.WithConcurrency(1),
			scrapemateapp.WithExitOnInactivity(inactivity),
			scrapemateapp.WithJS(scrapemateapp.DisableImages()),
			scrapemateapp.WithPageReuseLimit(2),
		}
		if proxies != "" {
			opts = append(opts, scrapemateapp.WithProxies(strings.Split(proxies, ",")))
		}

		matecfg, err := scrapemateapp.NewConfig([]scrapemate.ResultWriter{mem}, opts...)
		if err != nil {
			log.Printf("[%s] config: %v", placeID, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		app, err := scrapemateapp.NewScrapeMateApp(matecfg)
		if err != nil {
			log.Printf("[%s] app: %v", placeID, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer app.Close()

		u := buildPlaceURL(placeID)
		job := gmaps.NewPlaceJob(placeID, langCode, u, extractEmail, extraReviews)

		if err := app.Start(ctx, job); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			log.Printf("[%s] scrape: %v", placeID, err)
			http.Error(w, "scrape error", http.StatusInternalServerError)
			return
		}

		select {
		case gp := <-mem.ch:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(gp)
		default:
			http.Error(w, "place not found", http.StatusNotFound)
		}
	})

	mux.HandleFunc("POST /v1/places:searchText", func(w http.ResponseWriter, r *http.Request) {
		var req searchTextRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TextQuery == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Serve from page-token cache
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

		// Build location params
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
			params.Location = gmaps.MapLocation{Lat: 0, Lon: 0, ZoomLvl: 2, Radius: 20_037_000} // half Earth in meters
		}

		sw := &searchWriter{}
		matecfg, err := scrapemateapp.NewConfig(
			[]scrapemate.ResultWriter{sw},
			scrapemateapp.WithConcurrency(1),
			scrapemateapp.WithExitOnInactivity(30*time.Second),
		)
		if err != nil {
			log.Printf("[searchText] config: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		app, err := scrapemateapp.NewScrapeMateApp(matecfg)
		if err != nil {
			log.Printf("[searchText] app: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer app.Close()

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		if err := app.Start(ctx, gmaps.NewSearchJob(params)); err != nil &&
			err != context.Canceled && err != context.DeadlineExceeded {
			log.Printf("[searchText] scrape: %v", err)
		}

		places := make([]*googlePlace, 0, len(sw.entries))
		for _, e := range sw.entries {
			places = append(places, convertEntryToPlace(e))
		}

		key := searchCacheKey(req.TextQuery, req.LocationRestriction)
		searchResultCache.Store(key, &cachedSearch{places: places, createdAt: time.Now()})
		respondSearchPage(w, places, key, 0)
	})

	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       70 * time.Second,
		WriteTimeout:      70 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("placebyid server listening on %s  (Ctrl+C to stop)", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}

var resultsFilePattern = regexp.MustCompile(`^results(\d+)_\d{4}_\d{4}\.(json|csv)$`)

// nextNumberedResultsPath returns results{N}_MMDD_HHMM.ext in dir.
func nextNumberedResultsPath(dir string, jsonOutput bool) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("abs dir %q: %w", dir, err)
	}

	ext := ".csv"
	if jsonOutput {
		ext = ".json"
	}

	log.Printf("[output] auto-naming: scan dir=%s (from %q), ext=%s", absDir, dir, ext)

	maxN := 0
	var matched []string
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return "", fmt.Errorf("read dir %q: %w", absDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := resultsFilePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			log.Printf("[output] skip %q: bad number %q", e.Name(), m[1])
			continue
		}
		matched = append(matched, fmt.Sprintf("%s (N=%d)", e.Name(), n))
		if n > maxN {
			maxN = n
		}
	}

	if len(matched) == 0 {
		log.Printf("[output] no existing results{N}_MMDD_HHMM files in %s", absDir)
	} else {
		log.Printf("[output] matched %d file(s): %s", len(matched), strings.Join(matched, ", "))
	}

	nextN := maxN + 1
	stamp := time.Now().Format("0102_1504")
	name := fmt.Sprintf("results%d_%s%s", nextN, stamp, ext)
	outPath := filepath.Join(absDir, name)
	log.Printf("[output] maxN=%d -> nextN=%d, stamp=%s, file=%s", maxN, nextN, stamp, outPath)
	return outPath, nil
}

// ====================================================================
// main
// ====================================================================

func main() {
	var (
		inputFile    string
		outputFile   string
		concurrency  int
		langCode     string
		extractEmail bool
		extraReviews bool
		proxies      string
		inactivity   time.Duration
		serve        bool
		port         int
	)

	flag.StringVar(&inputFile, "input", "place_ids.txt", "input file with one place_id per line")
	flag.StringVar(&outputFile, "results", "auto", "output: 'auto' (resultsN_MMDD_HHMM.json), 'stdout', or a file path")
	flag.IntVar(&concurrency, "c", 2, "concurrency")
	flag.StringVar(&langCode, "lang", "en", "language code")
	flag.BoolVar(&extractEmail, "email", false, "crawl business website for emails")
	flag.BoolVar(&extraReviews, "extra-reviews", false, "fetch up to ~300 reviews per place")
	flag.StringVar(&proxies, "proxies", "", "comma-separated proxies")
	flag.DurationVar(&inactivity, "exit-on-inactivity", 3*time.Minute, "stop after idle")
	flag.BoolVar(&serve, "serve", false, "run as HTTP API server (GET /v1/places/{placeId})")
	flag.IntVar(&port, "port", 3001, "port for HTTP server (used with -serve)")
	flag.Parse()

	if serve {
		runServer(port, concurrency, langCode, extractEmail, extraReviews, proxies, inactivity)
		return
	}

	// Read place IDs
	f, err := os.Open(inputFile)
	if err != nil {
		log.Fatalf("open input: %v", err)
	}
	var placeIDs []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		placeIDs = append(placeIDs, line)
	}
	f.Close()
	if len(placeIDs) == 0 {
		log.Fatal("no place IDs in input")
	}

	// Output (auto: results1_0519_1430.json, results2_..., etc.)
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}
	log.Printf("[output] cwd=%s, -results=%q", cwd, outputFile)

	out := os.Stdout
	if outputFile != "stdout" {
		path := outputFile
		switch {
		case path == "" || path == "auto":
			log.Printf("[output] mode=auto (empty or \"auto\")")
			path, err = nextNumberedResultsPath(".", true)
			if err != nil {
				log.Fatalf("resolve output path: %v", err)
			}
		default:
			log.Printf("[output] mode=explicit path=%q", path)
			if !filepath.IsAbs(path) {
				path = filepath.Join(cwd, path)
				log.Printf("[output] resolved relative path -> %s", path)
			}
		}
		log.Printf("[output] creating file: %s", path)
		out, err = os.Create(path)
		if err != nil {
			log.Fatalf("create output: %v", err)
		}
		defer out.Close()
		if st, err := out.Stat(); err != nil {
			log.Printf("[output] warn: stat after create: %v", err)
		} else {
			log.Printf("[output] ready: %s (size=%d bytes)", path, st.Size())
		}
	} else {
		log.Printf("[output] mode=stdout")
	}

	writers := []scrapemate.ResultWriter{newGoogleFormatWriter(out)}

	opts := []func(*scrapemateapp.Config) error{
		scrapemateapp.WithConcurrency(concurrency),
		scrapemateapp.WithExitOnInactivity(inactivity),
		scrapemateapp.WithJS(scrapemateapp.DisableImages()),
		scrapemateapp.WithPageReuseLimit(2),
		scrapemateapp.WithPageReuseLimit(200),
	}
	if proxies != "" {
		opts = append(opts, scrapemateapp.WithProxies(strings.Split(proxies, ",")))
	}

	matecfg, err := scrapemateapp.NewConfig(writers, opts...)
	if err != nil {
		log.Fatalf("scrapemate config: %v", err)
	}
	app, err := scrapemateapp.NewScrapeMateApp(matecfg)
	if err != nil {
		log.Fatalf("scrapemate app: %v", err)
	}
	defer app.Close()

	exitMonitor := exiter.New()
	var jobs []scrapemate.IJob
	for _, pid := range placeIDs {
		// place-by-id URL — Google Maps redirects to the place page
		u := fmt.Sprintf("https://www.google.com/maps/place/?q=%s",
			url.QueryEscape("place_id:"+pid))
		job := gmaps.NewPlaceJob(pid, langCode, u, extractEmail, extraReviews,
			gmaps.WithPlaceJobExitMonitor(exitMonitor))
		jobs = append(jobs, job)
	}
	exitMonitor.SetSeedCount(len(jobs))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exitMonitor.SetCancelFunc(cancel)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() { <-sigChan; cancel() }()

	go exitMonitor.Run(ctx)

	fmt.Fprintf(os.Stderr, "scraping %d place(s), concurrency=%d\n", len(jobs), concurrency)
	if err := app.Start(ctx, jobs...); err != nil && err != context.Canceled {
		log.Fatalf("scrape: %v", err)
	}
}