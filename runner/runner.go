package runner

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"github.com/gosom/google-maps-scraper/s3uploader"
	"github.com/gosom/google-maps-scraper/tlmt"
	"github.com/gosom/google-maps-scraper/tlmt/gonoop"
	"github.com/gosom/google-maps-scraper/tlmt/goposthog"
	"github.com/gosom/google-maps-scraper/webshare"
)

// parseConcurrency parses dynamic concurrency values including percentages, fractions, and keywords
func parseConcurrency(value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("empty value")
	}

	cpuCores := runtime.NumCPU()
	slog.Debug("cpu_cores_detected", slog.Int("cores", cpuCores))

	value = strings.TrimSpace(strings.ToLower(value))

	// Handle keywords
	switch value {
	case "auto":
		result := cpuCores / 2
		if result < 1 {
			result = 1
		}
		slog.Debug("concurrency_resolved", slog.String("mode", "auto"), slog.Int("result", result), slog.Int("cores", cpuCores))
		return result, nil
	case "max":
		slog.Debug("concurrency_resolved", slog.String("mode", "max"), slog.Int("result", cpuCores), slog.Int("cores", cpuCores))
		return cpuCores, nil
	case "conservative":
		result := cpuCores / 4
		if result < 1 {
			result = 1
		}
		slog.Debug("concurrency_resolved", slog.String("mode", "conservative"), slog.Int("result", result), slog.Int("cores", cpuCores))
		return result, nil
	case "aggressive":
		result := (cpuCores * 3) / 4
		if result < 1 {
			result = 1
		}
		slog.Debug("concurrency_resolved", slog.String("mode", "aggressive"), slog.Int("result", result), slog.Int("cores", cpuCores))
		return result, nil
	}

	// Handle percentages (e.g., "75%")
	if strings.HasSuffix(value, "%") {
		percentStr := strings.TrimSuffix(value, "%")
		percent, err := strconv.ParseFloat(percentStr, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid percentage format: %s", value)
		}
		if percent < 0 || percent > 100 {
			return 0, fmt.Errorf("percentage must be between 0 and 100: %.1f", percent)
		}
		result := int((float64(cpuCores) * percent) / 100.0)
		if result < 1 {
			result = 1
		}
		slog.Debug("concurrency_resolved", slog.String("mode", "percent"), slog.String("input", value), slog.Int("result", result), slog.Int("cores", cpuCores))
		return result, nil
	}

	// Handle fractions (e.g., "3/4")
	if strings.Contains(value, "/") {
		parts := strings.Split(value, "/")
		if len(parts) != 2 {
			return 0, fmt.Errorf("invalid fraction format: %s", value)
		}
		numerator, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid fraction numerator: %s", parts[0])
		}
		denominator, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid fraction denominator: %s", parts[1])
		}
		if denominator == 0 {
			return 0, fmt.Errorf("fraction denominator cannot be zero")
		}
		result := int((float64(cpuCores) * numerator) / denominator)
		if result < 1 {
			result = 1
		}
		slog.Debug("concurrency_resolved", slog.String("mode", "fraction"), slog.String("input", value), slog.Int("result", result), slog.Int("cores", cpuCores))
		return result, nil
	}

	// Handle direct numbers
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid number format: %s", value)
	}
	if number < 1 {
		return 0, fmt.Errorf("concurrency must be at least 1: %d", number)
	}
	slog.Debug("concurrency_resolved", slog.String("mode", "direct"), slog.String("input", value), slog.Int("result", number))
	return number, nil
}

const (
	RunModeFile = iota + 1
	RunModeDatabase
	RunModeDatabaseProduce
	RunModeInstallPlaywright
	RunModeWeb
	RunModeAwsLambda
	RunModeAwsLambdaInvoker
)

var (
	ErrInvalidRunMode = errors.New("invalid run mode")
)

type Runner interface {
	Run(context.Context) error
	Close(context.Context) error
}

type S3Uploader interface {
	Upload(ctx context.Context, bucketName, key string, body io.Reader, contentType string) (*s3uploader.UploadResult, error)
}

// AWSConfig holds all AWS-related configuration fields.
type AWSConfig struct {
	AccessKey       string
	SecretKey       string
	Region          string
	S3Bucket        string
	LambdaRunner    bool
	LambdaInvoker   bool
	FunctionName    string
	LambdaChunkSize int
}

// ScrapingConfig holds scraping behaviour configuration fields.
type ScrapingConfig struct {
	FastMode         bool
	MaxDepth         int
	LangCode         string
	Email            bool
	Images           bool
	ExtraReviews     bool
	MaxResults       int
	GeoCoordinates   string
	Zoom             int
	Radius           float64
	DisablePageReuse bool
}

