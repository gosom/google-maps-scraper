package web

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
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

// GetPlaces locates the job's CSV output and parses it into mappable places.
// In web mode each job writes exactly one {id}.csv, so that file is the single
// source of truth for the map.
func (s *Service) GetPlaces(_ context.Context, id string) ([]Place, error) {
	path, err := s.csvPath(id)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("csv file not found for job %s: %w", id, ErrPlacesNotFound)
		}

		return nil, err
	}

	defer func() {
		_ = f.Close()
	}()

	return parsePlaces(f)
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

// finite reports whether f is a usable, real number (not NaN or ±Inf).
func finite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}
