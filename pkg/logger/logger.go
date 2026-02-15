package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ctxKey struct{}

const (
	defaultLogOutputMode = "both"
	defaultLogDir        = "logs"
	defaultLogFileName   = "brezel-api.log"
	defaultLogMaxSizeMB  = 100
	defaultLogRetention  = 14
)

var (
	outputWriterOnce sync.Once
	sharedOutput     io.Writer
)

// New creates a *slog.Logger with JSON output at the given level.
// Output is controlled by environment variables:
// - LOG_OUTPUT: stdout | file | both (default: both)
// - LOG_FILE_PATH: explicit log file path (overrides LOG_DIR/LOG_FILE_NAME)
// - LOG_DIR: log directory when LOG_FILE_PATH is not set (default: logs)
// - LOG_FILE_NAME: log filename when LOG_FILE_PATH is not set (default: brezel-api.log)
// - LOG_MAX_SIZE_MB: max size per dated file before rollover (default: 100)
// - LOG_RETENTION_DAYS: number of days to keep dated files (default: 14)
// Valid levels: "debug", "info", "warn", "error". Defaults to "info".
func New(level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(outputWriter(), &slog.HandlerOptions{
		Level: parseLevel(level),
	}))
}

// NewWithComponent creates a logger with a "component" attribute.
// Replaces the old pattern of log.New(os.Stdout, "[API] ", ...).
func NewWithComponent(level, component string) *slog.Logger {
	return New(level).With(slog.String("component", component))
}

// FromContext extracts the *slog.Logger from context.
// Falls back to slog.Default() if none is stored.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// WithContext stores a *slog.Logger in the context.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

func outputWriter() io.Writer {
	outputWriterOnce.Do(func() {
		sharedOutput = buildOutputWriter()
	})
	return sharedOutput
}

func buildOutputWriter() io.Writer {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_OUTPUT")))
	if mode == "" {
		mode = defaultLogOutputMode
	}

	useStdout := mode == "stdout" || mode == "both"
	useFile := mode == "file" || mode == "both"
	if !useStdout && !useFile {
		os.Stderr.WriteString("logger: invalid LOG_OUTPUT (expected stdout|file|both), using both\n")
		useStdout = true
		useFile = true
	}

	writers := make([]io.Writer, 0, 2)
	if useStdout {
		writers = append(writers, os.Stdout)
	}

	if useFile {
		fileWriter, err := openLogFileWriter()
		if err != nil {
			os.Stderr.WriteString("logger: failed to open log file: " + err.Error() + "\n")
		} else {
			writers = append(writers, fileWriter)
		}
	}

	if len(writers) == 0 {
		return os.Stdout
	}
	if len(writers) == 1 {
		return writers[0]
	}
	return io.MultiWriter(writers...)
}

func openLogFileWriter() (io.Writer, error) {
	path := resolveLogFilePath()
	maxSizeMB := envInt("LOG_MAX_SIZE_MB", defaultLogMaxSizeMB)
	if maxSizeMB < 1 {
		maxSizeMB = defaultLogMaxSizeMB
	}

	retentionDays := envInt("LOG_RETENTION_DAYS", defaultLogRetention)
	if retentionDays < 1 {
		retentionDays = defaultLogRetention
	}

	return newRotatingFileWriter(path, int64(maxSizeMB)*1024*1024, retentionDays)
}

func resolveLogFilePath() string {
	if path := strings.TrimSpace(os.Getenv("LOG_FILE_PATH")); path != "" {
		return filepath.Clean(path)
	}

	dir := strings.TrimSpace(os.Getenv("LOG_DIR"))
	if dir == "" {
		dir = defaultLogDir
	}

	name := strings.TrimSpace(os.Getenv("LOG_FILE_NAME"))
	if name == "" {
		name = defaultLogFileName
	}

	return filepath.Clean(filepath.Join(dir, name))
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	n, err := strconv.Atoi(value)
	if err != nil {
		os.Stderr.WriteString(fmt.Sprintf("logger: invalid %s=%q, using %d\n", key, value, fallback))
		return fallback
	}
	return n
}

