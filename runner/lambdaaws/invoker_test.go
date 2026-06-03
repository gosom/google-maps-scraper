//nolint:testpackage // This test validates unexported payload construction without invoking AWS.
package lambdaaws

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gosom/google-maps-scraper/runner"
)

func TestSetPayloadsIncludesBrowserPoolConfig(t *testing.T) {
	t.Parallel()

	inputFile := filepath.Join(t.TempDir(), "keywords.txt")
	if err := os.WriteFile(inputFile, []byte("coffee\npizza\nbakery\n"), 0o600); err != nil {
		t.Fatalf("write input file: %v", err)
	}

	cfg := &runner.Config{
		InputFile:          inputFile,
		AwsLambdaChunkSize: 2,
		S3Bucket:           "results",
		MaxDepth:           3,
		Concurrency:        4,
		LangCode:           "en",
		FunctionName:       "scraper",
		BrowserPoolSize:    5,
		MaxPagesPerBrowser: 6,
		DisablePageReuse:   true,
		ExtraReviews:       true,
	}

	var inv invoker
	if err := inv.setPayloads(cfg); err != nil {
		t.Fatalf("set payloads: %v", err)
	}

	if got, want := len(inv.payloads), 2; got != want {
		t.Fatalf("payload count = %d, want %d", got, want)
	}

	for idx, payload := range inv.payloads {
		if got, want := payload.BrowserPoolSize, cfg.BrowserPoolSize; got != want {
			t.Fatalf("payload %d BrowserPoolSize = %d, want %d", idx, got, want)
		}

		if got, want := payload.MaxPagesPerBrowser, cfg.MaxPagesPerBrowser; got != want {
			t.Fatalf("payload %d MaxPagesPerBrowser = %d, want %d", idx, got, want)
		}
	}
}
