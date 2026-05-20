package gmaps

import (
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Google's signed-in RPC endpoints (e.g. /maps/rpc/listugcposts) require a
// SAPISIDHASH-style Authorization header in addition to the auth cookies. The
// algorithm is:
//
//	payload = ts_millis + " " + SAPISID + " " + origin
//	value   = ts_millis + "_" + lowercase_hex(sha1(payload))
//
// Chrome sends all three label variants concatenated into the SAME
// Authorization header, space-separated:
//
//	Authorization: SAPISIDHASH <hash> SAPISID1PHASH <hash> SAPISID3PHASH <hash>
//
// using SAPISID, __Secure-1PAPISID and __Secure-3PAPISID respectively. We
// mirror that format. Without this header, Google silently treats the request
// as anonymous and returns the canonical 33-byte stub `)]}'\n
// [null,null,null,null,null,1]` even when all the auth cookies are valid.
//
// References:
//   - https://gist.github.com/eyecatchup/2d700122e24154fdc985b7071ec7764a
//   - https://brutecat.com/articles/decoding-google/

const googleAuthOrigin = "https://www.google.com"

// sapisidLabelByCookie maps cookie name → Authorization-header label. Order
// matters: Chrome emits SAPISIDHASH first, then 1PHASH, then 3PHASH. We follow
// the same order so logs are diff-friendly against a DevTools capture.
var sapisidLabelByCookie = []struct {
	cookieName string
	label      string
}{
	{"SAPISID", "SAPISIDHASH"},
	{"__Secure-1PAPISID", "SAPISID1PHASH"},
	{"__Secure-3PAPISID", "SAPISID3PHASH"},
}

// googleSAPISIDHash returns the per-cookie hash component, formatted as
// `{ts_millis}_{sha1_hex}`. Lowercase hex per Chrome's implementation.
func googleSAPISIDHash(ts time.Time, sapisid, origin string) string {
	tsStr := strconv.FormatInt(ts.UnixMilli(), 10)
	sum := sha1.Sum([]byte(tsStr + " " + sapisid + " " + origin))
	return tsStr + "_" + hex.EncodeToString(sum[:])
}

// buildGoogleAuthorization assembles the full Authorization-header value by
// iterating sapisidLabelByCookie. Cookies that are missing or have empty
// values are skipped — emitting "SAPISIDHASH " with no hash would be worse
// than omitting the label.
//
// Returns "" when no SAPISID variant is available; the caller MUST NOT set
// an Authorization header in that case.
func buildGoogleAuthorization(ts time.Time, cookies []CookieEntry, origin string) string {
	byName := make(map[string]string, len(cookies))
	for _, c := range cookies {
		byName[c.Name] = c.Value
	}

	var parts []string
	for _, m := range sapisidLabelByCookie {
		v := byName[m.cookieName]
		if v == "" {
			continue
		}
		parts = append(parts, m.label+" "+googleSAPISIDHash(ts, v, origin))
	}
	return strings.Join(parts, " ")
}

// applyGoogleAuthHeaders sets the Authorization header and the supporting
// Origin / Referer / X-Goog-AuthUser / X-Same-Domain headers on req. Caller
// is responsible for also setting the Cookie header (see GetCookieHeader).
//
// `now` is injected for deterministic tests; production callers pass
// time.Now().
func applyGoogleAuthHeaders(req *http.Request, cookies []CookieEntry, now time.Time) {
	if auth := buildGoogleAuthorization(now, cookies, googleAuthOrigin); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("Origin", googleAuthOrigin)
	req.Header.Set("Referer", googleAuthOrigin+"/maps/")
	req.Header.Set("X-Goog-AuthUser", "0")
	req.Header.Set("X-Same-Domain", "1")
}
