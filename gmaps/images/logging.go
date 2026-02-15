package images

import (
	"fmt"
	"log/slog"
	"strings"
)

// logf preserves existing printf-style call sites while routing output through structured slog levels.
func logf(format string, args ...any) {
	msg := fmt.Sprintf(strings.TrimSuffix(format, "\n"), args...)

	switch {
	case strings.HasPrefix(msg, "DEBUG: "):
		slog.Debug(strings.TrimPrefix(msg, "DEBUG: "))
	case strings.HasPrefix(msg, "Warning: "):
		slog.Warn(strings.TrimPrefix(msg, "Warning: "))
	case strings.HasPrefix(msg, "WARNING: "):
		slog.Warn(strings.TrimPrefix(msg, "WARNING: "))
	case strings.HasPrefix(msg, "ERROR: "):
		slog.Error(strings.TrimPrefix(msg, "ERROR: "))
	case strings.HasPrefix(msg, "Error: "):
		slog.Error(strings.TrimPrefix(msg, "Error: "))
	default:
		slog.Info(msg)
	}
}
