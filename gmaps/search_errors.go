package gmaps

import "context"

type searchErrorReporterKey struct{}

// WithSearchErrorReporter registers a concurrency-safe callback for failures
// processing searches or listings in this run.
func WithSearchErrorReporter(ctx context.Context, report func(error)) context.Context {
	return context.WithValue(ctx, searchErrorReporterKey{}, report)
}

func reportSearchError(ctx context.Context, err error) {
	if report, ok := ctx.Value(searchErrorReporterKey{}).(func(error)); ok && err != nil {
		report(err)
	}
}
