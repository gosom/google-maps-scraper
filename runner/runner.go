package runner

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
	fmt.Printf("DEBUG: System CPU cores detected: %d\n", cpuCores)

	value = strings.TrimSpace(strings.ToLower(value))

	// Handle keywords
	switch value {
	case "auto":
		result := cpuCores / 2
		if result < 1 {
			result = 1
		}
		fmt.Printf("DEBUG: CONCURRENCY=auto -> %d (50%% of %d cores)\n", result, cpuCores)
		return result, nil
	case "max":
		fmt.Printf("DEBUG: CONCURRENCY=max -> %d (100%% of %d cores)\n", cpuCores, cpuCores)
		return cpuCores, nil
	case "conservative":
		result := cpuCores / 4
		if result < 1 {
			result = 1
		}
		fmt.Printf("DEBUG: CONCURRENCY=conservative -> %d (25%% of %d cores)\n", result, cpuCores)
		return result, nil
	case "aggressive":
		result := (cpuCores * 3) / 4
		if result < 1 {
			result = 1
		}
		fmt.Printf("DEBUG: CONCURRENCY=aggressive -> %d (75%% of %d cores)\n", result, cpuCores)
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
		fmt.Printf("DEBUG: CONCURRENCY=%s -> %d (%.1f%% of %d cores)\n", value, result, percent, cpuCores)
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
		fmt.Printf("DEBUG: CONCURRENCY=%s -> %d (%.1f/%.1f of %d cores)\n", value, result, numerator, denominator, cpuCores)
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
	fmt.Printf("DEBUG: CONCURRENCY=%s -> %d (direct number)\n", value, number)
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
	Images                   bool
	CustomWriter             string
	GeoCoordinates           string
	Zoom                     int
	RunMode                  int
	DisableTelemetry         bool
	WebRunner                bool
	AwsLamdbaRunner          bool
	DataFolder               string
	Proxies                  []string
	AwsAccessKey             string
	AwsSecretKey             string
	AwsRegion                string
	S3Uploader               S3Uploader
	S3Bucket                 string
	AwsLambdaInvoker         bool
	FunctionName             string
	AwsLambdaChunkSize       int
	FastMode                 bool
	Radius                   float64
	Addr                     string
	DisablePageReuse         bool
	ExtraReviews             bool
	MaxResults               int
	WebshareAPIKey           string
}

