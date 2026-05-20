package gmaps

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestGoogleSAPISIDHash_KnownVector locks in the algorithm against a vector
// computed independently with shasum -a 1. Format: "{ts_millis}_{sha1_hex}"
// where the SHA1 input is "{ts_millis} {SAPISID} {origin}".
//
// Reference (POSIX shell):
//
//	printf '%s' "1700000000000 testSAPISID https://www.google.com" | shasum -a 1
//	→ abfc84eb37b59da5fc22d1b66d7d7e296951eb18
func TestGoogleSAPISIDHash_KnownVector(t *testing.T) {
	ts := time.UnixMilli(1700000000000)
	got := googleSAPISIDHash(ts, "testSAPISID", "https://www.google.com")
	want := "1700000000000_abfc84eb37b59da5fc22d1b66d7d7e296951eb18"
	if got != want {
		t.Fatalf("googleSAPISIDHash mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestGoogleSAPISIDHash_DifferentSAPISIDDifferentHash(t *testing.T) {
	ts := time.UnixMilli(1700000000000)
	a := googleSAPISIDHash(ts, "sapisidA", "https://www.google.com")
	b := googleSAPISIDHash(ts, "sapisidB", "https://www.google.com")
	if a == b {
		t.Fatalf("hash collision across different SAPISIDs: %s", a)
	}
}

func TestGoogleSAPISIDHash_DifferentTimestampDifferentPrefix(t *testing.T) {
	a := googleSAPISIDHash(time.UnixMilli(1700000000000), "x", "https://www.google.com")
	b := googleSAPISIDHash(time.UnixMilli(1700000000001), "x", "https://www.google.com")
	if a == b {
		t.Fatalf("hash collision across different timestamps")
	}
	if !strings.HasPrefix(a, "1700000000000_") {
		t.Fatalf("expected ts prefix, got %s", a)
	}
	if !strings.HasPrefix(b, "1700000000001_") {
		t.Fatalf("expected ts prefix, got %s", b)
	}
}

// TestBuildGoogleAuthorization_AllThreeLabels verifies that when SAPISID,
// __Secure-1PAPISID and __Secure-3PAPISID are all present, the header
// concatenates SAPISIDHASH + SAPISID1PHASH + SAPISID3PHASH space-separated —
// matching the format Chrome sends on www.google.com RPC endpoints.
func TestBuildGoogleAuthorization_AllThreeLabels(t *testing.T) {
	ts := time.UnixMilli(1700000000000)
	cookies := []CookieEntry{
		{Name: "SAPISID", Value: "v1"},
		{Name: "__Secure-1PAPISID", Value: "v2"},
		{Name: "__Secure-3PAPISID", Value: "v3"},
		{Name: "SID", Value: "ignored-for-hash"},
	}
	got := buildGoogleAuthorization(ts, cookies, "https://www.google.com")
	if !strings.Contains(got, "SAPISIDHASH ") {
		t.Errorf("missing SAPISIDHASH label: %s", got)
	}
	if !strings.Contains(got, "SAPISID1PHASH ") {
		t.Errorf("missing SAPISID1PHASH label: %s", got)
	}
	if !strings.Contains(got, "SAPISID3PHASH ") {
		t.Errorf("missing SAPISID3PHASH label: %s", got)
	}
	// Three labels => exactly three spaces inside each hash (label<sp>hash)
	// plus separators between groups. Sanity check the total label count.
	if c := strings.Count(got, "HASH "); c != 3 {
		t.Errorf("expected 3 hash labels, got %d in %q", c, got)
	}
}

// TestBuildGoogleAuthorization_MissingCookiesReturnsEmpty covers the path where
// none of the SAPISID variants are present — caller should NOT set an
// Authorization header in that case (better to send no header than a malformed
// one).
func TestBuildGoogleAuthorization_MissingCookiesReturnsEmpty(t *testing.T) {
	ts := time.UnixMilli(1700000000000)
	cookies := []CookieEntry{
		{Name: "SID", Value: "x"},
		{Name: "HSID", Value: "y"},
	}
	if got := buildGoogleAuthorization(ts, cookies, "https://www.google.com"); got != "" {
		t.Fatalf("expected empty Authorization when no SAPISID variants present, got %q", got)
	}
}

func TestBuildGoogleAuthorization_SkipsEmptyValues(t *testing.T) {
	ts := time.UnixMilli(1700000000000)
	cookies := []CookieEntry{
		{Name: "SAPISID", Value: ""},
		{Name: "__Secure-1PAPISID", Value: "v2"},
	}
	got := buildGoogleAuthorization(ts, cookies, "https://www.google.com")
	if strings.Contains(got, "SAPISIDHASH ") {
		t.Errorf("must skip cookie with empty value; got %q", got)
	}
	if !strings.Contains(got, "SAPISID1PHASH ") {
		t.Errorf("expected SAPISID1PHASH from non-empty cookie; got %q", got)
	}
}

// TestApplyGoogleAuthHeaders_SetsAllRequiredHeaders covers the contract that
// /maps/rpc/listugcposts needs. Missing any one of these previously caused
// Google to return the 33-byte unauthenticated stub
// `)]}'\n[null,null,null,null,null,1]` — see PR description.
func TestApplyGoogleAuthHeaders_SetsAllRequiredHeaders(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://www.google.com/maps/rpc/listugcposts", nil)
	cookies := []CookieEntry{
		{Name: "SAPISID", Value: "abc"},
		{Name: "__Secure-1PAPISID", Value: "def"},
		{Name: "__Secure-3PAPISID", Value: "ghi"},
	}
	applyGoogleAuthHeaders(req, cookies, time.UnixMilli(1700000000000))

	if got := req.Header.Get("Authorization"); !strings.HasPrefix(got, "SAPISIDHASH ") {
		t.Errorf("Authorization header missing or wrong format: %q", got)
	}
	if got := req.Header.Get("Origin"); got != "https://www.google.com" {
		t.Errorf("Origin = %q, want https://www.google.com", got)
	}
	if got := req.Header.Get("Referer"); got != "https://www.google.com/maps/" {
		t.Errorf("Referer = %q, want https://www.google.com/maps/", got)
	}
	if got := req.Header.Get("X-Goog-AuthUser"); got != "0" {
		t.Errorf("X-Goog-AuthUser = %q, want 0", got)
	}
	if got := req.Header.Get("X-Same-Domain"); got != "1" {
		t.Errorf("X-Same-Domain = %q, want 1", got)
	}
}

// TestApplyGoogleAuthHeaders_NoAuthCookiesSkipsAuthHeader covers the safety
// case: when no SAPISID variants are loaded we MUST NOT set an Authorization
// header at all (Google will reject "SAPISIDHASH " with no hash). The other
// headers (Origin/Referer/X-Goog-*) are still set because they're cheap and
// browser-typical.
func TestApplyGoogleAuthHeaders_NoAuthCookiesSkipsAuthHeader(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://www.google.com/maps/rpc/listugcposts", nil)
	applyGoogleAuthHeaders(req, []CookieEntry{{Name: "SID", Value: "x"}}, time.UnixMilli(1700000000000))
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization must not be set without SAPISID cookies; got %q", got)
	}
	if got := req.Header.Get("Origin"); got != "https://www.google.com" {
		t.Errorf("Origin still expected; got %q", got)
	}
}
