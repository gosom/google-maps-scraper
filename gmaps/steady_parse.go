package gmaps

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Collector v2 reads Google Maps the way its own page does, without a browser: the search page
// for a query names the results request Google's app makes next (a /search?tbm=map URL keyed on
// the query's words, so no coordinates are needed), and that request answers with the first 20
// places as JSON. Asking again with an offset gives the next 20.

// steadyPageSize is how many places one results page holds.
const steadyPageSize = 20

// errNoResultsRequest is a search page that does not name its results request: a consent wall,
// a refusal Google served with a 200, or a page layout that changed.
var errNoResultsRequest = errors.New("the search page names no results request")

var resultsRequestRe = regexp.MustCompile(`href="(/search\?tbm=map[^"]*)"`)

// steadySearchPageURL is the Google Maps search page for a query.
func steadySearchPageURL(query, lang string) string {
	u := "https://www.google.com/maps/search/" + url.QueryEscape(strings.TrimSpace(query))
	if lang != "" {
		u += "?hl=" + url.QueryEscape(lang)
	}

	return u
}

// resultsRequestURL is the results request a search page names, as an absolute URL.
func resultsRequestURL(page []byte) (string, error) {
	m := resultsRequestRe.FindSubmatch(page)
	if m == nil {
		return "", errNoResultsRequest
	}

	return "https://www.google.com" + html.UnescapeString(string(m[1])), nil
}

// resultsPageURL is the results request for the page starting at offset: Google's "!7i20" (20 a
// page) followed by "!8i<offset>". An offset of 0 is the request as the page named it.
func resultsPageURL(first string, offset int) (string, error) {
	if offset == 0 {
		return first, nil
	}

	for _, size := range []string{"%217i20", "!7i20"} {
		if i := strings.Index(first, size); i >= 0 {
			bang := size[:len(size)-len("7i20")]

			return first[:i+len(size)] + bang + "8i" + strconv.Itoa(offset) + first[i+len(size):], nil
		}
	}

	return "", errors.New("the results request has no page size to page from")
}

// parseResultsPage reads the places in one results response. Each place is Google's usual place
// array (the one a place page carries), so it is read by EntryFromJSON like any other; the places
// are found by their shape, which survives Google moving the list around the response.
func parseResultsPage(body []byte) ([]*Entry, error) {
	if i := bytes.IndexByte(body, '\n'); bytes.HasPrefix(body, []byte(")]}'")) && i >= 0 {
		body = body[i+1:]
	}

	var root []any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("results page is not JSON: %w", err)
	}

	var places [][]any

	collectPlaces(root, &places)

	entries := make([]*Entry, 0, len(places))

	for _, p := range places {
		raw, err := json.Marshal([]any{nil, nil, nil, nil, nil, nil, p})
		if err != nil {
			continue
		}

		entry, err := EntryFromJSON(raw)
		if err != nil || entry.Title == "" {
			continue
		}

		if entry.Link == "" {
			entry.Link = placeLink(&entry)
		}

		entries = append(entries, &entry)
	}

	return entries, nil
}

// placeArrayMinLen is how long an array must be to hold a place's id at index 78.
const placeArrayMinLen = 79

// collectPlaces finds every place array under v: a long array with a name at 11 and a data id
// ("0x...:0x...") at 10. It does not look inside a place, so its own nested arrays are not taken
// for places.
func collectPlaces(v any, out *[][]any) {
	arr, ok := v.([]any)
	if !ok {
		return
	}

	if len(arr) >= placeArrayMinLen {
		name, _ := arr[11].(string)
		dataID, _ := arr[10].(string)

		if name != "" && strings.HasPrefix(dataID, "0x") {
			*out = append(*out, arr)

			return
		}
	}

	for _, child := range arr {
		collectPlaces(child, out)
	}
}

// placeLink is a Google Maps link for a place whose array carries none.
func placeLink(e *Entry) string {
	switch {
	case e.PlaceID != "":
		return "https://www.google.com/maps/place/?q=place_id:" + e.PlaceID
	case e.DataID != "":
		return "https://www.google.com/maps/place/data=!4m2!3m1!1s" + e.DataID
	default:
		return ""
	}
}

// placeKey is what makes two sightings the same place.
func placeKey(e *Entry) string {
	if e.PlaceID != "" {
		return e.PlaceID
	}

	if e.DataID != "" {
		return e.DataID
	}

	return e.Link
}
