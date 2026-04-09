// Package utils — cap parameter constants and language allowlist.
//
// This file is the single source of truth for the cap-parameter convention
// described in §2 of docs/superpowers/plans/2026-04-08-api-production-readiness-audit.md.
//
// Convention: every integer cap field in the public API has min, max, and
// default. There is NO sentinel for "unlimited" — clients paginate or run
// multiple jobs. Values above the max are rejected with HTTP 400, never
// silently coerced. Defaults are billing-safe (lean toward "less work" when
// the user doesn't specify).
package utils

// Cap parameter constants. Most caps are per-job (the value is a job-wide
// limit on the request body). Two exceptions:
//
//   - CapReviewsMax is PER PLACE (a job with 100 places at reviews_max=500
//     produces up to 50,000 reviews). The cap shape matches how reviews
//     naturally cluster around individual businesses.
//
//   - CapImagesMaxTotal is PER JOB TOTAL across all places. Image counts on
//     Google Maps are unbounded per business — popular venues return
//     hundreds. A per-place cap that covers real businesses would let a
//     500-place job produce ~50k images, which is far beyond any user's
//     billing intent. The per-job total cap is the only shape that
//     meaningfully bounds total image-event billing. Real-world reference:
//     a typical 100-place job at depth=20 produces ~8000 images at the
//     natural Google Maps density (~80 images/place average), so the 20000
//     ceiling allows ~250 places-worth of imagery.
const (
	// CapMaxResults bounds places per job. Per-job. min 1.
	// Real-world test: 1 search × depth=20 yielded 112 places, so 500 is
	// a comfortable headroom over typical jobs.
	CapMaxResults     = 500
	DefaultMaxResults = 20

	// CapReviewsMax bounds reviews PER PLACE. min 0 — 0 means "skip reviews".
	// A single business rarely has more than a few hundred reviews; 500 is
	// the safe per-place ceiling.
	CapReviewsMax     = 500
	DefaultReviewsMax = 10

	// CapImagesMaxTotal bounds the TOTAL number of images across all places
	// in a job — NOT per place. See package doc above for the full rationale.
	// min 0 — 0 means "skip all image scraping" (the billing-safe default).
	CapImagesMaxTotal     = 20_000
	DefaultImagesMaxTotal = 0

	// CapDepth bounds search depth. Per-job. min 1.
	CapDepth     = 20
	DefaultDepth = 5

	// CapRadiusMeters bounds search radius in meters. Per-job. min 0 — 0
	// means "no radius constraint".
	CapRadiusMeters     = 50_000
	DefaultRadiusMeters = 0

	// CapMaxTimeSeconds bounds wall-clock job duration. Per-job. min 60.
	CapMaxTimeSeconds     = 14_400 // 4 hours
	DefaultMaxTimeSeconds = 1_800  // 30 minutes
)

// supportedLangs is the ISO 639-1 allowlist of language codes the Google Maps
// scraper supports. Two-character codes only. Add to this map when launching
// new locales — there is no fallback or wildcard.
var supportedLangs = map[string]struct{}{
	"en": {}, "de": {}, "fr": {}, "es": {}, "it": {}, "pt": {}, "nl": {},
	"pl": {}, "tr": {}, "sv": {}, "no": {}, "da": {}, "fi": {}, "cs": {},
	"sk": {}, "hu": {}, "ro": {}, "el": {}, "bg": {}, "hr": {}, "sl": {},
	"et": {}, "lv": {}, "lt": {}, "ja": {}, "ko": {}, "zh": {}, "ar": {},
	"he": {}, "th": {}, "vi": {}, "id": {}, "ms": {}, "uk": {}, "ru": {},
}

// IsSupportedLang reports whether the 2-char ISO 639-1 code is in the allowlist.
// Returns false for any value not exactly matching an entry — including
// uppercase variants (caller is responsible for lowercasing first if needed).
func IsSupportedLang(code string) bool {
	_, ok := supportedLangs[code]
	return ok
}
