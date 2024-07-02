package utils

import (
	"errors"
	"fmt"

	"github.com/gosom/kit/logging"
	"github.com/uber/h3-go/v4"
)

// GenerateH3Listing creates H3 indices for each vertex of a polygon at level 12.
// Input polygon(location listing)
// Output point center h3 lising
func GenerateH3Listing(req [][]float64, resolution int) ([][]float64, error) {
	if resolution < 1 {
		return nil, errors.New("resolution must be greater than 0")
	}
	// Convert req to h3.GeoPolygon
	geoPolygon := h3.GeoPolygon{
		GeoLoop: make(h3.GeoLoop, len(req)),
	}
	for i, point := range req {
		geoPolygon.GeoLoop[i] = h3.NewLatLng(point[1], point[0])
	}

	//Generate geoPolygon to Cells
	cells := geoPolygon.Cells(resolution)

	// Convert cells to [][]float64
	h3Listing := make([][]float64, len(cells))
	for i, cell := range cells {
		latLng := cell.LatLng()
		h3Listing[i] = []float64{latLng.Lat, latLng.Lng}
	}
	logging.Info("GenerateH3Listing %+v", h3Listing)
	return h3Listing, nil
}

// LocationToString convert []float64 to string like 10.7773285,106.6864011
func LocationToString(req []float64) string {
	if len(req) == 0 {
		return ""
	}
	return fmt.Sprintf("%f,%f", req[0], req[1])
}
