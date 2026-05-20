package gmaps

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withCookiesFile resets the package-level cookie cache and points it at
// the supplied path for the duration of the test. Tests use this to avoid
// cross-test pollution of the global cache.
func withCookiesFile(t *testing.T, path string) {
	t.Helper()
	prev := cookiesFilePath
	resetCookiesCacheForTest()
	SetCookiesFile(path)
	t.Cleanup(func() {
		cookiesFilePath = prev
		resetCookiesCacheForTest()
	})
}

func writeCookies(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write cookies: %v", err)
	}
}

// TestLoadGoogleCookies_ReloadsOnFileChange is the headline contract: when
// the cookies file on disk changes mtime, the NEXT LoadGoogleCookies call
// returns the new contents without requiring a process restart. The pre-fix
// behavior was sync.Once-locked: a stale snapshot for the entire process
// lifetime even after a fresh cookie dump on prod.
func TestLoadGoogleCookies_ReloadsOnFileChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	writeCookies(t, path, `[{"name":"SID","value":"old","domain":".google.com","path":"/"}]`)
	withCookiesFile(t, path)

	first, err := LoadGoogleCookies()
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if len(first) != 1 || first[0].Value != "old" {
		t.Fatalf("first load got %+v, want SID=old", first)
	}

	// Rewrite with a later mtime. macOS/Linux filesystems typically have
	// 1-second mtime granularity, so bump the timestamp explicitly.
	writeCookies(t, path, `[{"name":"SID","value":"new","domain":".google.com","path":"/"}]`)
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	second, err := LoadGoogleCookies()
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if len(second) != 1 || second[0].Value != "new" {
		t.Fatalf("second load got %+v, want SID=new", second)
	}
}

// TestLoadGoogleCookies_NoReloadWhenMtimeUnchanged covers the cache-hit path:
// repeated calls without a file change return the same slice and do NOT
// re-parse the JSON. We assert by writing garbage to the cache slot and
// confirming LoadGoogleCookies still returns the original values (the file
// is not touched because mtime is unchanged).
func TestLoadGoogleCookies_NoReloadWhenMtimeUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	writeCookies(t, path, `[{"name":"SID","value":"v1","domain":".google.com","path":"/"}]`)
	withCookiesFile(t, path)

	if _, err := LoadGoogleCookies(); err != nil {
		t.Fatalf("first load: %v", err)
	}

	// Replace file content WITHOUT bumping mtime — same mtime means no
	// reload. The cached value should still surface.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	writeCookies(t, path, `garbage not json`)
	if err := os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	second, err := LoadGoogleCookies()
	if err != nil {
		t.Fatalf("second load (expected cache hit): %v", err)
	}
	if len(second) != 1 || second[0].Value != "v1" {
		t.Fatalf("expected cached value v1, got %+v", second)
	}
}

// TestLoadGoogleCookies_FilterOnlyGoogleDomains preserves the existing
// filter contract: cookies whose Domain does not contain "google" are
// dropped. Locks in a regression guard since the reload refactor touches
// the parser.
func TestLoadGoogleCookies_FilterOnlyGoogleDomains(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	writeCookies(t, path, `[
		{"name":"SID","value":"x","domain":".google.com","path":"/"},
		{"name":"OTHER","value":"y","domain":".example.com","path":"/"}
	]`)
	withCookiesFile(t, path)

	got, err := LoadGoogleCookies()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || got[0].Name != "SID" {
		t.Fatalf("expected only SID, got %+v", got)
	}
}

// TestLoadGoogleCookies_MissingFileError covers the path where the file
// configured via SetCookiesFile does not exist on disk: the call returns
// an error but does NOT poison the cache (a later call after the file
// appears succeeds).
func TestLoadGoogleCookies_MissingThenAppearing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	withCookiesFile(t, path)

	if _, err := LoadGoogleCookies(); err == nil {
		t.Fatal("expected error when cookies file missing")
	}

	writeCookies(t, path, `[{"name":"SID","value":"z","domain":".google.com","path":"/"}]`)
	got, err := LoadGoogleCookies()
	if err != nil {
		t.Fatalf("after file created: %v", err)
	}
	if len(got) != 1 || got[0].Value != "z" {
		t.Fatalf("got %+v, want SID=z", got)
	}
}
