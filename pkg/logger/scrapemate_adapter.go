package logger

import (
	"context"
	"log/slog"
	"os"

	"github.com/gosom/kit/logging"
)

// SlogAdapter implements gosom/kit/logging.Logger by delegating to slog.
// This bridges scrapemate's logging into our structured slog pipeline so
// all scraper logs appear in Grafana/Loki with the same format and fields.
type SlogAdapter struct {
	log *slog.Logger
}

// NewSlogAdapter wraps an slog.Logger as a logging.Logger for scrapemate.
func NewSlogAdapter(log *slog.Logger) *SlogAdapter {
	return &SlogAdapter{log: log}
}

func (a *SlogAdapter) Info(msg string, args ...any)  { a.log.Info(msg, args...) }
func (a *SlogAdapter) Warn(msg string, args ...any)  { a.log.Warn(msg, args...) }
func (a *SlogAdapter) Error(msg string, args ...any) { a.log.Error(msg, args...) }
func (a *SlogAdapter) Debug(msg string, args ...any) { a.log.Debug(msg, args...) }
func (a *SlogAdapter) Trace(msg string, args ...any) { a.log.Debug(msg, args...) } // slog has no trace level
func (a *SlogAdapter) Fatal(msg string, args ...any) {
	a.log.Error(msg, args...)
	os.Exit(1)
}
func (a *SlogAdapter) Panic(msg string, args ...any) {
	a.log.Error(msg, args...)
	panic(msg)
}
func (a *SlogAdapter) With(args ...any) logging.Logger {
	return &SlogAdapter{log: a.log.With(args...)}
}

func (a *SlogAdapter) Level(_ logging.Level) logging.Logger {
	// slog level is set at the handler, not per-logger. Return self.
	return a
}

// slogAdapterCtxKey is used by NewContext to store the adapter in context.
// In practice, we inject via scrapemate.ContextWithLogger() directly,
// so this key is only needed to satisfy the logging.Logger interface.
type slogAdapterCtxKey struct{}

func (a *SlogAdapter) NewContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, slogAdapterCtxKey{}, a)
}

func (a *SlogAdapter) Log(level logging.Level, msg string, args ...any) {
	switch level {
	case logging.DEBUG, logging.TRACE:
		a.log.Debug(msg, args...)
	case logging.INFO:
		a.log.Info(msg, args...)
	case logging.WARN:
		a.log.Warn(msg, args...)
	case logging.ERROR, logging.FATAL, logging.PANIC:
		a.log.Error(msg, args...)
	default:
		a.log.Info(msg, args...)
	}
}

// Verify interface compliance at compile time.
var _ logging.Logger = (*SlogAdapter)(nil)
