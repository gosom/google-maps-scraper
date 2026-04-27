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
