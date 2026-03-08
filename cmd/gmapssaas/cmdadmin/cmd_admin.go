package cmdadmin

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/gosom/google-maps-scraper/admin"
	adminpg "github.com/gosom/google-maps-scraper/admin/postgres"
	gocli "github.com/gosom/google-maps-scraper/cli"
	"github.com/gosom/google-maps-scraper/cryptoext"
	"github.com/gosom/google-maps-scraper/env"
	saas "github.com/gosom/google-maps-scraper/saas"
)

var Command = &cli.Command{
	Name:  "admin",
	Usage: "Admin tasks",
	Commands: []*cli.Command{
		{
			Name:  "create-user",
			Usage: "Create or update a user",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "username",
					Aliases:  []string{"u"},
					Usage:    "Username of the user",
					Value:    "admin",
					Required: false,
				},
				&cli.StringFlag{
					Name:    "password",
					Aliases: []string{"p"},
					Usage:   "Password (generated if not provided)",
				},
				&cli.StringFlag{
					Name:    "database-url",
					Usage:   "PostgreSQL connection string",
					Value:   "postgres://postgres:postgres@localhost:5432/gmaps_pro?sslmode=disable",
					Sources: cli.EnvVars(saas.EnvDatabaseURL),
				},
				&cli.StringFlag{
					Name:     "encryption-key",
					Usage:    "Hex-encoded 32-byte encryption key for sensitive data",
					Sources:  cli.EnvVars(saas.EnvEncryptionKey),
					Value:    "0398d4cad290e145cb8242bb74e045264564d384d33ada80ff7702e460e6956c",
					Required: false,
				},
			},
			Action: runCreateUser,
		},
	},
}

func runCreateUser(ctx context.Context, cmd *cli.Command) error {
	gocli.PrintBanner("Google Maps Scraper Pro - Admin User Management")

	username := cmd.String("username")
	password := cmd.String("password")
	databaseURL := cmd.String("database-url")

	env.LogUnsetEnvs(saas.EnvDatabaseURL, saas.EnvEncryptionKey)

	encryptionKey, err := cryptoext.ParseEncryptionKey(cmd.String("encryption-key"))
	if err != nil {
		return err
	}

	store, err := adminpg.New(ctx, databaseURL, encryptionKey)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	fmt.Printf("Creating/updating user '%s'...\n", username)

	// Generate password if not provided
	if password == "" {
		password = cryptoext.GenerateRandomHex(16)
	}

	// Check if user exists
	existingUser, err := store.GetUser(ctx, username)
	if err != nil && err != admin.ErrUserNotFound {
		return fmt.Errorf("failed to check existing user: %w", err)
	}

	var userCreated bool

	if existingUser != nil {
		if err := store.UpdatePassword(ctx, username, password); err != nil {
			return fmt.Errorf("failed to update password: %w", err)
		}

		fmt.Println("Password updated for existing user")
	} else {
		_, err := store.CreateUser(ctx, username, password)
		if err != nil {
			return fmt.Errorf("failed to create user: %w", err)
		}

		userCreated = true

		fmt.Println("New user created")
	}

	gocli.PrintCredentials(username, password, "", userCreated)

	return nil
}
