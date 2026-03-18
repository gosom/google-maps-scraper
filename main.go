package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/google-maps-scraper/runner/databaserunner"
	"github.com/gosom/google-maps-scraper/runner/filerunner"
	"github.com/gosom/google-maps-scraper/runner/installplaywright"
	"github.com/gosom/google-maps-scraper/runner/lambdaaws"
	"github.com/gosom/google-maps-scraper/runner/webrunner"
	"github.com/joho/godotenv"
)

// version is injected at build time via ldflags:
//
//	go build -ldflags "-X main.version=$(git rev-parse --short HEAD)"
//
// Falls back to "dev" when running without build flags.
var version = "dev"

func main() {
	_ = godotenv.Load() // Load .env file if present

	// Security guard: BRAZA_DEV_AUTH_BYPASS must never be enabled in production.
	// If both flags are set simultaneously the server refuses to start to prevent
	// accidental complete auth bypass on production deployments.
	if strings.TrimSpace(os.Getenv("BRAZA_DEV_AUTH_BYPASS")) == "1" &&
		strings.TrimSpace(os.Getenv("APP_ENV")) == "production" {
		fmt.Fprintln(os.Stderr, "FATAL: BRAZA_DEV_AUTH_BYPASS=1 must not be set when APP_ENV=production — refusing to start")
		os.Exit(1)
	}

	// Set structured JSON logging as the global default before anything else.
	slog.SetDefault(pkglogger.New(os.Getenv("LOG_LEVEL")))

	ctx, cancel := context.WithCancel(context.Background())

	// Parse config first so banner can reflect debug mode
	cfg, err := runner.ParseConfig()
	if err != nil {
		slog.Error("invalid_configuration", slog.Any("error", err))
		cancel()
		os.Exit(1)
	}

	// Propagate build-time version into config so the health endpoint can report it.
	cfg.Version = version

	runner.BannerWithDebug(cfg.Debug)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan

		slog.Info("signal_received", slog.String("action", "shutting_down"))

		cancel()
	}()

	runnerInstance, err := runnerFactory(cfg)
	if err != nil {
		cancel()
		os.Stderr.WriteString(err.Error() + "\n")

		runner.Telemetry().Close()

		os.Exit(1)
	}

	if err := runnerInstance.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		os.Stderr.WriteString(err.Error() + "\n")

		_ = runnerInstance.Close(ctx)
		runner.Telemetry().Close()

		cancel()

		os.Exit(1)
	}

	_ = runnerInstance.Close(ctx)
	runner.Telemetry().Close()

	cancel()

	os.Exit(0)
}

func runnerFactory(cfg *runner.Config) (runner.Runner, error) {
	switch cfg.RunMode {
	case runner.RunModeFile:
		return filerunner.New(cfg)
	case runner.RunModeDatabase, runner.RunModeDatabaseProduce:
		return databaserunner.New(cfg)
	case runner.RunModeInstallPlaywright:
		return installplaywright.New(cfg)
	case runner.RunModeWeb:
		return webrunner.New(cfg)
	case runner.RunModeAwsLambda:
		return lambdaaws.New(cfg)
	case runner.RunModeAwsLambdaInvoker:
		return lambdaaws.NewInvoker(cfg)
	default:
		return nil, fmt.Errorf("%w: %d", runner.ErrInvalidRunMode, cfg.RunMode)
	}
}
