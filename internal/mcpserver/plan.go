package mcpserver

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/gosom/google-maps-scraper/grid"
	"golang.org/x/text/unicode/norm"
)

//go:embed cities.tsv.gz
var cityIndex []byte

//go:embed additional-places.tsv
var additionalPlaces string

const (
	modeFast     = "fast"
	modeDetailed = "detailed"
)

const (
	defaultLimit = 50
	maxLimit     = 1000
	maxCells     = 64
	cellSizeKM   = 1.0
)

type point struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type startInput struct {
	Response      string   `json:"response,omitempty" jsonschema:"preview for results, export for paths only, live for first rows, job (default) for immediate background startup."`
	Query         string   `json:"query,omitempty" jsonschema:"Business category or keyword"`
	Location      string   `json:"location,omitempty" jsonschema:"Place name, optionally followed by country code"`
	LocationID    string   `json:"location_id,omitempty" jsonschema:"ID returned by a location clarification"`
	Center        *point   `json:"center,omitempty"`
	Bounds        string   `json:"bounds,omitempty" jsonschema:"Explicit minLat,minLon,maxLat,maxLon"`
	RadiusKM      float64  `json:"radius_km,omitempty"`
	Mode          string   `json:"mode,omitempty" jsonschema:"auto (default) prefers fast, including for emails. Review text uses detailed. Normal means detailed."`
	Coverage      string   `json:"coverage,omitempty" jsonschema:"Omit for a named city, even with a small limit. Use standard for a centered nearby search or broad to request a grid."`
	GridCellKM    float64  `json:"grid_cell_km,omitempty" jsonschema:"Optional coarser grid spacing in km for large areas"`
	Fields        []string `json:"fields,omitempty" jsonschema:"Requested preview fields, e.g. phone or website. Use emails, reviews, extra_reviews, reviews_per_rating, or owner for enrichment."`
	Limit         int      `json:"limit,omitempty" jsonschema:"Maximum businesses in the full export. The preview defaults to three."`
	RequestID     string   `json:"request_id,omitempty" jsonschema:"Reuse this value when retrying the same start request"`
	PreviousJobID string   `json:"previous_job_id,omitempty" jsonschema:"Rerun this job with the same geography and optional mode override"`
}

type location struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Region      string `json:"region"`
	Country     string `json:"country"`
	Center      point  `json:"center"`
	CountryName string `json:"country_name"`
	population  int
}

type plan struct {
	Fields          []string          `json:"fields,omitempty"`
	Query           string            `json:"query"`
	Location        string            `json:"location,omitempty"`
	Mode            string            `json:"mode"`
	Email           bool              `json:"include_emails"`
	ExtraReviews    bool              `json:"extra_reviews"`
	RequiresDetails bool              `json:"requires_details"`
	Center          point             `json:"center"`
	RadiusKM        float64           `json:"radius_km,omitempty"`
	Bounds          *grid.BoundingBox `json:"bounds,omitempty"`
	Cells           []grid.Cell       `json:"cells"`
	CellSizeKM      float64           `json:"cell_size_km,omitempty"`
	Approximate     bool              `json:"approximate_area,omitempty"`
	Limit           int               `json:"limit"`
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}

		return r
	}, norm.NFD.String(value))))
}

func findLocations(name, id string) ([]location, error) {
	reader, err := gzip.NewReader(bytes.NewReader(cityIndex))
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	name, qualifier, _ := strings.Cut(name, ",")
	name, qualifier = normalize(name), normalize(qualifier)

	var matches []location

	scanner := bufio.NewScanner(io.MultiReader(reader, strings.NewReader(additionalPlaces)))
	for scanner.Scan() {
		f := strings.SplitN(scanner.Text(), "\t", 9)
		if len(f) != 9 {
			continue
		}

		switch {
		case id != "":
			if f[0] != id {
				continue
			}
		case qualifier != "" && qualifier != normalize(f[2]) && qualifier != normalize(f[3]) && qualifier != normalize(f[4]):
			continue
		default:
			found := false

			for alias := range strings.SplitSeq(f[8], ",") {
				if normalize(alias) == name {
					found = true
					break
				}
			}

			if !found {
				continue
			}
		}

		lat, _ := strconv.ParseFloat(f[5], 64)
		lon, _ := strconv.ParseFloat(f[6], 64)
		population, _ := strconv.Atoi(f[7])
		matches = append(matches, location{ID: f[0], Name: f[1], Region: f[4], Country: f[2], CountryName: f[3], Center: point{lat, lon}, population: population})
	}

	slices.SortStableFunc(matches, func(a, b location) int { return b.population - a.population })

	return matches[:min(len(matches), 5)], scanner.Err()
}

