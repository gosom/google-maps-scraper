package s3uploader

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestUpload_RecordsMetrics asserts that a successful Upload increments
// OpTotal{op="upload",result="ok"} from 0 to 1 and OpBytes{op="upload"}
// by the body length, while a failed Upload increments
// OpTotal{op="upload",result="error"} but does NOT credit bytes.
//
// Each test owns a fresh prometheus.Registry to avoid polluting the
// global DefaultRegisterer (which other tests in this package and in the
// process at large rely on for clean assertions).
func TestUpload_RecordsMetrics(t *testing.T) {
	t.Parallel()

	t.Run("success path increments ok counter and bytes", func(t *testing.T) {
		t.Parallel()

		reg := prometheus.NewRegistry()
		metrics := NewMetrics(reg)
		stub := &fakeS3{}
		u := newTestUploader(stub, WithMetrics(metrics))

		// Pre-condition: counters start at 0.
		require.Equal(t, 0.0, testutil.ToFloat64(metrics.OpTotal.WithLabelValues("upload", "ok")))
		require.Equal(t, 0.0, testutil.ToFloat64(metrics.OpBytes.WithLabelValues("upload")))

		body := []byte("a,b\nc,d\n")
		_, err := u.Upload(context.Background(), "bkt", "k.csv", bytes.NewReader(body), "text/csv")
		require.NoError(t, err)
		require.NotNil(t, stub.lastPut, "fake should have observed the PutObject call")

		assert.Equal(t, 1.0, testutil.ToFloat64(metrics.OpTotal.WithLabelValues("upload", "ok")),
			"OpTotal{upload,ok} should increment by 1 on success")
		assert.Equal(t, float64(len(body)), testutil.ToFloat64(metrics.OpBytes.WithLabelValues("upload")),
			"OpBytes{upload} should equal body length on success")
		assert.Equal(t, 0.0, testutil.ToFloat64(metrics.OpTotal.WithLabelValues("upload", "error")),
			"OpTotal{upload,error} must remain 0 on success")
	})

	t.Run("error path increments error counter and skips bytes", func(t *testing.T) {
		t.Parallel()

		reg := prometheus.NewRegistry()
		metrics := NewMetrics(reg)
		stub := &fakeS3{put: func(*s3.PutObjectInput) (*s3.PutObjectOutput, error) {
			return nil, errors.New("boom")
		}}
		u := newTestUploader(stub, WithMetrics(metrics))

		_, err := u.Upload(context.Background(), "bkt", "k.csv", bytes.NewReader([]byte("x")), "text/csv")
		require.Error(t, err)

		assert.Equal(t, 1.0, testutil.ToFloat64(metrics.OpTotal.WithLabelValues("upload", "error")),
			"OpTotal{upload,error} should increment by 1 on failure")
		assert.Equal(t, 0.0, testutil.ToFloat64(metrics.OpTotal.WithLabelValues("upload", "ok")),
			"OpTotal{upload,ok} must remain 0 on failure")
		assert.Equal(t, 0.0, testutil.ToFloat64(metrics.OpBytes.WithLabelValues("upload")),
			"OpBytes{upload} must NOT be credited on failure (recordOp passes 0 bytes)")
	})
}
