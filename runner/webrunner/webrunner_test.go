package webrunner

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain installs a goroutine-leak guard for every test in this package.
// If any test (especially future ones in Phase 3 that exercise the
// scrapeJob coordinator) leaves a goroutine running, the package's tests
// fail loudly instead of silently leaking under -race.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
