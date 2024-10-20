package runner

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"github.com/gosom/google-maps-scraper/tlmt"
	"github.com/gosom/google-maps-scraper/tlmt/gonoop"
	"github.com/gosom/google-maps-scraper/tlmt/goposthog"
)

const (
	RunModeFile = iota + 1
	RunModeDatabase
	RunModeDatabaseProduce
	RunModeInstallPlaywright
	RunModeWeb
)

var (
	ErrInvalidRunMode = errors.New("invalid run mode")
)

type Runner interface {
	Run(context.Context) error
	Close(context.Context) error
}

type Config struct {
	Concurrency              int
	CacheDir                 string
	MaxDepth                 int
	InputFile                string
	ResultsFile              string
	JSON                     bool
	LangCode                 string
	Debug                    bool
	Dsn                      string
	ProduceOnly              bool
	ExitOnInactivityDuration time.Duration
	Email                    bool
	CustomWriter             string
	GeoCoordinates           string
	Zoom                     int
	RunMode                  int
	DisableTelemetry         bool
	WebRunner                bool
	DataFolder               string
	Proxies                  []string
	ProxyUsername            string
	ProxyPassword            string
}

func ParseConfig() *Config {
	cfg := Config{}

	if os.Getenv("PLAYWRIGHT_INSTALL_ONLY") == "1" {
		cfg.RunMode = RunModeInstallPlaywright

		return &cfg
	}

	var proxies string

	flag.IntVar(&cfg.Concurrency, "c", runtime.NumCPU()/2, "sets the concurrency [default: half of CPU cores]")
	flag.StringVar(&cfg.CacheDir, "cache", "cache", "sets the cache directory [no effect at the moment]")
	flag.IntVar(&cfg.MaxDepth, "depth", 10, "maximum scroll depth in search results [default: 10]")
	flag.StringVar(&cfg.ResultsFile, "results", "stdout", "path to the results file [default: stdout]")
	flag.StringVar(&cfg.InputFile, "input", "", "path to the input file with queries (one per line) [default: empty]")
	flag.StringVar(&cfg.LangCode, "lang", "en", "language code for Google (e.g., 'de' for German) [default: en]")
	flag.BoolVar(&cfg.Debug, "debug", false, "enable headful crawl (opens browser window) [default: false]")
	flag.StringVar(&cfg.Dsn, "dsn", "", "database connection string [only valid with database provider]")
	flag.BoolVar(&cfg.ProduceOnly, "produce", false, "produce seed jobs only (requires dsn)")
	flag.DurationVar(&cfg.ExitOnInactivityDuration, "exit-on-inactivity", 0, "exit after inactivity duration (e.g., '5m')")
	flag.BoolVar(&cfg.JSON, "json", false, "produce JSON output instead of CSV")
	flag.BoolVar(&cfg.Email, "email", false, "extract emails from websites")
	flag.StringVar(&cfg.CustomWriter, "writer", "", "use custom writer plugin (format: 'dir:pluginName')")
	flag.StringVar(&cfg.GeoCoordinates, "geo", "", "set geo coordinates for search (e.g., '37.7749,-122.4194')")
	flag.IntVar(&cfg.Zoom, "zoom", 0, "set zoom level (0-21) for search")
	flag.BoolVar(&cfg.WebRunner, "web", false, "run web server instead of crawling")
	flag.StringVar(&cfg.DataFolder, "data-folder", "webdata", "data folder for web runner")
	flag.StringVar(&proxies, "proxies", "", "comma separated list of proxies to use")
	flag.StringVar(&cfg.ProxyUsername, "proxy-username", "", "username for proxy authentication")
	flag.StringVar(&cfg.ProxyPassword, "proxy-password", "", "password for proxy authentication")

	flag.Parse()

	if cfg.Concurrency < 1 {
		panic("Concurrency must be greater than 0")
	}

	if cfg.MaxDepth < 1 {
		panic("MaxDepth must be greater than 0")
	}

	if cfg.Zoom < 0 || cfg.Zoom > 21 {
		panic("Zoom must be between 0 and 21")
	}

	if cfg.Dsn == "" && cfg.ProduceOnly {
		panic("Dsn must be provided when using ProduceOnly")
	}

	if proxies != "" {
		cfg.Proxies = strings.Split(proxies, ",")
	}

	switch {
	case cfg.WebRunner || (cfg.Dsn == "" && cfg.InputFile == ""):
		cfg.RunMode = RunModeWeb
	case cfg.Dsn == "":
		cfg.RunMode = RunModeFile
	case cfg.ProduceOnly:
		cfg.RunMode = RunModeDatabaseProduce
	case cfg.Dsn != "":
		cfg.RunMode = RunModeDatabase
	default:
		panic("Invalid configuration")
	}

	return &cfg
}

var (
	telemetryOnce sync.Once
	telemetry     tlmt.Telemetry
)

func Telemetry() tlmt.Telemetry {
	telemetryOnce.Do(func() {
		disableTel := func() bool {
			return os.Getenv("DISABLE_TELEMETRY") == "1"
		}()

		if disableTel {
			telemetry = gonoop.New()

			return
		}

		val, err := goposthog.New("phc_CHYBGEd1eJZzDE7ZWhyiSFuXa9KMLRnaYN47aoIAY2A", "https://eu.i.posthog.com")
		if err != nil || val == nil {
			telemetry = gonoop.New()

			return
		}

		telemetry = val
	})

	return telemetry
}

func wrapText(text string, width int) []string {
	var lines []string

	currentLine := ""
	currentWidth := 0

	for _, r := range text {
		runeWidth := runewidth.RuneWidth(r)
		if currentWidth+runeWidth > width {
			lines = append(lines, currentLine)
			currentLine = string(r)
			currentWidth = runeWidth
		} else {
			currentLine += string(r)
			currentWidth += runeWidth
		}
	}

	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return lines
}

func banner(messages []string, width int) string {
	if width <= 0 {
		var err error

		width, _, err = term.GetSize(0)
		if err != nil {
			width = 80
		}
	}

	if width < 20 {
		width = 20
	}

	contentWidth := width - 4

	var wrappedLines []string
	for _, message := range messages {
		wrappedLines = append(wrappedLines, wrapText(message, contentWidth)...)
	}

	var builder strings.Builder

	builder.WriteString("╔" + strings.Repeat("═", width-2) + "╗\n")

	for _, line := range wrappedLines {
		lineWidth := runewidth.StringWidth(line)
		paddingRight := contentWidth - lineWidth

		if paddingRight < 0 {
			paddingRight = 0
		}

		builder.WriteString(fmt.Sprintf("║ %s%s ║\n", line, strings.Repeat(" ", paddingRight)))
	}

	builder.WriteString("╚" + strings.Repeat("═", width-2) + "╝\n")

	return builder.String()
}

func Banner() {
	message1 := "🌍 Google Maps Scraper"
	message2 := "⭐ If you find this project useful, please star it on GitHub: https://github.com/gosom/google-maps-scraper"
	message3 := "💖 Consider sponsoring to support development: https://github.com/sponsors/gosom"

	fmt.Fprintln(os.Stderr, banner([]string{message1, message2, message3}, 0))
}
