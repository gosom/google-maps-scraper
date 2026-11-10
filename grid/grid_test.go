package grid_test

import (
	"math"
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/grid"
)

func TestParseBoundingBox(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{
			name: "valid",
			in:   "40.30,-3.80,40.50,-3.60",
		},
		{
			name:    "invalid min latitude",
			in:      "-91,0,10,10",
			wantErr: "minLat",
		},
		{
			name:    "invalid max longitude",
			in:      "10,0,20,181",
			wantErr: "maxLon",
		},
		{
			name:    "non finite value",
			in:      "NaN,0,10,10",
			wantErr: "must be finite",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := grid.ParseBoundingBox(tc.in)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
				}
			}
		})
	}
}

func TestGenerateCellsAndEstimateCellCountNearPoles(t *testing.T) {
	t.Parallel()

	bbox := grid.BoundingBox{
		MinLat: 89.90,
		MinLon: 0.00,
		MaxLat: 89.95,
		MaxLon: 20.00,
	}

	cellSizeKm := 1.0

	gotCells := grid.GenerateCells(bbox, cellSizeKm)
	if len(gotCells) == 0 {
		t.Fatal("expected cells to be generated near poles")
	}

	for _, c := range gotCells {
		if math.IsNaN(c.Lat) || math.IsInf(c.Lat, 0) || math.IsNaN(c.Lon) || math.IsInf(c.Lon, 0) {
			t.Fatalf("expected finite cell coordinates, got %+v", c)
		}
	}

	gotCount := grid.EstimateCellCount(bbox, cellSizeKm)
	if gotCount != len(gotCells) {
		t.Fatalf("expected EstimateCellCount=%d to match generated cells=%d", gotCount, len(gotCells))
	}
}

func TestGenerateCellsCoversWholeBoundingBox(t *testing.T) {
	t.Parallel()

	const cellSizeKm = 1.0

	tests := []struct {
		name string
		bbox grid.BoundingBox
	}{
		{
			// Smaller than half a cell in both directions.
			name: "smaller than one cell",
			bbox: grid.BoundingBox{MinLat: 40.400, MinLon: -3.700, MaxLat: 40.402, MaxLon: -3.698},
		},
		{
			// About 1.2 cells tall and 2.3 cells wide: the remainder is less
			// than half a cell on both sides.
			name: "partial last row and column",
			bbox: grid.BoundingBox{MinLat: 0, MinLon: 0, MaxLat: 0.0108, MaxLon: 0.0207},
		},
		{
			name: "madrid example",
			bbox: grid.BoundingBox{MinLat: 40.30, MinLon: -3.80, MaxLat: 40.50, MaxLon: -3.60},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cells := grid.GenerateCells(tc.bbox, cellSizeKm)
			if len(cells) == 0 {
				t.Fatal("expected at least one cell")
			}

			if got := grid.EstimateCellCount(tc.bbox, cellSizeKm); got != len(cells) {
				t.Fatalf("EstimateCellCount=%d, GenerateCells produced %d cells", got, len(cells))
			}

			// Half a cell in degrees: no point of the box may be further than
			// this from the nearest centre along either axis.
			halfLat := cellSizeKm / 111.32 / 2
			halfLon := halfLat / math.Cos((tc.bbox.MinLat+tc.bbox.MaxLat)/2*math.Pi/180)
			const eps = 1e-12

			minLat, maxLat := math.Inf(1), math.Inf(-1)
			minLon, maxLon := math.Inf(1), math.Inf(-1)

			for _, c := range cells {
				if c.Lat < tc.bbox.MinLat || c.Lat > tc.bbox.MaxLat || c.Lon < tc.bbox.MinLon || c.Lon > tc.bbox.MaxLon {
					t.Fatalf("cell centre %+v is outside the bounding box %+v", c, tc.bbox)
				}

				minLat, maxLat = math.Min(minLat, c.Lat), math.Max(maxLat, c.Lat)
				minLon, maxLon = math.Min(minLon, c.Lon), math.Max(maxLon, c.Lon)
			}

			if minLat-tc.bbox.MinLat > halfLat+eps || tc.bbox.MaxLat-maxLat > halfLat+eps {
				t.Fatalf("cells leave a latitude strip uncovered: centres span [%f, %f] in [%f, %f]",
					minLat, maxLat, tc.bbox.MinLat, tc.bbox.MaxLat)
			}

			if minLon-tc.bbox.MinLon > halfLon+eps || tc.bbox.MaxLon-maxLon > halfLon+eps {
				t.Fatalf("cells leave a longitude strip uncovered: centres span [%f, %f] in [%f, %f]",
					minLon, maxLon, tc.bbox.MinLon, tc.bbox.MaxLon)
			}
		})
	}
}
