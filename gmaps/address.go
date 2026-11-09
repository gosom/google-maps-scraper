package gmaps

import (
	"strings"
	"unicode"
)

const (
	// placeAddressNode is the index inside a Google Maps place array that holds the
	// structured address block. Most listings have it, but some do not — service-area
	// businesses and plus-code-only listings among them — which is why every reader of
	// it needs a fallback.
	placeAddressNode = 183

	// addressTagLocality identifies the entry inside the address block's tag list that
	// spells out the locality, either as "City" or as "City, Country". It duplicates
	// information from the structured slots and survives in some payloads where those
	// slots are empty.
	addressTagLocality = 4

	// minStateAbbreviationLen and maxStateAbbreviationLen bound the length of a state or
	// province abbreviation such as "IL", "NY" or "QLD".
	minStateAbbreviationLen = 2
	maxStateAbbreviationLen = 3

	// maxPostalCodeTokens caps how many trailing whitespace-separated tokens may be
	// peeled off a locality segment as a postal code, so that "Ilion 131 22" splits into
	// "Ilion" and "131 22" without swallowing the locality itself.
	maxPostalCodeTokens = 2

	// isoCountryCodeLen is the length of an ISO 3166-1 alpha-2 country code.
	isoCountryCodeLen = 2
)

// completeAddressFromPlaceArray builds the structured address of a place.
//
// The authoritative source is the ordered slot list at index 1 of the address block,
// which Google fills with borough, street, city, postal code, state and the ISO country
// code. Whenever a slot comes back empty the redundant copies Google ships in the same
// block are consulted, and the flat formatted address is mined as a last resort. Fields
// are only ever filled in, never overwritten, so a value coming from the structured
// slots always wins over a derived one.
func completeAddressFromPlaceArray(darray []any, flatAddress string) Address {
	node := getNthElementAndCast[[]any](darray, placeAddressNode)

	addr := Address{
		Borough:    getNthElementAndCast[string](node, 1, 0),
		Street:     getNthElementAndCast[string](node, 1, 1),
		City:       getNthElementAndCast[string](node, 1, 3),
		PostalCode: getNthElementAndCast[string](node, 1, 4),
		State:      getNthElementAndCast[string](node, 1, 5),
		Country:    getNthElementAndCast[string](node, 1, 6),
	}

	if addr.City == "" || addr.Country == "" {
		city, country := splitLocality(addressPartByTag(node, addressTagLocality))

		if addr.City == "" {
			addr.City = city
		}

		if addr.Country == "" {
			addr.Country = country
		}
	}

	if addr.Street == "" || addr.City == "" || addr.State == "" ||
		addr.PostalCode == "" || addr.Country == "" {
		fallback := AddressFromFlat(flatAddress)

		if addr.Street == "" {
			addr.Street = fallback.Street
		}

		if addr.City == "" {
			addr.City = fallback.City
		}

		if addr.State == "" {
			addr.State = fallback.State
		}

		if addr.PostalCode == "" {
			addr.PostalCode = fallback.PostalCode
		}

		if addr.Country == "" {
			addr.Country = fallback.Country
		}
	}

	addr.Country = normalizeCountryCode(addr.Country)

	return addr
}

// addressPartByTag returns the value of the tagged entry with the given tag from the
// address block's tag list, or an empty string when the tag is absent. Each entry has
// the shape [tag, [[value]]].
func addressPartByTag(node []any, tag int) string {
	parts := getNthElementAndCast[[]any](node, 0)

	for i := range parts {
		el := getNthElementAndCast[[]any](parts, i)

		if int(getNthElementAndCast[float64](el, 0)) != tag {
			continue
		}

		return strings.TrimSpace(getNthElementAndCast[string](el, 1, 0, 0))
	}

	return ""
}

// splitLocality splits a locality string such as "Ilion, Greece" into its city and its
// ISO country code. A trailing segment is only read as a country when it matches a known
// country name, so "Limassol" yields a city and no country.
func splitLocality(v string) (city, country string) {
	parts := splitTrimmed(v)
	if len(parts) == 0 {
		return "", ""
	}

	if len(parts) > 1 {
		if code, ok := countryCodeFromName(parts[len(parts)-1]); ok {
			country = code
		}
	}

	return parts[0], country
}

