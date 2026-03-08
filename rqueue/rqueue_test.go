//nolint:testpackage // tests unexported queue helpers directly
package rqueue

import (
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/scraper"
	"github.com/stretchr/testify/require"
)

func TestWaitForFlushResultReturnsResultWithinGrace(t *testing.T) {
	ch := make(chan scraper.FlushResult, 1)

	go func() {
		time.Sleep(20 * time.Millisecond)
		ch <- scraper.FlushResult{ResultCount: 3}
	}()

	result, err := waitForFlushResult(ch, 200*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, 3, result.ResultCount)
}

func TestWaitForFlushResultTimesOut(t *testing.T) {
	ch := make(chan scraper.FlushResult)

	_, err := waitForFlushResult(ch, 30*time.Millisecond)
	require.Error(t, err)
	require.Contains(t, err.Error(), "timed out waiting for flush result")
}

func TestResolveRegistryImage(t *testing.T) {
	testCases := []struct {
		name       string
		registry   string
		image      string
		wantResult string
	}{
		{
			name:       "full ghcr image remains unchanged",
			registry:   "ghcr.io",
			image:      "ghcr.io/gosom/google-maps-scraper-saas:latest",
			wantResult: "ghcr.io/gosom/google-maps-scraper-saas:latest",
		},
		{
			name:       "short image is prefixed by registry",
			registry:   "ghcr.io",
			image:      "gosom/google-maps-scraper-saas:latest",
			wantResult: "ghcr.io/gosom/google-maps-scraper-saas:latest",
		},
		{
			name:       "registry slash and image slash are normalized",
			registry:   "ghcr.io/",
			image:      "/gosom/google-maps-scraper-saas:latest",
			wantResult: "ghcr.io/gosom/google-maps-scraper-saas:latest",
		},
		{
			name:       "localhost registry in image stays unchanged",
			registry:   "ghcr.io",
			image:      "localhost:5000/org/app:1.0.0",
			wantResult: "localhost:5000/org/app:1.0.0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveRegistryImage(tc.registry, tc.image)
			require.Equal(t, tc.wantResult, got)
		})
	}
}
