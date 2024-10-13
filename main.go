package main

import (
	"context"
	"fmt"
	"os"

	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/google-maps-scraper/runner/databaserunner"
	"github.com/gosom/google-maps-scraper/runner/filerunner"
	"github.com/gosom/google-maps-scraper/runner/installplaywright"
)

func main() {
	ctx := context.Background()
	cfg := runner.ParseConfig()

	runnerInstance, err := runnerFactory(cfg)
	if err != nil {
		os.Stderr.WriteString(err.Error() + "\n")

		runner.Telemetry().Close()

		os.Exit(1)
	}

	if err := runnerInstance.Run(ctx); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")

		_ = runnerInstance.Close(ctx)
		runner.Telemetry().Close()

		os.Exit(1)
	}

	_ = runnerInstance.Close(ctx)
	runner.Telemetry().Close()

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
	default:
		return nil, fmt.Errorf("%w: %d", runner.ErrInvalidRunMode, cfg.RunMode)
	}
}
