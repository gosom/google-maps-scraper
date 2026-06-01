package main

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gosom/google-maps-scraper/gmaps"
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
//
//	field 1 (wire 2, len 18): inner message
//	  field 1 (wire 1, fixed64): first hex value (little-endian)
//	  field 2 (wire 1, fixed64): second hex value (little-endian)
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
