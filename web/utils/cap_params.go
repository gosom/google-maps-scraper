// Package utils — cap parameter constants and language allowlist.
//
// This file is the single source of truth for the cap-parameter convention
// described in §2 of docs/superpowers/plans/2026-04-08-api-production-readiness-audit.md.
//
// REST best-practice resolution (locked in 2026-04-09):
//
//   - Defaults are CONSERVATIVE — a client hitting the API directly without
//     setting fields gets a small, cheap job. This is the OWASP API4:2023
//     fail-safe posture and matches how Stripe/GitHub/Google Places ship.
//   - Hard ceilings exist on every resource-consuming parameter and exceeding
//     them returns HTTP 400 with a descriptive message.
//   - "Missing" never means "unlimited" — there is no sentinel value. A
//     missing field is filled with its DOCUMENTED default by the
//     ApplyJobDataDefaults helper at the API entry point.
//   - The frontend is responsible for nudging revenue: "no cap" UX toggles
//     send the hard ceiling explicitly. The API doesn't do magic semantics
//     on missing fields.
//
// Headless-browser reality: max_time ceiling is 1 hour because Chromium
// scraping Google Maps degrades sharply over time (memory creep, anti-bot
// escalation, session staleness, supervisor SIGTERMs). Jobs that need more
// than 1 hour of wall clock should split into multiple submissions.
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
//     meaningfully bounds total image-event billing.
//
// Cap sizing math (production concurrency = 8, max_time = 1h):
//
//	1h budget ÷ ~60s per place × concurrency 8 ≈ 480 places max in a
//	single job. 480 × ~80 images/place average ≈ 38 400 images. The
//	40 000 image ceiling covers this worst case with a small headroom.
const (
	// CapMaxResults bounds places per job. Per-job. min 1. Set to 500 to
	// support multi-keyword power-user jobs (5 keywords × ~120 places each
	// at depth=20 ≈ 600 natural ceiling, clipped to 500).
	CapMaxResults = 500
	// DefaultMaxResults matches the natural place yield at the default
	// depth=5: real-world data shows depth=5 returns 40-50 places, so 50
	// is the API safety net for direct callers that omit max_results.
	// The frontend's "no cap" toggle sends the hard ceiling (500) instead.
	DefaultMaxResults = 50

	// CapReviewsMax bounds reviews PER PLACE. min 0 — 0 means "skip reviews".
	// A single business rarely has more than a few hundred reviews; 500 is
	// the safe per-place ceiling.
	CapReviewsMax = 500
	// DefaultReviewsMax is 0 (skip) to match the frontend's toggle UX:
	// reviews are an opt-in enrichment, so the API default is "off" and
	// the frontend sends a positive value when the user enables the toggle.
	DefaultReviewsMax = 0

	// CapImagesMaxTotal bounds the TOTAL number of images across all places
	// in a job — NOT per place. See package doc above for the full rationale
	// and the production-concurrency math.
	CapImagesMaxTotal = 40_000
	// DefaultImagesMaxTotal is 0 (skip) to match the frontend's toggle UX:
	// image scraping is an opt-in enrichment.
	DefaultImagesMaxTotal = 0

	// CapDepth bounds search depth. Per-job. min 1.
	CapDepth     = 20
	DefaultDepth = 5

	// CapRadiusMeters bounds search radius in meters. Per-job. min 0 — 0
	// means "no radius constraint".
	CapRadiusMeters     = 50_000
	DefaultRadiusMeters = 0

	// CapMaxTimeSeconds bounds wall-clock job duration. Per-job. min 60.
	// 1 hour is the realistic ceiling for headless Chromium scraping
	// Google Maps — see the package-level header comment for the reasons.
	CapMaxTimeSeconds     = 3_600 // 1 hour
	DefaultMaxTimeSeconds = 1_800 // 30 minutes
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
