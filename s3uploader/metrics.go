package s3uploader

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds Prometheus collectors for S3 operations.
//
// Three metrics, all labelled by op and (where applicable) result:
//   - brezel_s3_op_duration_seconds: latency histogram, op ∈ {upload, download, head_bucket}
//   - brezel_s3_op_total: total count, result ∈ {ok, error}
//   - brezel_s3_op_bytes_total: bytes transferred (currently upload only;
//     download skips counting to avoid double-buffering the response body)
type Metrics struct {
	OpDuration *prometheus.HistogramVec
	OpTotal    *prometheus.CounterVec
	OpBytes    *prometheus.CounterVec
}

// NewMetrics registers and returns a Metrics instance using the provided
// registerer. Passing nil uses the default registry. If a metric with the
// same name is already registered (e.g. when NewMetrics is called more than
// once in the same process or in tests), the existing collector is reused —
// matching the idiom in pkg/metrics/billing.go.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	dur := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "brezel",
		Subsystem: "s3",
		Name:      "op_duration_seconds",
		Help:      "Duration of S3 operations (Upload, Download, HeadBucket) in seconds, labelled by op and result.",
		Buckets:   prometheus.ExponentialBuckets(0.05, 2, 10),
	}, []string{"op", "result"})
	dur = mustRegisterHistogramVec(reg, dur)

	total := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "brezel",
		Subsystem: "s3",
		Name:      "op_total",
		Help:      "Count of S3 operations by op and result.",
	}, []string{"op", "result"})
	total = mustRegisterCounterVec(reg, total)

	bytes := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "brezel",
		Subsystem: "s3",
		Name:      "op_bytes_total",
		Help:      "Bytes transferred during S3 operations, labelled by op.",
	}, []string{"op"})
	bytes = mustRegisterCounterVec(reg, bytes)

	return &Metrics{OpDuration: dur, OpTotal: total, OpBytes: bytes}
}

func mustRegisterHistogramVec(reg prometheus.Registerer, c *prometheus.HistogramVec) *prometheus.HistogramVec {
	if err := reg.Register(c); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return are.ExistingCollector.(*prometheus.HistogramVec)
		}
		panic(err)
	}
	return c
}

func mustRegisterCounterVec(reg prometheus.Registerer, c *prometheus.CounterVec) *prometheus.CounterVec {
	if err := reg.Register(c); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return are.ExistingCollector.(*prometheus.CounterVec)
		}
		panic(err)
	}
	return c
}