type rotatingFileWriter struct {
	mu            sync.Mutex
	dir           string
	baseName      string
	ext           string
	maxSize       int64
	retentionDays int

	currentDate string
	currentPart int
	currentSize int64
	file        *os.File
}

func newRotatingFileWriter(path string, maxSizeBytes int64, retentionDays int) (*rotatingFileWriter, error) {
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	name := filepath.Base(path)
	ext := filepath.Ext(name)
	baseName := strings.TrimSuffix(name, ext)
	if baseName == "" {
		baseName = "brezel-api"
	}
	if ext == "" {
		ext = ".log"
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	w := &rotatingFileWriter{
		dir:           dir,
		baseName:      baseName,
		ext:           ext,
		maxSize:       maxSizeBytes,
		retentionDays: retentionDays,
	}

	if err := w.openForDate(time.Now().Format("2006-01-02")); err != nil {
		return nil, err
	}

	return w, nil
}

func (w *rotatingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.rotateIfNeeded(int64(len(p))); err != nil {
		return 0, err
	}

	n, err := w.file.Write(p)
	w.currentSize += int64(n)
	return n, err
}

func (w *rotatingFileWriter) rotateIfNeeded(incomingBytes int64) error {
	today := time.Now().Format("2006-01-02")

	if w.file == nil || w.currentDate != today {
		if err := w.openForDate(today); err != nil {
			return err
		}
	}

	if w.currentSize+incomingBytes <= w.maxSize {
		return nil
	}

	return w.rolloverPart()
}

func (w *rotatingFileWriter) openForDate(date string) error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}

	w.currentDate = date
	w.currentPart = 0

	if err := w.pruneOldFiles(date); err != nil {
		os.Stderr.WriteString("logger: failed pruning old logs: " + err.Error() + "\n")
	}

	return w.openNextWritablePart()
}

func (w *rotatingFileWriter) rolloverPart() error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}

	w.currentPart++
	return w.openNextWritablePart()
}

func (w *rotatingFileWriter) openNextWritablePart() error {
	for {
		path := filepath.Join(w.dir, w.filename(w.currentDate, w.currentPart))
		fd, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}

		info, err := fd.Stat()
		if err != nil {
			_ = fd.Close()
			return err
		}

		size := info.Size()
		if size >= w.maxSize {
			_ = fd.Close()
			w.currentPart++
			continue
		}

		w.file = fd
		w.currentSize = size
		return nil
	}
}

func (w *rotatingFileWriter) filename(date string, part int) string {
	if part == 0 {
		return fmt.Sprintf("%s-%s%s", w.baseName, date, w.ext)
	}
	return fmt.Sprintf("%s-%s.%d%s", w.baseName, date, part, w.ext)
}

func (w *rotatingFileWriter) pruneOldFiles(today string) error {
	if w.retentionDays <= 0 {
		return nil
	}

	todayDate, err := time.Parse("2006-01-02", today)
	if err != nil {
		return err
	}
	cutoff := todayDate.AddDate(0, 0, -(w.retentionDays - 1))

	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return err
	}

	pattern := regexp.MustCompile(
		"^" + regexp.QuoteMeta(w.baseName) + `-(\d{4}-\d{2}-\d{2})(?:\.\d+)?` + regexp.QuoteMeta(w.ext) + `$`,
	)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		matches := pattern.FindStringSubmatch(entry.Name())
		if len(matches) < 2 {
			continue
		}

		fileDate, err := time.Parse("2006-01-02", matches[1])
		if err != nil {
			continue
		}
		if fileDate.Before(cutoff) {
			_ = os.Remove(filepath.Join(w.dir, entry.Name()))
		}
	}

	return nil
}