func validPoint(p point) bool {
	return p.Lat >= -90 && p.Lat <= 90 && p.Lon >= -180 && p.Lon <= 180
}

func modeFor(mode string, requiresDetails bool) (string, error) {
	switch mode {
	case "", "auto":
		if requiresDetails {
			return modeDetailed, nil
		}

		return modeFast, nil
	case "normal", modeDetailed:
		return modeDetailed, nil
	case modeFast:
		if requiresDetails {
			return "", errors.New("requested listing details require detailed mode; use detailed or request ratings and review counts in fast mode")
		}

		return modeFast, nil
	default:
		return "", errors.New("mode must be auto, fast, or detailed")
	}
}

func makePlan(in *startInput) (plan, []location, error) {
	p := plan{Fields: in.Fields, Query: strings.TrimSpace(in.Query), Limit: in.Limit, RadiusKM: in.RadiusKM}

	if p.Query == "" || (strings.ContainsAny(p.Query, "\r\n") || strings.Contains(p.Query, "#!#")) || len(p.Query) > 500 {
		return p, nil, errors.New("provide a single business query of up to 500 bytes")
	}

	if p.Limit == 0 {
		p.Limit = defaultLimit
	}

	if p.Limit < 1 || p.Limit > maxLimit {
		return p, nil, fmt.Errorf("limit must be between 1 and %d", maxLimit)
	}

	for _, field := range in.Fields {
		switch fieldKey(field) {
		case fieldEmails:
			p.Email = true
		case fieldReviews, "reviews_per_rating", "owner":
			p.RequiresDetails = true
		case fieldExtraReviews:
			p.RequiresDetails, p.ExtraReviews = true, true
		}
	}

	mode, err := modeFor(in.Mode, p.RequiresDetails)
	if err != nil {
		return p, nil, err
	}

	p.Mode = mode
	if in.Coverage != "" && in.Coverage != "standard" && in.Coverage != "broad" {
		return p, nil, errors.New("coverage must be standard or broad")
	}

	if !(in.RadiusKM >= 0 && in.RadiusKM <= 100) {
		return p, nil, errors.New("radius_km must be between 0 and 100")
	}

	broad := in.Coverage == "broad" || (in.Bounds == "" && in.Center == nil && in.Coverage == "" && in.RadiusKM == 0)

	choices, err := p.resolveArea(in, broad)
	if err != nil || len(choices) > 0 {
		return p, choices, err
	}

	err = p.setCells(in.GridCellKM, broad)

	return p, nil, err
}

func (p *plan) resolveArea(in *startInput, broad bool) ([]location, error) {
	switch {
	case in.Bounds != "":
		bounds, err := grid.ParseBoundingBox(in.Bounds)
		if err != nil {
			return nil, err
		}

		if in.RadiusKM != 0 || in.Center != nil {
			return nil, errors.New("supply either bounds or a center with radius")
		}

		p.Bounds = &bounds
		p.Center = point{(bounds.MinLat + bounds.MaxLat) / 2, (bounds.MinLon + bounds.MaxLon) / 2}
		p.Location = in.Location
	case in.Center != nil:
		p.Center = *in.Center
		p.Location = in.Location
	default:
		if in.Location == "" && in.LocationID == "" {
			return nil, errors.New("provide a place name, center, or bounds")
		}

		matches, err := findLocations(in.Location, in.LocationID)
		if err != nil {
			return nil, err
		}

		if len(matches) == 0 {
			return nil, errors.New("place not found; try a city with an ISO country code, map coordinates, or explicit bounds")
		}

		if len(matches) > 1 {
			return matches, nil
		}

		resolved := matches[0]
		p.Center = resolved.Center
		p.Location = resolved.Name + ", " + resolved.CountryName

		if broad && in.RadiusKM == 0 {
			p.Approximate = true
			p.RadiusKM = cityRadiusKM(resolved.population)
		}
	}

	return nil, nil
}

