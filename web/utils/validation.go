package utils

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gosom/google-maps-scraper/models"
)

// Local aliases for the cap constants — the actual source of truth lives in
// cap_params.go. These exist so future readers of validation.go don't need
// to chase the value through cap_params.go.
const (
	minDepth      = 1
	maxKeywords   = 5
	maxKeywordLen = 200
	minMaxTime    = 60 * time.Second
	maxMaxTime    = time.Duration(CapMaxTimeSeconds) * time.Second
)

// ApplyJobDataDefaults fills in safe defaults for any zero-valued OPTIONAL
// fields. Call this at the API entry point AFTER JSON decode and BEFORE
// struct-tag validation. Required fields (Keywords, Lang) are not touched —
// missing them is still a 400.
//
// Defaults are conservative (REST best practice / OWASP API4:2023). The
// frontend is responsible for sending generous "no cap" values when the
// user picks the corresponding UX toggles. See cap_params.go for the
// full design rationale.
//
// MaxTime convention: this function treats MaxTime as a "seconds value"
// stored in time.Duration (i.e., a Duration of 1800 means "1800 seconds"
// even though numerically that's 1800 nanoseconds). The HTTP handler
// multiplies by time.Second AFTER calling this helper, converting the
// seconds value to a real Duration. This matches the JSON wire format.
//
// Idempotent: applying defaults to an already-populated struct is a no-op.
func ApplyJobDataDefaults(d *models.JobData) {
	if d == nil {
		return
	}
	if d.MaxResults == 0 {
		d.MaxResults = DefaultMaxResults
	}
	if d.Depth == 0 {
		d.Depth = DefaultDepth
	}
	if d.MaxTime == 0 {
		// Stored as a "seconds value" — see function comment.
		d.MaxTime = time.Duration(DefaultMaxTimeSeconds)
	}
	// ReviewsMax, ImagesMax, Radius default to 0 (toggle-off / no
	// constraint), so a zero value is the intended default — no fill.
}

// ValidateJob validates a job payload.
func ValidateJob(j *models.Job) error {
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
	return ValidateJobData(&j.Data)
}

// ValidateJobData enforces resource consumption limits (CWE-400) at the
// service layer in addition to the struct-tag validation performed at the
// HTTP layer, so that non-HTTP callers (CLI, workers, internal queues) are
// also protected. All caps are sourced from cap_params.go. There is NO
// "unlimited" sentinel — every integer field has a strict min and max.
func ValidateJobData(d *models.JobData) error {
	// Keywords
	if len(d.Keywords) == 0 {
		return errors.New("missing keywords")
	}
	if len(d.Keywords) > maxKeywords {
		return fmt.Errorf("too many keywords: maximum is %d", maxKeywords)
	}
	for i, kw := range d.Keywords {
		if kw == "" {
			return fmt.Errorf("keyword[%d] is empty", i)
		}
		// validator's `max=200` counts BYTES via len(), not runes.
		// Mirror that here so the boundary is identical at both layers.
		if len(kw) > maxKeywordLen {
			return fmt.Errorf("keyword[%d] exceeds %d bytes", i, maxKeywordLen)
		}
	}

	// Lang — must be in the launch allowlist.
	if d.Lang == "" {
		return errors.New("missing lang")
	}
	if len(d.Lang) != 2 {
		return errors.New("invalid lang: expected 2-character ISO 639-1 code")
	}
	if !IsSupportedLang(d.Lang) {
		return fmt.Errorf("unsupported lang %q: must be a 2-character ISO 639-1 code in the supported allowlist", d.Lang)
	}

	// Depth
	if d.Depth < minDepth {
		return fmt.Errorf("depth must be >= %d", minDepth)
	}
	if d.Depth > CapDepth {
		return fmt.Errorf("depth exceeds maximum of %d", CapDepth)
	}

	// MaxTime — required, bounded both directions.
	if d.MaxTime == 0 {
		return errors.New("missing max_time")
	}
	if d.MaxTime < minMaxTime {
		return fmt.Errorf("max_time must be >= %s", minMaxTime)
	}
	if d.MaxTime > maxMaxTime {
		return fmt.Errorf("max_time exceeds maximum of %s", maxMaxTime)
	}

	// FastMode requires geo coordinates.
	if d.FastMode && (d.Lat == "" || d.Lon == "") {
		return errors.New("missing geo coordinates: fast_mode requires lat and lon")
	}

	// Lat/Lon range — the struct tag handles `latitude`/`longitude` parse,
	// but service-layer callers may not run the validator, so re-check here.
	if d.Lat != "" {
		lat, err := strconv.ParseFloat(d.Lat, 64)
		if err != nil {
			return fmt.Errorf("invalid lat: %v", err)
		}
		if lat < -90 || lat > 90 {
			return errors.New("lat must be in [-90, 90]")
		}
	}
	if d.Lon != "" {
		lon, err := strconv.ParseFloat(d.Lon, 64)
		if err != nil {
			return fmt.Errorf("invalid lon: %v", err)
		}
		if lon < -180 || lon > 180 {
			return errors.New("lon must be in [-180, 180]")
		}
	}

	// MaxResults — strict minimum 1, no more 0=unlimited.
	if d.MaxResults < 1 {
		return errors.New("max_results must be >= 1 (no unlimited sentinel)")
	}
	if d.MaxResults > CapMaxResults {
		return fmt.Errorf("max_results exceeds maximum of %d", CapMaxResults)
	}

	// ReviewsMax — per place. 0 means "skip reviews".
	if d.ReviewsMax < 0 {
		return errors.New("reviews_max cannot be negative")
	}
	if d.ReviewsMax > CapReviewsMax {
		return fmt.Errorf("reviews_max exceeds maximum of %d (per place)", CapReviewsMax)
	}

	// ImagesMax — per-job total across all places. 0 means "skip images".
	if d.ImagesMax < 0 {
		return errors.New("images_max cannot be negative")
	}
	if d.ImagesMax > CapImagesMaxTotal {
		return fmt.Errorf("images_max exceeds maximum of %d (per-job total)", CapImagesMaxTotal)
	}

	// Radius — bounded both directions.
	if d.Radius < 0 {
		return errors.New("radius cannot be negative")
	}
	if d.Radius > CapRadiusMeters {
		return fmt.Errorf("radius exceeds maximum of %d meters", CapRadiusMeters)
	}

	return nil
}
