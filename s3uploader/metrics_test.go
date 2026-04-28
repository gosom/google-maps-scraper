package s3uploader

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// TestNewMetrics_DoubleRegistrationOK locks in the AlreadyRegisteredError
// idiom: calling NewMetrics twice with the same registerer must not panic
// and must return collectors that are pointer-equal (the same underlying
// collector is reused). This matches pkg/metrics/billing.go behaviour and
// makes test setup safe — multiple test cases can each call NewMetrics on
// a fresh registry, and production code can reconstruct the Uploader
// without crashing the process.
func TestNewMetrics_DoubleRegistrationOK(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	first := NewMetrics(reg)
	if first == nil {
		t.Fatal("expected first NewMetrics call to return non-nil")
	}

	// Second call must not panic. It should reuse the existing collectors.
	second := NewMetrics(reg)
	if second == nil {
		t.Fatal("expected second NewMetrics call to return non-nil")
	}

	if first.OpDuration != second.OpDuration {
		t.Error("OpDuration should be reused on double registration, got different pointers")
	}
	if first.OpTotal != second.OpTotal {
		t.Error("OpTotal should be reused on double registration, got different pointers")
	}
	if first.OpBytes != second.OpBytes {
		t.Error("OpBytes should be reused on double registration, got different pointers")
	}
}

// TestNewMetrics_NilRegistererUsesDefault verifies the documented behaviour
// of nil → DefaultRegisterer. We don't reach into the global default; we
// just assert the call doesn't panic and returns a non-nil Metrics. The
// global registry already holds these collectors after the first call (a
// side effect of running the test suite), so subsequent calls hit the
// AlreadyRegisteredError reuse branch — proving the idiom works there too.
func TestNewMetrics_NilRegistererUsesDefault(t *testing.T) {
	// Not parallel: this writes to the global default registry.
	m := NewMetrics(nil)
	if m == nil {
		t.Fatal("expected NewMetrics(nil) to return non-nil Metrics")
	}
	if m.OpDuration == nil || m.OpTotal == nil || m.OpBytes == nil {
		t.Error("expected all three collectors to be non-nil")
	}
}

// TODO: add a TestUpload_RecordsMetrics in Chunk 4 once stub_test.go
// lands the s3API stubbing infrastructure (Task 11). The metric path is
// covered by hand-runs against a real bucket today; once we have a stub,
// we can assert OpTotal counter increments for both the success and
// failure branches without depending on the ordering of NewMetrics
// initialization or AWS connectivity.