func (p *plan) setCells(gridCellKM float64, broad bool) error {
	if !validPoint(p.Center) {
		return errors.New("center has invalid latitude or longitude")
	}

	if p.Bounds == nil && p.RadiusKM == 0 {
		p.RadiusKM = 3
	}

	if p.Bounds == nil && !broad {
		if gridCellKM != 0 {
			return errors.New("grid_cell_km requires broad coverage or explicit bounds")
		}

		p.Cells = []grid.Cell{{Lat: p.Center.Lat, Lon: p.Center.Lon}}

		return nil
	}

	box := p.Bounds
	if box == nil {
		latDelta := p.RadiusKM / 111.0

		lonDelta := p.RadiusKM / (111.0 * math.Cos((math.Abs(p.Center.Lat)+latDelta)*math.Pi/180))
		if lonDelta <= 0 || !validPoint(point{p.Center.Lat - latDelta, p.Center.Lon - lonDelta}) ||
			!validPoint(point{p.Center.Lat + latDelta, p.Center.Lon + lonDelta}) {
			return errors.New("grid crosses the poles or antimeridian; provide bounds for one side")
		}

		box = &grid.BoundingBox{MinLat: p.Center.Lat - latDelta, MaxLat: p.Center.Lat + latDelta, MinLon: p.Center.Lon - lonDelta, MaxLon: p.Center.Lon + lonDelta}
	}

	spacing := gridCellKM
	if spacing == 0 {
		spacing = cellSizeKM
	}

	if p.Approximate && gridCellKM == 0 {
		spacing = math.Ceil(p.RadiusKM / 2)
	}

	if !(spacing >= 0.25 && spacing <= 10) {
		return errors.New("grid_cell_km must be between 0.25 and 10")
	}

	count := grid.EstimateCellCount(*box, spacing)
	if count < 1 || count > maxCells {
		for size := math.Ceil(spacing + 0.01); size <= 10; size++ {
			coarser := grid.EstimateCellCount(*box, size)
			if coarser > 0 && coarser <= maxCells {
				if p.Approximate && gridCellKM == 0 {
					p.Cells = grid.GenerateCells(*box, size)
					p.CellSizeKM = size

					return nil
				}

				return fmt.Errorf("area requires %d cells at %.2f km; narrow the area or use %.0f km spacing for %d cells", count, spacing, size, coarser)
			}
		}

		return fmt.Errorf("area requires %d cells at %.2f km; narrow the area to at most %d cells", count, spacing, maxCells)
	}

	p.Cells = grid.GenerateCells(*box, spacing)
	p.CellSizeKM = spacing

	return nil
}

// cityRadiusKM estimates a search area when the index provides only a point.
func cityRadiusKM(population int) float64 {
	switch {
	case population >= 500_000:
		return 8
	case population >= 100_000:
		return 5
	default:
		return 3
	}
}

func distanceKM(a, b point) float64 {
	const radians = math.Pi / 180

	dlat, dlon := (b.Lat-a.Lat)*radians, (b.Lon-a.Lon)*radians
	h := math.Pow(math.Sin(dlat/2), 2) + math.Cos(a.Lat*radians)*math.Cos(b.Lat*radians)*math.Pow(math.Sin(dlon/2), 2)

	return 12742 * math.Asin(math.Sqrt(min(1, h)))
}
