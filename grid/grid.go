// Package grid provides utilities to divide a geographic bounding box into a
// grid of smaller cells. This is useful for overcoming Google Maps' ~120
// results-per-search limit: by splitting a large area into many small cells
// and issuing one search per cell, you can retrieve far more results.
package grid

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const kmPerDegreeLat = 111.32
const minCosLatitude = 1e-6

// BoundingBox represents a geographic rectangle defined by two corners.
type BoundingBox struct {
	MinLat float64
	MinLon float64
	MaxLat float64
	MaxLon float64
}

// ParseBoundingBox parses a string with format "minLat,minLon,maxLat,maxLon".
// Example: "40.30,-3.80,40.50,-3.60"
func ParseBoundingBox(s string) (BoundingBox, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return BoundingBox{}, fmt.Errorf("invalid bounding box %q: expected format minLat,minLon,maxLat,maxLon", s)
	}

	vals := make([]float64, 4)

	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return BoundingBox{}, fmt.Errorf("invalid bounding box value %q: %w", p, err)
		}

		if math.IsNaN(v) || math.IsInf(v, 0) {
			return BoundingBox{}, fmt.Errorf("invalid bounding box value %q: must be finite", p)
		}

		vals[i] = v
	}

	bbox := BoundingBox{
		MinLat: vals[0],
		MinLon: vals[1],
		MaxLat: vals[2],
		MaxLon: vals[3],
	}

	if bbox.MinLat >= bbox.MaxLat {
		return BoundingBox{}, fmt.Errorf("minLat (%f) must be less than maxLat (%f)", bbox.MinLat, bbox.MaxLat)
	}

	if bbox.MinLon >= bbox.MaxLon {
		return BoundingBox{}, fmt.Errorf("minLon (%f) must be less than maxLon (%f)", bbox.MinLon, bbox.MaxLon)
	}

	if bbox.MinLat < -90 || bbox.MinLat > 90 {
		return BoundingBox{}, fmt.Errorf("minLat (%f) must be between -90 and 90", bbox.MinLat)
	}

	if bbox.MaxLat < -90 || bbox.MaxLat > 90 {
		return BoundingBox{}, fmt.Errorf("maxLat (%f) must be between -90 and 90", bbox.MaxLat)
	}

	if bbox.MinLon < -180 || bbox.MinLon > 180 {
		return BoundingBox{}, fmt.Errorf("minLon (%f) must be between -180 and 180", bbox.MinLon)
	}

	if bbox.MaxLon < -180 || bbox.MaxLon > 180 {
		return BoundingBox{}, fmt.Errorf("maxLon (%f) must be between -180 and 180", bbox.MaxLon)
	}

	return bbox, nil
}

// Cell represents the center point of a grid cell.
type Cell struct {
	Lat float64
	Lon float64
}

// GeoCoordinates returns the cell center in "lat,lon" format, ready to pass
// to gmaps.NewGmapJob as the geoCoordinates parameter.
func (c Cell) GeoCoordinates() string {
	return fmt.Sprintf("%f,%f", c.Lat, c.Lon)
}

// GenerateCells divides bbox into a grid where each cell is approximately
// cellSizeKm × cellSizeKm. It returns the center point of every cell.
//
// The longitude step is adjusted for the latitude of the bounding box centre
// so that cells are roughly square on the ground.
//
// Example: a 20×20 km area with cellSizeKm=1 produces ~400 cells.
func GenerateCells(bbox BoundingBox, cellSizeKm float64) []Cell {
	cellSizeKm = normalizeCellSizeKm(cellSizeKm)

	// Latitude step is constant everywhere.
	latStep := cellSizeKm / kmPerDegreeLat

	// Longitude step varies with latitude; use the midpoint for a good estimate.
	lonStep := calculateLonStep(bbox, cellSizeKm)

	var cells []Cell

	// Start at the centre of the first cell (half a step from the edge).
	for lat := bbox.MinLat + latStep/2; lat < bbox.MaxLat; lat += latStep {
		for lon := bbox.MinLon + lonStep/2; lon < bbox.MaxLon; lon += lonStep {
			cells = append(cells, Cell{Lat: lat, Lon: lon})
		}
	}

	return cells
}

// EstimateCellCount returns how many cells GenerateCells would produce
// without allocating them. Useful for logging or validation.
func EstimateCellCount(bbox BoundingBox, cellSizeKm float64) int {
	cellSizeKm = normalizeCellSizeKm(cellSizeKm)

	latStep := cellSizeKm / kmPerDegreeLat
	lonStep := calculateLonStep(bbox, cellSizeKm)

	latCells := int(math.Ceil((bbox.MaxLat - bbox.MinLat) / latStep))
	lonCells := int(math.Ceil((bbox.MaxLon - bbox.MinLon) / lonStep))

	if latCells < 0 {
		latCells = 0
	}

	if lonCells < 0 {
		lonCells = 0
	}

	return latCells * lonCells
}

func normalizeCellSizeKm(cellSizeKm float64) float64 {
	if cellSizeKm <= 0 {
		return 1.0
	}

	return cellSizeKm
}

func calculateLonStep(bbox BoundingBox, cellSizeKm float64) float64 {
	midLat := (bbox.MinLat + bbox.MaxLat) / 2
	cosMidLat := math.Cos(midLat * math.Pi / 180)

	if math.Abs(cosMidLat) < minCosLatitude {
		if cosMidLat < 0 {
			cosMidLat = -minCosLatitude
		} else {
			cosMidLat = minCosLatitude
		}
	}

	return cellSizeKm / (kmPerDegreeLat * cosMidLat)
}
