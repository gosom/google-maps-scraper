package utils

import "testing"

// TestCapConstants_AreSane locks in the cap values so any future change
// is intentional and forces a test update. Bumping a cap is a code change
// that must come with a deliberate decision and a doc update.
func TestCapConstants_AreSane(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		got, want int
	}{
		"CapMaxResults":         {CapMaxResults, 500},
		"DefaultMaxResults":     {DefaultMaxResults, 50},
		"CapReviewsMax":         {CapReviewsMax, 500},
		"DefaultReviewsMax":     {DefaultReviewsMax, 0},
		"CapImagesMaxTotal":     {CapImagesMaxTotal, 40_000},
		"DefaultImagesMaxTotal": {DefaultImagesMaxTotal, 0},
		"CapDepth":              {CapDepth, 20},
		"DefaultDepth":          {DefaultDepth, 5},
		"CapRadiusMeters":       {CapRadiusMeters, 50_000},
		"DefaultRadiusMeters":   {DefaultRadiusMeters, 0},
		"CapMaxTimeSeconds":     {CapMaxTimeSeconds, 3_600},
		"DefaultMaxTimeSeconds": {DefaultMaxTimeSeconds, 1_800},
	}
	for name, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %d, want %d", name, c.got, c.want)
		}
	}
}

// TestCapConstants_DefaultsWithinCaps verifies every default is within the
// valid range. A default outside its cap would be a programmer error that
// would crash any test that exercises the default-on-omission path.
func TestCapConstants_DefaultsWithinCaps(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		min, def, max int
	}{
		"max_results": {1, DefaultMaxResults, CapMaxResults},
		"max_reviews": {0, DefaultReviewsMax, CapReviewsMax},
		"max_images":  {0, DefaultImagesMaxTotal, CapImagesMaxTotal},
		"depth":       {1, DefaultDepth, CapDepth},
		"radius":      {0, DefaultRadiusMeters, CapRadiusMeters},
		"max_time":    {60, DefaultMaxTimeSeconds, CapMaxTimeSeconds},
	}
	for name, c := range cases {
		if c.def < c.min || c.def > c.max {
			t.Errorf("%s default %d is outside [%d, %d]", name, c.def, c.min, c.max)
		}
	}
}

// TestSupportedLangs_ContainsCommon verifies the launch-language allowlist
// includes the common European + Asian + Middle-Eastern codes we expect to
// support at launch. Adding a language requires updating both supportedLangs
// AND this test (the test failure forces a deliberate decision).
func TestSupportedLangs_ContainsCommon(t *testing.T) {
	t.Parallel()
	for _, lang := range []string{"en", "de", "fr", "es", "it", "pt", "nl", "ja", "ko", "zh", "ar"} {
		if !IsSupportedLang(lang) {
			t.Errorf("expected %q to be supported", lang)
		}
	}
}

// TestSupportedLangs_RejectsUnknown is the negative-space sibling of the
// previous test: garbage and uppercase variants are rejected.
func TestSupportedLangs_RejectsUnknown(t *testing.T) {
	t.Parallel()
	for _, lang := range []string{"xx", "@@", "", "EN", "DE", "english", "e", "deu"} {
		if IsSupportedLang(lang) {
			t.Errorf("expected %q to be rejected", lang)
		}
	}
}
