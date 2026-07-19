package scrapemate

import (
	"context"

	"github.com/gosom/kit/logging"
)

// GetLoggerFromContext returns a logger from the context or a default logger
func GetLoggerFromContext(ctx context.Context) logging.Logger {
	log, ok := ctx.Value(contextKey("log")).(logging.Logger)
	if !ok {
		return logging.Get()
	}

	return log
}

// ContextWithLogger returns a new context with the logger
func ContextWithLogger(ctx context.Context, logger logging.Logger) context.Context {
	return context.WithValue(ctx, contextKey("log"), logger)
}

type contextKey string
