package web

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gosom/google-maps-scraper/gmaps"
)

// ErrPlacesNotFound is returned by GetPlaces when the job's CSV output does not
// exist. Callers use it to distinguish a missing job (404) from other errors.
var ErrPlacesNotFound = errors.New("places not found")

// Place is a single map-able result extracted from a job's CSV output.
type Place struct {
	Title        string  `json:"title"`
	Address      string  `json:"address"`
	Latitude     float64 `json:"latitude"`
	Longitude    float64 `json:"longitude"`
	Link         string  `json:"link"`
	Category     string  `json:"category"`
	Phone        string  `json:"phone"`
	Website      string  `json:"website"`
	ReviewRating float64 `json:"review_rating"`
}

// GetPlaces locates the job's result output and parses it into mappable
// places. In web mode each job writes exactly one {id}.csv or {id}.json, so
// that file is the single source of truth for the map.
func (s *Service) GetPlaces(_ context.Context, id string) ([]Place, error) {
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return nil, fmt.Errorf("invalid file name")
	}

	csvPath := filepath.Join(s.dataFolder, id+".csv")
	if f, err := os.Open(csvPath); err == nil { //nolint:gosec // path is joined from a validated id.
		defer func() {
			_ = f.Close()
		}()

		return parsePlaces(f)
	}

	jsonPath := filepath.Join(s.dataFolder, id+".json")
	f, err := os.Open(jsonPath) //nolint:gosec // path is joined from a validated id.
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("result file not found for job %s: %w", id, ErrPlacesNotFound)
		}

		return nil, err
	}

	defer func() {
		_ = f.Close()
	}()

	return parsePlacesJSON(f)
}

// parsePlaces reads scraped results from a CSV stream and returns the places
// that have valid coordinates. Columns are resolved by header name so the
// parser tolerates reordering; the names mirror gmaps.Entry.CsvHeaders().
func parsePlaces(r io.Reader) ([]Place, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return []Place{}, nil
		}

		return nil, err
	}

	col := make(map[string]int, len(header))
	for i, name := range header {
		col[name] = i
	}

	get := func(row []string, name string) string {
		idx, ok := col[name]
		if !ok || idx >= len(row) {
			return ""
		}

		return row[idx]
	}

	places := []Place{}

	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, err
		}

		lat, errLat := strconv.ParseFloat(get(row, "latitude"), 64)
		lon, errLon := strconv.ParseFloat(get(row, "longitude"), 64)

		if errLat != nil || errLon != nil {
			continue
		}

		if !finite(lat) || !finite(lon) {
			continue
		}

		if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
			continue
		}

		if lat == 0 && lon == 0 {
			continue
		}

		rating, _ := strconv.ParseFloat(get(row, "review_rating"), 64)
		if !finite(rating) {
			rating = 0
		}

		places = append(places, Place{
			Title:        get(row, "title"),
			Address:      get(row, "address"),
			Latitude:     lat,
			Longitude:    lon,
			Link:         get(row, "link"),
			Category:     get(row, "category"),
			Phone:        get(row, "phone"),
			Website:      get(row, "website"),
			ReviewRating: rating,
		})
	}

	return places, nil
}

// parsePlacesJSON reads scraped results from a JSON stream and returns the
// places that have valid coordinates. The stream is newline-delimited JSON
// (one gmaps.Entry object per line), which is how jsonwriter emits results.
func parsePlacesJSON(r io.Reader) ([]Place, error) {
	decoder := json.NewDecoder(r)

	places := []Place{}

	for {
		var entry gmaps.Entry
		if err := decoder.Decode(&entry); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, err
		}

		lat, lon := entry.Latitude, entry.Longtitude

		if !finite(lat) || !finite(lon) {
			continue
		}

		if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
			continue
		}

		if lat == 0 && lon == 0 {
			continue
		}

		rating := entry.ReviewRating
		if !finite(rating) {
			rating = 0
		}

		category := entry.Category
		if category == "" && len(entry.Categories) > 0 {
			category = entry.Categories[0]
		}

		places = append(places, Place{
			Title:        entry.Title,
			Address:      entry.Address,
			Latitude:     lat,
			Longitude:    lon,
			Link:         entry.Link,
			Category:     category,
			Phone:        entry.Phone,
			Website:      entry.WebSite,
			ReviewRating: rating,
		})
	}

	return places, nil
}

// finite reports whether f is a usable, real number (not NaN or ±Inf).
func finite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}