// AddressFromFlat mines a flat formatted address for its components. It is a best-effort
// reader used only when the structured block is missing or incomplete, and it is
// deliberately conservative: a segment is read as a country only on an exact match
// against the known country names, and as a state only when it looks like an ASCII
// abbreviation, so the function prefers leaving a field empty over guessing wrong.
func AddressFromFlat(flat string) Address {
	parts := splitTrimmed(flat)
	if len(parts) == 0 {
		return Address{}
	}

	var addr Address

	if code, ok := countryCodeFromName(parts[len(parts)-1]); ok {
		addr.Country = code
		parts = parts[:len(parts)-1]
	}

	if len(parts) == 0 {
		return addr
	}

	// The last remaining segment carries the city, usually with a postal code and
	// sometimes a state abbreviation glued on: "Ilion 131 22", "Brisbane QLD 4000".
	cityIdx := len(parts) - 1

	locality, postalCode := splitPostalCode(parts[cityIdx])
	addr.PostalCode = postalCode

	city, state := splitStateAbbreviation(locality)
	addr.State = state

	if city == "" && cityIdx > 0 {
		// The segment held nothing but a state and a postal code, as in
		// "Springfield, IL 62704", so the city is the segment before it.
		cityIdx--
		city = parts[cityIdx]
	}

	addr.City = city

	if cityIdx > 0 {
		addr.Street = strings.Join(parts[:cityIdx], ", ")
	}

	return addr
}

// splitPostalCode peels a trailing postal code off a locality segment, returning the
// locality and the postal code. "Ilion 131 22" splits into "Ilion" and "131 22".
func splitPostalCode(v string) (locality, postalCode string) {
	fields := strings.Fields(v)

	end := len(fields)
	for end > 0 && len(fields)-end < maxPostalCodeTokens && isPostalCodeToken(fields[end-1]) {
		end--
	}

	if end == len(fields) {
		return v, ""
	}

	return strings.Join(fields[:end], " "), strings.Join(fields[end:], " ")
}

// isPostalCodeToken reports whether a token consists only of digits and hyphens and
// holds at least one digit. Alphanumeric postal codes are deliberately not recognised,
// because telling them apart from a locality name is not reliable.
func isPostalCodeToken(v string) bool {
	hasDigit := false

	for _, r := range v {
		switch {
		case unicode.IsDigit(r):
			hasDigit = true
		case r == '-':
		default:
			return false
		}
	}

	return hasDigit
}

// splitStateAbbreviation peels a trailing state or province abbreviation off a locality,
// returning the locality and the abbreviation. "Brisbane QLD" splits into "Brisbane" and
// "QLD"; a bare "IL" yields an empty locality, which signals the caller that the city has
// to come from the preceding segment.
func splitStateAbbreviation(v string) (locality, state string) {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return "", ""
	}

	last := fields[len(fields)-1]
	if !isStateAbbreviation(last) {
		return v, ""
	}

	return strings.Join(fields[:len(fields)-1], " "), last
}

// isStateAbbreviation reports whether a token looks like an upper-case ASCII state or
// province abbreviation such as "IL", "NY" or "QLD".
func isStateAbbreviation(v string) bool {
	if len(v) < minStateAbbreviationLen || len(v) > maxStateAbbreviationLen {
		return false
	}

	for _, r := range v {
		if r < 'A' || r > 'Z' {
			return false
		}
	}

	return true
}

// normalizeCountryCode brings a country to a single representation, the upper-case ISO
// 3166-1 alpha-2 code, so that the stored value does not end up a mix of codes and names
// depending on which source filled it. A value that is neither a known country name nor
// a two-letter code is passed through unchanged rather than dropped.
func normalizeCountryCode(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}

	if code, ok := countryCodeFromName(v); ok {
		return code
	}

	if len(v) == isoCountryCodeLen && isASCIILetters(v) {
		return strings.ToUpper(v)
	}

	return v
}

// countryCodeFromName resolves a country name to its upper-case ISO 3166-1 alpha-2 code,
// reporting whether the name was known.
func countryCodeFromName(name string) (string, bool) {
	code, ok := countryToCode[strings.ToLower(strings.TrimSpace(name))]
	return code, ok
}

// isASCIILetters reports whether every character is an ASCII letter.
func isASCIILetters(v string) bool {
	for _, r := range v {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}

	return v != ""
}

// splitTrimmed splits a comma-separated string and drops empty segments.
func splitTrimmed(v string) []string {
	raw := strings.Split(v, ",")
	out := make([]string, 0, len(raw))

	for _, part := range raw {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}
