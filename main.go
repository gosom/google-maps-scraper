package main

import (
	"context"
	"log"
	"os"

	// postgres driver
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"

	"github.com/playwright-community/playwright-go"

	"github.com/gosom/google-maps-scraper/api/router"
	"github.com/gosom/google-maps-scraper/constants"
	"github.com/gosom/google-maps-scraper/jobs"

	"github.com/gosom/google-maps-scraper/models"
)

func main() {
	// just install playwright
	if os.Getenv("PLAYWRIGHT_INSTALL_ONLY") == "1" {
		if err := installPlaywright(); err != nil {
			os.Exit(1)
		}

		os.Exit(0)
	}

	if err := run(); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")

		os.Exit(1)

		return
	}

	os.Exit(0)
}

func run() error {
	ctx := context.Background()
	args := models.ParseArgs()
	err := godotenv.Load()
	if err != nil {
		log.Println("[WARN] Error loading .env file")
	}
	if args.Api {
		go jobs.RunFromDatabase(ctx, &args)
		return router.RouterRegister(args)
	}
	if args.Dsn == "" && len(os.Getenv(constants.POSTGREST_CONN)) <= 0 {
		return jobs.RunFromLocalFile(ctx, &args)
	}

	return jobs.RunFromDatabase(ctx, &args)
}

func installPlaywright() error {
	return playwright.Install()
}
