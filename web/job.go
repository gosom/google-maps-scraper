package web

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gosom/google-maps-scraper/grid"
)

var jobs []Job

const (
	StatusPending = "pending"
	StatusWorking = "working"
	StatusOK      = "ok"
	StatusFailed  = "failed"
)

const (
	// OutputFormatCSV selects CSV output (default for empty OutputFormat).
	OutputFormatCSV = "csv"
	// OutputFormatJSON selects JSON output, which preserves structured fields
	// such as the owner object.
	OutputFormatJSON = "json"
)

type SelectParams struct {
	Status string
	Limit  int
	Offset int
}

// JobPage contains one page of jobs and the metadata needed to navigate it.
type JobPage struct {
	Jobs        []Job
	CurrentPage int
	TotalPages  int
	Total       int
	HasPrev     bool
	HasNext     bool
	PrevPage    int
	NextPage    int
	HasPages    bool
}

type JobRepository interface {
	Get(context.Context, string) (Job, error)
	Create(context.Context, *Job) error
	Delete(context.Context, string) error
	Select(context.Context, SelectParams) ([]Job, error)
	Update(context.Context, *Job) error
}

type Job struct {
	ID     string
	Name   string
	Date   time.Time
	Status string
	Data   JobData
}

func (j *Job) Validate() error {
	if j.ID == "" {
		return errors.New("missing id")
	}

	if j.Name == "" {
		return errors.New("missing name")
	}

	if j.Status == "" {
		return errors.New("missing status")
	}

	if j.Date.IsZero() {
		return errors.New("missing date")
	}

	if err := j.Data.Validate(); err != nil {
		return err
	}

	return nil
}

type JobData struct {
	Keywords            []string      `json:"keywords"`
	Lang                string        `json:"lang"`
	Zoom                int           `json:"zoom"`
	Lat                 string        `json:"lat"`
	Lon                 string        `json:"lon"`
	FastMode            bool          `json:"fast_mode"`
	Radius              int           `json:"radius"`
	Depth               int           `json:"depth"`
	Email               bool          `json:"email"`
	ExtraReviews        bool          `json:"extra_reviews"`
	ReviewsMetadataOnly bool          `json:"reviews_metadata_only"`
	MaxTime             time.Duration `json:"max_time"`
	Proxies             []string      `json:"proxies"`

	// OutputFormat selects the result file format. Empty means OutputFormatCSV.
	OutputFormat string `json:"output_format,omitempty"`

	// Per-job scraping options (empty/zero means "use the server default").
	Concurrency      int     `json:"concurrency,omitempty"`
	GridBBox         string  `json:"grid_bbox,omitempty"`
	GridCellKm       float64 `json:"grid_cell_km,omitempty"`
	PagesPerBrowser  int     `json:"pages_per_browser,omitempty"`
	BrowserPoolSize  int     `json:"browser_pool_size,omitempty"`
	DisablePageReuse bool    `json:"disable_page_reuse,omitempty"`
}

// GeoCoordinates returns the "lat,lon" pair used to anchor map searches, or an
// empty string when no usable anchor is set.
//
// Rule: both values must parse as floats, otherwise no anchor is used. The
// (0, 0) pair is treated as "no coordinate" — it was the form's historical
// default and only ever produced searches anchored in the Gulf of Guinea. A
// genuine 0 on one axis is preserved when the partner axis is non-zero (e.g.
// a location on the equator or the prime meridian).
func (d *JobData) GeoCoordinates() string {
	lat, latErr := strconv.ParseFloat(strings.TrimSpace(d.Lat), 64)
	if latErr != nil {
		return ""
	}

	lon, lonErr := strconv.ParseFloat(strings.TrimSpace(d.Lon), 64)
	if lonErr != nil {
		return ""
	}

	if lat == 0 && lon == 0 {
		return ""
	}

	return strings.TrimSpace(d.Lat) + "," + strings.TrimSpace(d.Lon)
}

// EffectiveOutputFormat returns the concrete output format, defaulting to CSV
// so that jobs stored before the field existed keep their CSV behaviour.
func (d *JobData) EffectiveOutputFormat() string {
	if d.OutputFormat == OutputFormatJSON {
		return OutputFormatJSON
	}

	return OutputFormatCSV
}

func (d *JobData) Validate() error {
	if len(d.Keywords) == 0 {
		return errors.New("missing keywords")
	}

	if d.Lang == "" {
		return errors.New("missing lang")
	}

	if len(d.Lang) != 2 {
		return errors.New("invalid lang")
	}

	if d.Depth == 0 {
		return errors.New("missing depth")
	}

	if d.MaxTime == 0 {
		return errors.New("missing max time")
	}

	if d.FastMode && (d.Lat == "" || d.Lon == "") {
		return errors.New("missing geo coordinates")
	}

	if (d.Lat == "") != (d.Lon == "") {
		return errors.New("latitude and longitude must both be set or both be empty")
	}

	if d.OutputFormat != "" && d.OutputFormat != OutputFormatCSV && d.OutputFormat != OutputFormatJSON {
		return errors.New("invalid output format: must be csv or json")
	}

	if d.Concurrency < 0 {
		return errors.New("concurrency must not be negative")
	}

	if d.PagesPerBrowser < 0 {
		return errors.New("pages per browser must not be negative")
	}

	if d.BrowserPoolSize < 0 {
		return errors.New("browser pool size must not be negative")
	}

	if d.GridCellKm < 0 {
		return errors.New("grid cell size must not be negative")
	}

	if d.GridBBox != "" {
		if d.FastMode {
			return errors.New("grid bounding box cannot be used together with fast mode")
		}

		if _, err := grid.ParseBoundingBox(d.GridBBox); err != nil {
			return err
		}
	}

	return nil
}
