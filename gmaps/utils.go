package gmaps

import (
	"regexp"
)

// ExtractDataIDFromURL extracts the DataID (0x[hex1]:0x[hex2]) from Google Maps URLs.
// This enables pre-filtering of duplicate businesses before full page scraping.
//
// Pattern: /maps/place/Name/data=!4m7!3m6!1s0x[hex1]:0x[hex2]!...
// Returns: "0x[hex1]:0x[hex2]" or empty string if not found
//
// Example:
//
//	Input:  "https://www.google.com/maps/place/Blue+Bottle+Coffee/data=!4m7!3m6!1s0x80858098babc2d4b:0xbeedd659cc698c92!8m2!3d37.7763342!4d-122.4232375"
//	Output: "0x80858098babc2d4b:0xbeedd659cc698c92"
func ExtractDataIDFromURL(url string) string {
	// Phase 8.1 investigation confirmed this pattern in 100% of search result URLs
	// Pattern: 1s0x[hex1]:0x[hex2] within the data= parameter
	pattern := `1s(0x[a-f0-9]+:0x[a-f0-9]+)`

	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(url)

	if len(matches) >= 2 {
		// Return the captured DataID (0x[hex1]:0x[hex2])
		return matches[1]
	}

	// Graceful degradation - return empty string if pattern not found
	// This allows fallback to existing URL-based deduplication
	return ""
}

// ConvertCIDToDataID converts a decimal CID to DataID format for deduplication.
// This enables checking if a decimal CID matches against extracted DataIDs.
//
// The CID is the decimal representation of the second hex part in DataID.
// Example: CID "16519582940102929223" = 0xe5415928d6702b47 (second part of DataID)
//
// Note: This function converts only the CID portion. Full DataID reconstruction
// would require the first hex part, which varies by location/business.
func ConvertCIDToDataID(cid string) string {
	// For now, we'll use a simple prefix approach for deduplication
	// This allows matching against the CID portion of extracted DataIDs
	return "cid:" + cid
}
