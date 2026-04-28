package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	pkgconfig "github.com/gosom/google-maps-scraper/pkg/config"
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

	// Build-time short-circuit: when invoked with PLAYWRIGHT_INSTALL_ONLY=1
	// (Docker image build step), skip env-config validation entirely. The
	// install-playwright path only invokes playwright.Install and needs no
	// runtime env vars; without this guard pkgconfig.Load() would fail on
	// missing required vars (DSN, CLERK_SECRET_KEY, etc.) at build time.
	if os.Getenv("PLAYWRIGHT_INSTALL_ONLY") == "1" {
		runInstallPlaywrightOnly()
		return
	}

	// Load typed env config first so the logger can be built from validated,
	// typed values rather than raw os.Getenv calls.
	// Use a temporary stderr logger for any config-load failures.
	appCfg, err := pkgconfig.Load()
	if err != nil {
		slog.Error("config_load_failed", slog.Any("error", err))
		os.Exit(1)
	}

	// Build the single root logger from typed config. All downstream code
	// receives this logger via constructor injection — no further os.Getenv
	// calls for LOG_LEVEL or log rotation settings.
	logger := pkglogger.New(appCfg.LogLevel, pkglogger.LogConfig{
		Output:        appCfg.Log.Output,
		FilePath:      appCfg.Log.FilePath,
		Dir:           appCfg.Log.Dir,
		FileName:      appCfg.Log.FileName,
		MaxSizeMB:     appCfg.Log.MaxSizeMB,
		RetentionDays: appCfg.Log.RetentionDays,
	})
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())

	// Parse CLI-flag config (separate from env config).
	cfg, err := runner.ParseConfig()
	if err != nil {
		slog.Error("invalid_configuration", slog.Any("error", err))
		cancel()
		os.Exit(1)
	}

	// Merge standard AWS_* env values into the CLI-flag config.
	// CLI flags take precedence; env values from pkg/config fill in the gaps.
	runner.MergeAWSDefaults(cfg, appCfg)

	// Build the S3 uploader now that AWS credentials are fully resolved.
	// This must run after MergeAWSDefaults so that env-only deployments
	// (credentials supplied via AWS_ACCESS_KEY_ID etc.) get an uploader.
	if err := runner.BuildS3Uploader(cfg, logger); err != nil {
		slog.Error("s3_uploader_init_failed", slog.Any("error", err))
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

	runnerInstance, err := runnerFactory(cfg, appCfg, logger)
	if err != nil {
		cancel()
		os.Stderr.WriteString(err.Error() + "\n")

		runner.Telemetry().Close()

		os.Exit(1)
	}

	if err := runnerInstance.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		os.Stderr.WriteString(err.Error() + "\n")

		if closeErr := runnerInstance.Close(ctx); closeErr != nil {
			slog.Warn("runner_close_failed", slog.Any("error", closeErr))
		}
		runner.Telemetry().Close()

		cancel()

		os.Exit(1)
	}

	if closeErr := runnerInstance.Close(ctx); closeErr != nil {
		slog.Warn("runner_close_failed", slog.Any("error", closeErr))
	}
	runner.Telemetry().Close()

	cancel()

	os.Exit(0)
}

func runnerFactory(cfg *runner.Config, appCfg *pkgconfig.Config, logger *slog.Logger) (runner.Runner, error) {
	switch cfg.RunMode {
	case runner.RunModeFile:
		return filerunner.New(cfg, logger.With(slog.String("component", "filerunner")))
	case runner.RunModeDatabase, runner.RunModeDatabaseProduce:
		return databaserunner.New(cfg, appCfg, logger.With(slog.String("component", "databaserunner")))
	case runner.RunModeInstallPlaywright:
		return installplaywright.New(cfg)
	case runner.RunModeWeb:
		return webrunner.New(cfg, appCfg, logger.With(slog.String("component", "webrunner")))
	case runner.RunModeAwsLambda:
		return lambdaaws.New(cfg, logger.With(slog.String("component", "lambdaaws")))
	case runner.RunModeAwsLambdaInvoker:
		return lambdaaws.NewInvoker(cfg, logger.With(slog.String("component", "invoker")))
	default:
		return nil, fmt.Errorf("%w: %d", runner.ErrInvalidRunMode, cfg.RunMode)
	}
}

// runInstallPlaywrightOnly is the Docker build-time entry point. It bypasses
// pkgconfig.Load() (which would fail on missing required env vars) and runs
// only playwright.Install. Triggered by PLAYWRIGHT_INSTALL_ONLY=1.
func runInstallPlaywrightOnly() {
	cfg := &runner.Config{RunMode: runner.RunModeInstallPlaywright}
	r, err := installplaywright.New(cfg)
	if err != nil {
		slog.Error("playwright_install_init_failed", slog.Any("error", err))
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Run(ctx); err != nil {
		slog.Error("playwright_install_failed", slog.Any("error", err))
		os.Exit(1)
	}
	if err := r.Close(ctx); err != nil {
		slog.Error("playwright_install_close_failed", slog.Any("error", err))
	}
	slog.Info("playwright_install_complete")
}