// ProxyConfig holds proxy-related configuration fields.
type ProxyConfig struct {
	Proxies        []string
	WebshareAPIKey string
}

type Config struct {
	AWS      AWSConfig
	Scraping ScrapingConfig
	Proxy    ProxyConfig

	Concurrency              int
	CacheDir                 string
	InputFile                string
	ResultsFile              string
	JSON                     bool
	Debug                    bool
	Dsn                      string
	ProduceOnly              bool
	ExitOnInactivityDuration time.Duration
	CustomWriter             string
	RunMode                  int
	DisableTelemetry         bool
	WebRunner                bool
	DataFolder               string
	S3Uploader               S3Uploader
	CookiesFile              string
	Addr                     string
	// Version holds the Git SHA injected at build time via ldflags (-X main.version=...).
	// It is propagated from main.go so that the /health endpoint can report it.
	Version string
}

func ParseConfig() (*Config, error) {
	cfg := Config{}

	if os.Getenv("PLAYWRIGHT_INSTALL_ONLY") == "1" {
		cfg.RunMode = RunModeInstallPlaywright

		return &cfg, nil
	}

	var (
		proxies string
	)

	defaultConcurrency := runtime.NumCPU() / 2
	if defaultConcurrency < 1 {
		defaultConcurrency = 1
	}
	flag.IntVar(&cfg.Concurrency, "c", defaultConcurrency, "sets the concurrency [default: half of CPU cores]. Also accepts CONCURRENCY env var with: numbers, percentages (75%), fractions (3/4), or keywords (auto, max, conservative, aggressive)")
	flag.StringVar(&cfg.CacheDir, "cache", "cache", "sets the cache directory [no effect at the moment]")
	flag.IntVar(&cfg.Scraping.MaxDepth, "depth", 10, "maximum scroll depth in search results [default: 10]")
	flag.StringVar(&cfg.ResultsFile, "results", "stdout", "path to the results file [default: stdout]")
	flag.StringVar(&cfg.InputFile, "input", "", "path to the input file with queries (one per line) [default: empty]")
	flag.StringVar(&cfg.Scraping.LangCode, "lang", "en", "language code for Google (e.g., 'de' for German) [default: en]")
	flag.BoolVar(&cfg.Debug, "debug", false, "enable headful crawl (opens browser window) [default: false]")
	flag.StringVar(&cfg.Dsn, "dsn", "", "database connection string [only valid with database provider]")
	flag.BoolVar(&cfg.ProduceOnly, "produce", false, "produce seed jobs only (requires dsn)")
	flag.DurationVar(&cfg.ExitOnInactivityDuration, "exit-on-inactivity", 0, "exit after inactivity duration (e.g., '5m')")
	flag.BoolVar(&cfg.JSON, "json", false, "produce JSON output instead of CSV")
	flag.BoolVar(&cfg.Scraping.Email, "email", false, "extract emails from websites")
	flag.StringVar(&cfg.CustomWriter, "writer", "", "use custom writer plugin (format: 'dir:pluginName')")
	flag.StringVar(&cfg.Scraping.GeoCoordinates, "geo", "", "set geo coordinates for search (e.g., '37.7749,-122.4194')")
	flag.IntVar(&cfg.Scraping.Zoom, "zoom", 15, "set zoom level (0-21) for search")
	flag.BoolVar(&cfg.WebRunner, "web", false, "run web server instead of crawling")
	flag.StringVar(&cfg.DataFolder, "data-folder", "webdata", "data folder for web runner")
	flag.StringVar(&proxies, "proxies", "", "comma separated list of proxies to use in the format protocol://user:pass@host:port example: socks5://localhost:9050 or http://user:pass@localhost:9050")
	flag.BoolVar(&cfg.AWS.LambdaRunner, "aws-lambda", false, "run as AWS Lambda function")
	flag.BoolVar(&cfg.AWS.LambdaInvoker, "aws-lambda-invoker", false, "run as AWS Lambda invoker")
	flag.StringVar(&cfg.AWS.FunctionName, "function-name", "", "AWS Lambda function name")
	flag.StringVar(&cfg.AWS.AccessKey, "aws-access-key", "", "AWS access key")
	flag.StringVar(&cfg.AWS.SecretKey, "aws-secret-key", "", "AWS secret key")
	flag.StringVar(&cfg.AWS.Region, "aws-region", "", "AWS region")
	flag.StringVar(&cfg.AWS.S3Bucket, "s3-bucket", "", "S3 bucket name")
	flag.IntVar(&cfg.AWS.LambdaChunkSize, "aws-lambda-chunk-size", 100, "AWS Lambda chunk size")
	flag.BoolVar(&cfg.Scraping.FastMode, "fast-mode", false, "fast mode (reduced data collection)")
	flag.Float64Var(&cfg.Scraping.Radius, "radius", 10000, "search radius in meters. Default is 10000 meters")
	flag.StringVar(&cfg.Addr, "addr", ":8080", "address to listen on for web server")
	flag.BoolVar(&cfg.Scraping.DisablePageReuse, "disable-page-reuse", false, "disable page reuse in playwright")
	flag.BoolVar(&cfg.Scraping.ExtraReviews, "extra-reviews", false, "enable extra reviews collection")
	flag.IntVar(&cfg.Scraping.MaxResults, "max-results", 0, "maximum number of results to collect (0 = unlimited)")

	flag.Parse()

	if cfg.AWS.AccessKey == "" {
		cfg.AWS.AccessKey = os.Getenv("MY_AWS_ACCESS_KEY")
	}

	if cfg.AWS.SecretKey == "" {
		cfg.AWS.SecretKey = os.Getenv("MY_AWS_SECRET_KEY")
	}

	if cfg.AWS.Region == "" {
		cfg.AWS.Region = os.Getenv("MY_AWS_REGION")
	}

	if cfg.Dsn == "" {
		cfg.Dsn = os.Getenv("DSN")
	}

	// Allow concurrency override via environment variable with dynamic parsing
	if concurrencyEnv := os.Getenv("CONCURRENCY"); concurrencyEnv != "" {
		if c, err := parseConcurrency(concurrencyEnv); err == nil {
			cfg.Concurrency = c
			slog.Debug("concurrency_override", slog.Int("concurrency", cfg.Concurrency))
		} else {
			slog.Warn("invalid_concurrency_env", slog.String("value", concurrencyEnv), slog.Any("error", err), slog.Int("default", cfg.Concurrency))
		}
	} else {
		cpuCores := runtime.NumCPU()
		slog.Debug("default_concurrency", slog.Int("concurrency", cfg.Concurrency), slog.Int("cpu_cores", cpuCores))
	}

	// Do not force concurrency in debug mode; keep user/provider choice intact

	if cfg.AWS.LambdaInvoker && cfg.AWS.FunctionName == "" {
		return nil, fmt.Errorf("FunctionName must be provided when using AwsLambdaInvoker")
	}

	if cfg.AWS.LambdaInvoker && cfg.AWS.S3Bucket == "" {
		return nil, fmt.Errorf("S3Bucket must be provided when using AwsLambdaInvoker")
	}

	if cfg.AWS.LambdaInvoker && cfg.InputFile == "" {
		return nil, fmt.Errorf("InputFile must be provided when using AwsLambdaInvoker")
	}

	if cfg.Concurrency < 1 {
		return nil, fmt.Errorf("Concurrency must be greater than 0, got %d", cfg.Concurrency)
	}

	if cfg.Scraping.MaxDepth < 1 {
		return nil, fmt.Errorf("MaxDepth must be greater than 0, got %d", cfg.Scraping.MaxDepth)
	}

	if cfg.Scraping.Zoom < 0 || cfg.Scraping.Zoom > 21 {
		return nil, fmt.Errorf("Zoom must be between 0 and 21, got %d", cfg.Scraping.Zoom)
	}

	if cfg.Dsn == "" && cfg.ProduceOnly {
		return nil, fmt.Errorf("Dsn must be provided when using ProduceOnly")
	}

	slog.Debug("proxy_config", slog.String("proxies_env", os.Getenv("PROXIES")), slog.String("cli_proxies_flag", proxies))

	// Priority: CLI proxies > Webshare API > No proxies
	if proxies != "" {
		cfg.Proxy.Proxies = strings.Split(proxies, ",")
		slog.Debug("cli_proxies_configured", slog.Int("count", len(cfg.Proxy.Proxies)))
	} else if os.Getenv("PROXIES") != "" {
		// Informative log: PROXIES env is set but ignored unless -proxies flag is provided
		slog.Debug("proxies_env_ignored", slog.String("hint", "pass with -proxies flag to enable"))
	}

	// Check for Webshare API key
	if cfg.Proxy.WebshareAPIKey == "" {
		cfg.Proxy.WebshareAPIKey = os.Getenv("WEBSHARE_API_KEY")
	}

	// Fetch proxies from Webshare API if no manual proxies provided and API key exists
	if len(cfg.Proxy.Proxies) == 0 && cfg.Proxy.WebshareAPIKey != "" {
		slog.Info("webshare_api_key_detected_fetching_proxies")
		webshareClient := webshare.NewClient(cfg.Proxy.WebshareAPIKey, slog.Default())

		// Ensure IP is authorized
		if err := webshareClient.EnsureIPAuthorized(); err != nil {
			slog.Warn("webshare_ip_authorization_failed",
				slog.Any("error", err),
				slog.String("action", "continuing_without_proxies"),
			)
		} else {
			// Fetch proxy list
			proxyList, err := webshareClient.GetProxiesForScraper("direct")
			if err != nil {
				slog.Warn("webshare_proxy_fetch_failed",
					slog.Any("error", err),
					slog.String("action", "continuing_without_proxies"),
				)
			} else {
				cfg.Proxy.Proxies = proxyList
				slog.Info("webshare_proxies_loaded", slog.Int("proxy_count", len(cfg.Proxy.Proxies)))
			}
		}
	}

	if cfg.AWS.AccessKey != "" && cfg.AWS.SecretKey != "" && cfg.AWS.Region != "" {
		uploader, err := s3uploader.New(cfg.AWS.AccessKey, cfg.AWS.SecretKey, cfg.AWS.Region)
		if err != nil {
			return nil, fmt.Errorf("creating S3 uploader: %w", err)
		}
		cfg.S3Uploader = uploader
	}

	switch {
	case cfg.AWS.LambdaInvoker:
		cfg.RunMode = RunModeAwsLambdaInvoker
	case cfg.AWS.LambdaRunner:
		cfg.RunMode = RunModeAwsLambda
	case cfg.WebRunner || (cfg.Dsn == "" && cfg.InputFile == ""):
		cfg.RunMode = RunModeWeb
	case cfg.Dsn == "":
		cfg.RunMode = RunModeFile
	case cfg.ProduceOnly:
		cfg.RunMode = RunModeDatabaseProduce
	case cfg.Dsn != "":
		cfg.RunMode = RunModeDatabase
	default:
		return nil, fmt.Errorf("invalid configuration: unable to determine run mode")
	}

	return &cfg, nil
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

// bannerRender renders a framed banner. If color is non-empty (ANSI code),
// it applies the color to both frame and content lines and resets at EOL.
func bannerRender(messages []string, width int, color string) string {
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

	if color != "" {
		builder.WriteString(color + "╔" + strings.Repeat("═", width-2) + "╗\x1b[0m\n")
	} else {
		builder.WriteString("╔" + strings.Repeat("═", width-2) + "╗\n")
	}

	for _, line := range wrappedLines {
		lineWidth := runewidth.StringWidth(line)
		paddingRight := contentWidth - lineWidth

		if paddingRight < 0 {
			paddingRight = 0
		}

		if color != "" {
			builder.WriteString(fmt.Sprintf("%s║ %s%s ║\x1b[0m\n", color, line, strings.Repeat(" ", paddingRight)))
		} else {
			builder.WriteString(fmt.Sprintf("║ %s%s ║\n", line, strings.Repeat(" ", paddingRight)))
		}
	}

	if color != "" {
		builder.WriteString(color + "╚" + strings.Repeat("═", width-2) + "╝\x1b[0m\n")
	} else {
		builder.WriteString("╚" + strings.Repeat("═", width-2) + "╝\n")
	}

	return builder.String()
}

func banner(messages []string, width int) string {
	return bannerRender(messages, width, "")
}

func bannerColored(messages []string, width int, color string) string {
	return bannerRender(messages, width, color)
}

func BannerWithDebug(debug bool) {
	message1 := "Google Maps Scraper"
	message2 := "Forked from GitHub: https://github.com/gosom/google-maps-scraper"

	fmt.Fprintln(os.Stderr, banner([]string{message1, message2}, 0))

	if debug {
		art := []string{
			" ______   _______  _______  __   __  _______    __   __  _______  ______   _______ ",
			"|      | |       ||  _    ||  | |  ||       |  |  |_|  ||       ||      | |       |",
			"|  _    ||    ___|| |_|   ||  | |  ||    ___|  |       ||   _   ||  _    ||    ___|",
			"| | |   ||   |___ |       ||  |_|  ||   | __   |       ||  | |  || | |   ||   |___ ",
			"| |_|   ||    ___||  _   | |       ||   ||  |  |       ||  |_|  || |_|   ||    ___|",
			"|       ||   |___ | |_|   ||       ||   |_| |  | ||_|| ||       ||       ||   |___ ",
			"|______| |_______||_______||_______||_______|  |_|   |_||_______||______| |_______|",
		}
		lines := append([]string{}, art...)
		fmt.Fprintln(os.Stderr, bannerColored(lines, 0, "\x1b[31m"))
	}
}
