package cmdserve

import (
	"context"
	"fmt"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"github.com/urfave/cli/v3"

	"github.com/gosom/google-maps-scraper/admin"
	adminpostgres "github.com/gosom/google-maps-scraper/admin/postgres"
	"github.com/gosom/google-maps-scraper/api"
	_ "github.com/gosom/google-maps-scraper/api/docs" // registers swagger docs
	apipostgres "github.com/gosom/google-maps-scraper/api/postgres"
	"github.com/gosom/google-maps-scraper/cryptoext"
	"github.com/gosom/google-maps-scraper/env"
	"github.com/gosom/google-maps-scraper/httpext"
	"github.com/gosom/google-maps-scraper/log"
	"github.com/gosom/google-maps-scraper/postgres"
	ratelimitpostgres "github.com/gosom/google-maps-scraper/ratelimit/postgres"
	"github.com/gosom/google-maps-scraper/rqueue"
	saas "github.com/gosom/google-maps-scraper/saas"
)

var Command = &cli.Command{
	Name:  "serve",
	Usage: "Start the API server",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "addr",
			Usage:   "Server listen address",
			Value:   ":8080",
			Sources: cli.EnvVars(saas.EnvAddr),
		},
		&cli.StringFlag{
			Name:    "database-url",
			Usage:   "PostgreSQL connection string",
			Value:   "postgres://postgres:postgres@localhost:5432/gmaps_pro?sslmode=disable",
			Sources: cli.EnvVars(saas.EnvDatabaseURL),
		},
		&cli.IntFlag{
			Name:    "db-max-conns",
			Usage:   "Maximum database connections",
			Value:   10,
			Sources: cli.EnvVars(saas.EnvDBMaxConns),
		},
		&cli.IntFlag{
			Name:    "db-min-conns",
			Usage:   "Minimum database connections",
			Value:   2,
			Sources: cli.EnvVars(saas.EnvDBMinConns),
		},
		&cli.DurationFlag{
			Name:    "db-max-conn-lifetime",
			Usage:   "Maximum connection lifetime",
			Value:   time.Hour,
			Sources: cli.EnvVars(saas.EnvDBMaxConnLifetime),
		},
		&cli.DurationFlag{
			Name:    "db-max-conn-idle-time",
			Usage:   "Maximum connection idle time",
			Value:   30 * time.Minute,
			Sources: cli.EnvVars(saas.EnvDBMaxConnIdleTime),
		},
		&cli.StringFlag{
			Name:     "encryption-key",
			Usage:    "Hex-encoded 32-byte encryption key for sensitive data. Generate with: openssl rand -hex 32",
			Sources:  cli.EnvVars(saas.EnvEncryptionKey),
			Required: true,
		},
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		addr := cmd.String("addr")
		dsn := cmd.String("database-url")

		// Connect to database
		dbPool, err := postgres.Connect(ctx, dsn,
			postgres.WithMaxConns(int32(cmd.Int("db-max-conns"))),
			postgres.WithMinConns(int32(cmd.Int("db-min-conns"))),
			postgres.WithMaxConnLifetime(cmd.Duration("db-max-conn-lifetime")),
			postgres.WithMaxConnIdleTime(cmd.Duration("db-max-conn-idle-time")),
		)
		if err != nil {
			return err
		}
		defer dbPool.Close()

		// Parse and validate encryption key
		encKeyHex := cmd.String("encryption-key")
		if encKeyHex == "0398d4cad290e145cb8242bb74e045264564d384d33ada80ff7702e460e6956c" {
			return fmt.Errorf("ENCRYPTION_KEY must not be the default value. Generate one with: openssl rand -hex 32")
		}

		encryptionKey, err := cryptoext.ParseEncryptionKey(encKeyHex)
		if err != nil {
			return fmt.Errorf("invalid ENCRYPTION_KEY (must be 64 hex chars / 32 bytes): %w. Generate one with: openssl rand -hex 32", err)
		}

		env.LogUnsetEnvs(saas.EnvDatabaseURL, saas.EnvEncryptionKey)

		// Create stores
		adminStore := adminpostgres.NewWithPool(dbPool, encryptionKey)
		apiStore := apipostgres.New(dbPool)

		// Store database URL in config (encrypted)
		dsnConfig := &admin.AppConfig{
			Key:   "database_url",
			Value: dsn,
		}
		if err = adminStore.SetConfig(ctx, dsnConfig, true); err != nil {
			return err
		}

		// Create River queue client (processes maintenance queue for worker provisioning)
		rqueueClient, err := rqueue.NewClient(dbPool, encryptionKey)
		if err != nil {
			return err
		}

		if err = rqueueClient.Start(ctx); err != nil {
			return err
		}

		riverUIHandler, err := rqueue.CreateRiverUIHandler(ctx, rqueueClient)
		if err != nil {
			return err
		}

		if err = riverUIHandler.Start(ctx); err != nil {
			return err
		}

		// Create rate limiter
		rateLimiter := ratelimitpostgres.New(dbPool)

		// Create AppStates
		adminState, err := admin.NewAppState(adminStore, rateLimiter, encryptionKey)
		if err != nil {
			return err
		}

		adminState.RQueueClient = rqueueClient

		apiState := api.NewAppState(rqueueClient, apiStore)

		// Setup router
		mainRouter := chi.NewRouter()
		mainRouter.Use(middleware.Recoverer)

		// Setup admin routes
		admin.Routes(mainRouter, adminState, riverUIHandler)

		// Setup API routes (in a group so middleware can be added)
		mainRouter.Group(func(r chi.Router) {
			api.Routes(r, apiState)
		})

		// Swagger UI
		mainRouter.Get("/swagger/*", httpSwagger.Handler(
			httpSwagger.URL("/swagger/doc.json"),
		))

		srv, err := httpext.New(mainRouter, httpext.WithAddr(addr))
		if err != nil {
			return err
		}

		log.Info("starting server", "addr", addr)

		return srv.Run(ctx)
	},
}
