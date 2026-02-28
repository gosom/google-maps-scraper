package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdadmin"
	"github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdprovision"
	"github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdserve"
	"github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdupdate"
	"github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdworker"
	"github.com/gosom/google-maps-scraper/log"

	// Register infrastructure providers.
	_ "github.com/gosom/google-maps-scraper/infra/digitalocean"
	_ "github.com/gosom/google-maps-scraper/infra/hetzner"
)

func main() {
	cmd := &cli.Command{
		Name:    "gmapssaas",
		Usage:   "Google Maps Scraper Pro",
		Version: "1.0.0",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "debug",
				Usage: "Enable debug logging",
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			level := slog.LevelInfo
			if cmd.Bool("debug") {
				level = slog.LevelDebug
			}
			log.Init(level)
			return ctx, nil
		},
		Commands: []*cli.Command{
			cmdserve.Command,
			cmdworker.Command,
			cmdprovision.Command,
			cmdupdate.Command,
			cmdadmin.Command,
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Error("application failed", "error", err)
		os.Exit(1)
	}
}