func ParseConfig() *Config {
	cfg := Config{}

	if os.Getenv("PLAYWRIGHT_INSTALL_ONLY") == "1" {
		cfg.RunMode = RunModeInstallPlaywright

		return &cfg
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
	flag.IntVar(&cfg.Zoom, "zoom", 15, "set zoom level (0-21) for search")
	flag.BoolVar(&cfg.WebRunner, "web", false, "run web server instead of crawling")
	flag.StringVar(&cfg.DataFolder, "data-folder", "webdata", "data folder for web runner")
	flag.StringVar(&proxies, "proxies", "", "comma separated list of proxies to use in the format protocol://user:pass@host:port example: socks5://localhost:9050 or http://user:pass@localhost:9050")
	flag.BoolVar(&cfg.AwsLamdbaRunner, "aws-lambda", false, "run as AWS Lambda function")
	flag.BoolVar(&cfg.AwsLambdaInvoker, "aws-lambda-invoker", false, "run as AWS Lambda invoker")
	flag.StringVar(&cfg.FunctionName, "function-name", "", "AWS Lambda function name")
	flag.StringVar(&cfg.AwsAccessKey, "aws-access-key", "", "AWS access key")
	flag.StringVar(&cfg.AwsSecretKey, "aws-secret-key", "", "AWS secret key")
	flag.StringVar(&cfg.AwsRegion, "aws-region", "", "AWS region")
	flag.StringVar(&cfg.S3Bucket, "s3-bucket", "", "S3 bucket name")
	flag.IntVar(&cfg.AwsLambdaChunkSize, "aws-lambda-chunk-size", 100, "AWS Lambda chunk size")
	flag.BoolVar(&cfg.FastMode, "fast-mode", false, "fast mode (reduced data collection)")
	flag.Float64Var(&cfg.Radius, "radius", 10000, "search radius in meters. Default is 10000 meters")
	flag.StringVar(&cfg.Addr, "addr", ":8080", "address to listen on for web server")
	flag.BoolVar(&cfg.DisablePageReuse, "disable-page-reuse", false, "disable page reuse in playwright")
	flag.BoolVar(&cfg.ExtraReviews, "extra-reviews", false, "enable extra reviews collection")
	flag.IntVar(&cfg.MaxResults, "max-results", 0, "maximum number of results to collect (0 = unlimited)")

	flag.Parse()

	if cfg.AwsAccessKey == "" {
		cfg.AwsAccessKey = os.Getenv("MY_AWS_ACCESS_KEY")
	}

	if cfg.AwsSecretKey == "" {
		cfg.AwsSecretKey = os.Getenv("MY_AWS_SECRET_KEY")
	}

	if cfg.AwsRegion == "" {
		cfg.AwsRegion = os.Getenv("MY_AWS_REGION")
	}

	if cfg.Dsn == "" {
		cfg.Dsn = os.Getenv("DSN")
	}

	// Allow concurrency override via environment variable with dynamic parsing
	if concurrencyEnv := os.Getenv("CONCURRENCY"); concurrencyEnv != "" {
		if c, err := parseConcurrency(concurrencyEnv); err == nil {
			cfg.Concurrency = c
			fmt.Printf("DEBUG: Final concurrency set to: %d\n", cfg.Concurrency)
		} else {
			fmt.Printf("WARNING: Invalid CONCURRENCY value '%s': %v. Using default: %d\n", concurrencyEnv, err, cfg.Concurrency)
		}
	} else {
		cpuCores := runtime.NumCPU()
		fmt.Printf("DEBUG: Using default concurrency: %d (no CONCURRENCY env var set, system has %d cores)\n", cfg.Concurrency, cpuCores)
	}

	// Do not force concurrency in debug mode; keep user/provider choice intact

	if cfg.AwsLambdaInvoker && cfg.FunctionName == "" {
		panic("FunctionName must be provided when using AwsLambdaInvoker")
	}

	if cfg.AwsLambdaInvoker && cfg.S3Bucket == "" {
		panic("S3Bucket must be provided when using AwsLambdaInvoker")
	}

	if cfg.AwsLambdaInvoker && cfg.InputFile == "" {
		panic("InputFile must be provided when using AwsLambdaInvoker")
	}

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

	fmt.Printf("DEBUG: PROXIES env var: '%s'\n", os.Getenv("PROXIES"))
	fmt.Printf("DEBUG: CLI proxies flag: '%s'\n", proxies)

	// Priority: CLI proxies > Webshare API > No proxies
	if proxies != "" {
		cfg.Proxies = strings.Split(proxies, ",")
		fmt.Printf("DEBUG: CLI proxies configured: %d entries\n", len(cfg.Proxies))
		fmt.Printf("DEBUG: CLI proxy values: %v\n", cfg.Proxies)
	} else if os.Getenv("PROXIES") != "" {
		// Informative log: PROXIES env is set but ignored unless -proxies flag is provided
		fmt.Println("DEBUG: PROXIES env detected but not used; pass with -proxies to enable")
	}

	// Check for Webshare API key
	if cfg.WebshareAPIKey == "" {
		cfg.WebshareAPIKey = os.Getenv("WEBSHARE_API_KEY")
	}

	// Fetch proxies from Webshare API if no manual proxies provided and API key exists
	if len(cfg.Proxies) == 0 && cfg.WebshareAPIKey != "" {
		fmt.Println("🔧 Webshare API key detected, fetching proxies dynamically...")
		webshareClient := webshare.NewClient(cfg.WebshareAPIKey)

		// Ensure IP is authorized
		if err := webshareClient.EnsureIPAuthorized(); err != nil {
			fmt.Printf("⚠️ Warning: Failed to authorize IP with Webshare: %v\n", err)
			fmt.Println("⚠️ Continuing without proxies. You may need to manually authorize your IP.")
		} else {
			// Fetch proxy list
			proxyList, err := webshareClient.GetProxiesForScraper("direct")
			if err != nil {
				fmt.Printf("⚠️ Warning: Failed to fetch proxies from Webshare: %v\n", err)
				fmt.Println("⚠️ Continuing without proxies.")
			} else {
				cfg.Proxies = proxyList
				fmt.Printf("✅ Successfully loaded %d proxies from Webshare API\n", len(cfg.Proxies))
			}
		}
	}

	if cfg.AwsAccessKey != "" && cfg.AwsSecretKey != "" && cfg.AwsRegion != "" {
		cfg.S3Uploader = s3uploader.New(cfg.AwsAccessKey, cfg.AwsSecretKey, cfg.AwsRegion)
	}

	switch {
	case cfg.AwsLambdaInvoker:
		cfg.RunMode = RunModeAwsLambdaInvoker
	case cfg.AwsLamdbaRunner:
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
