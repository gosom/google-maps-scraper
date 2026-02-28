package cmdprovision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/gosom/google-maps-scraper/admin"
	adminpg "github.com/gosom/google-maps-scraper/admin/postgres"
	gocli "github.com/gosom/google-maps-scraper/cli"
	"github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdcommon"
	"github.com/gosom/google-maps-scraper/cryptoext"
	"github.com/gosom/google-maps-scraper/infra"
	"github.com/gosom/google-maps-scraper/infra/digitalocean"
	"github.com/gosom/google-maps-scraper/infra/hetzner"
	"github.com/gosom/google-maps-scraper/infra/vps"
	"github.com/gosom/google-maps-scraper/migrations"
	"github.com/gosom/google-maps-scraper/postgres"
)

type providerResult struct {
	provisioner infra.Provisioner
	setup       func(ctx context.Context) error
	getAppURL   func() string // optional: returns the application URL after deploy
}

type providerFactory func(ctx context.Context, p *gocli.Prompter, state *ProvisionState) (*providerResult, error)

var Command = &cli.Command{
	Name:  "provision",
	Usage: "Provision infrastructure",
	Action: func(ctx context.Context, _ *cli.Command) error {
		return run(ctx)
	},
}

//nolint:gocyclo // complexity inherent in multi-step interactive provisioning wizard
func run(ctx context.Context) error {
	if err := EnsureStateDir(); err != nil {
		return err
	}

	p := gocli.NewPrompter(os.Stdin)

	state, err := LoadState()
	if err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}

	if state != nil {
		resume, err := p.Confirm("Found existing provisioning state. Resume?")
		if err != nil {
			return err
		}

		if !resume {
			if err := DeleteState(); err != nil {
				return fmt.Errorf("failed to delete state: %w", err)
			}

			state = nil
		}
	}

	if state == nil {
		state = &ProvisionState{
			StartedAt: time.Now(),
		}
	}

	// Handle registry/image build first so we fail fast before
	// creating any cloud resources.
	// GMAPSSAAS_IMAGE skips the build prompt and uses the given image directly.
	if img := os.Getenv("GMAPSSAAS_IMAGE"); img != "" {
		state.Registry = &infra.RegistryConfig{
			URL:   "ghcr.io",
			Image: img,
		}
		state.Steps.ImagePushed = true

		if err := SaveState(state); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}

		fmt.Printf("Using image: %s\n", img)
	} else {
		if state.Steps.ImagePushed {
			fmt.Printf("Image already pushed: %s/%s\n", state.Registry.URL, state.Registry.Image)

			rebuild, err := p.Confirm("Do you want to rebuild and push again?")
			if err != nil {
				return err
			}

			if rebuild {
				state.Steps.ImagePushed = false
			}
		}

		if !state.Steps.ImagePushed {
			if err := buildAndPushImage(p, state); err != nil {
				return err
			}

			state.Steps.ImagePushed = true

			if err := SaveState(state); err != nil {
				return fmt.Errorf("failed to save state: %w", err)
			}
		}
	}

	options := []gocli.Option[providerFactory]{
		{Label: "VPS (bring your own server)", Value: initVPS},
		{Label: "DigitalOcean (App Platform)", Value: initDO},
		{Label: "Hetzner Cloud", Value: initHetzner},
	}

	var factory providerFactory

	if state.Provider != "" {
		for _, opt := range options {
			if opt.Label == state.Provider {
				factory = opt.Value

				break
			}
		}
	}

	if factory == nil {
		factory, err = gocli.Select(p, "Where do you want to install?", options)
		if err != nil {
			return err
		}

		for _, opt := range options {
			if fmt.Sprintf("%p", opt.Value) == fmt.Sprintf("%p", factory) {
				state.Provider = opt.Label

				break
			}
		}
	}

	result, err := factory(ctx, p, state)
	if err != nil {
		return err
	}

	if err := SaveState(state); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	if !state.Steps.ConnectivityChecked {
		fmt.Println("Checking connectivity (server may still be booting)...")
		time.Sleep(5 * time.Second)

		var connErr error
		for i := range 24 {
			connErr = result.provisioner.CheckConnectivity(ctx)
			if connErr == nil {
				break
			}

			fmt.Printf("  attempt %d/24: %v — retrying in 10s...\n", i+1, connErr)
			time.Sleep(10 * time.Second)
		}

		if connErr != nil {
			return fmt.Errorf("connectivity check failed: %w", connErr)
		}

		fmt.Println("Connected successfully!")

		state.Steps.ConnectivityChecked = true

		if err := SaveState(state); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	} else {
		fmt.Println("Connectivity already verified, skipping...")
	}

	if !state.Steps.SetupCompleted {
		if err := result.setup(ctx); err != nil {
			return err
		}

		state.Steps.SetupCompleted = true

		if err := SaveState(state); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	} else {
		fmt.Println("Setup already completed, skipping...")
	}

	if !state.Steps.DatabaseCreated {
		dbConnURL, err := setupDatabase(ctx, p, result.provisioner, state)
		if err != nil {
			return err
		}

		state.DatabaseURL = dbConnURL
		state.Steps.DatabaseCreated = true

		if err := SaveState(state); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	}

	fmt.Println("Verifying database connection...")
	time.Sleep(3 * time.Second)

	var dbErr error

	for range 5 {
		conn, err := postgres.Connect(ctx, state.DatabaseURL)
		if err != nil {
			dbErr = err

			fmt.Println("Database connection failed, retrying...")
			time.Sleep(5 * time.Second)

			continue
		}

		conn.Close()

		dbErr = nil

		break
	}

	if dbErr != nil {
		return fmt.Errorf("failed to connect to database: %w", dbErr)
	}

	fmt.Printf("Database connection URL: %s\n", state.DatabaseURL)

	fmt.Println("Running database migrations...")

	n, err := migrations.RunWithDSN(state.DatabaseURL)
	if err != nil {
		return fmt.Errorf("migrations failed: %w", err)
	}

	fmt.Printf("Applied %d migration(s)\n", n)

	if state.EncryptionKey == "" {
		fmt.Println("Generating encryption key...")

		_, hexKey, err := cryptoext.GenerateEncryptionKey()
		if err != nil {
			return fmt.Errorf("failed to generate encryption key: %w", err)
		}

		state.EncryptionKey = hexKey

		if err := SaveState(state); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}

		fmt.Println("Encryption key generated and saved to state")
	} else {
		fmt.Println("Using existing encryption key from state")
	}

	if state.HashSalt == "" {
		fmt.Println("Generating hash salt...")

		hashIDSalt := cryptoext.GenerateRandomHex(16)
		state.HashSalt = hashIDSalt

		if err := SaveState(state); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	}

	if state.SSHKey == nil {
		fmt.Println("Generating SSH key pair...")

		pub, priv, err := cryptoext.GenerateSSHKey()
		if err != nil {
			return fmt.Errorf("failed to generate SSH key pair: %w", err)
		}

		state.SSHKey = &infra.SSHKey{
			Pub: pub,
			Key: priv,
		}

		if err := SaveState(state); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	}

	fmt.Println("Saving configuration to database...")

	store, err := adminpg.New(ctx, state.DatabaseURL, cryptoext.MustParseEncryptionKey(state.EncryptionKey))
	if err != nil {
		return fmt.Errorf("failed to create store: %w", err)
	}

	valuesToSave := map[string]string{
		admin.DatabaseURLKey: state.DatabaseURL,
		admin.RegistryProviderSettingsKey: func() string {
			b, err := json.Marshal(state.Registry)
			if err != nil {
				panic(err)
			}

			return string(b)
		}(),
		admin.HashSaltKey: state.HashSalt,
	}

	if state.SSHKey != nil {
		b, err := json.Marshal(state.SSHKey)
		if err != nil {
			return fmt.Errorf("failed to marshal SSH key: %w", err)
		}

		valuesToSave[admin.AppKeyPairKey] = string(b)
	}

	if state.DO != nil {
		valuesToSave[admin.DOTokenKey] = state.DO.Token
	}

	if state.Hetzner != nil {
		valuesToSave[admin.HetznerTokenKey] = state.Hetzner.Token
	}

	for key, value := range valuesToSave {
		cfg := admin.AppConfig{
			Key:   key,
			Value: value,
		}
		if err = store.SetConfig(ctx, &cfg, true); err != nil {
			return fmt.Errorf("failed to save config %s: %w", key, err)
		}
	}

	if state.Steps.Deployed {
		fmt.Println("Application already deployed.")

		redeploy, err := p.Confirm("Do you want to redeploy?")
		if err != nil {
			return err
		}

		if redeploy {
			state.Steps.Deployed = false
		}
	}

	if !state.Steps.Deployed {
		fmt.Println("Deploying application...")

		deployConfig := &infra.DeployConfig{
			Registry:      state.Registry,
			DatabaseURL:   state.DatabaseURL,
			EncryptionKey: state.EncryptionKey,
			HashSalt:      state.HashSalt,
		}

		if err := result.provisioner.Deploy(ctx, deployConfig); err != nil {
			return fmt.Errorf("deployment failed: %w", err)
		}

		state.Steps.Deployed = true

		if err := SaveState(state); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}

		fmt.Println("Application deployed successfully!")
	}

	if state.AdminUsername == "" {
		fmt.Println("Creating admin user...")

		state.AdminUsername = "admin"
		state.AdminPassword = cryptoext.GenerateRandomHex(12)

		_, err = store.CreateUser(ctx, state.AdminUsername, state.AdminPassword)
		if err != nil {
			return fmt.Errorf("failed to create admin user: %w", err)
		}

		if err := SaveState(state); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	}

	// Print summary
	var appURL string
	if result.getAppURL != nil {
		appURL = result.getAppURL()
	}

	if appURL == "" && state.VPS != nil {
		appURL = "https://" + state.VPS.Host
		if state.VPS.Domain != "" {
			appURL = "https://" + state.VPS.Domain
		}
	}

	if appURL == "" {
		appURL = "(check provider dashboard)"
	}

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println("                   PROVISIONING COMPLETE")
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println()
	fmt.Printf("  Application URL:  %s\n", appURL)
	fmt.Printf("  Username:         %s\n", state.AdminUsername)
	fmt.Printf("  Password:         %s\n", state.AdminPassword)
	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println()

	return nil
}

type dbStrategy func(ctx context.Context, p *gocli.Prompter, prov infra.Provisioner, state *ProvisionState) (string, error)

func setupDatabase(ctx context.Context, p *gocli.Prompter, prov infra.Provisioner, state *ProvisionState) (string, error) {
	options := []gocli.Option[dbStrategy]{
		{Label: "Create a new database", Value: createNewDB},
		{Label: "Use an existing database", Value: useExistingDB},
	}

	strategy, err := gocli.Select(p, "Database setup:", options)
	if err != nil {
		return "", err
	}

	return strategy(ctx, p, prov, state)
}

func createNewDB(ctx context.Context, p *gocli.Prompter, prov infra.Provisioner, state *ProvisionState) (string, error) {
	// For DO, prompt for database size and region before creating
	if state.DO != nil && state.DO.DBSize == "" {
		doProv := digitalocean.NewAppPlatform(state.DO)

		pgOpts, err := doProv.ListDatabaseOptions(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to list database options: %w", err)
		}

		// Prompt for region
		var regionOptions []gocli.Option[string]
		for _, r := range pgOpts.Regions {
			regionOptions = append(regionOptions, gocli.Option[string]{Label: r, Value: r})
		}

		dbRegion, err := gocli.Select(p, "Select database region:", regionOptions)
		if err != nil {
			return "", err
		}

		state.DO.DBRegion = dbRegion

		// Collect available sizes from layouts (single node)
		var sizeOptions []gocli.Option[string]

		for _, layout := range pgOpts.Layouts {
			if layout.NodeNum == 1 {
				for _, s := range layout.Sizes {
					sizeOptions = append(sizeOptions, gocli.Option[string]{Label: s, Value: s})
				}

				break
			}
		}

		if len(sizeOptions) == 0 {
			return "", fmt.Errorf("no database sizes available")
		}

		dbSize, err := gocli.Select(p, "Select database size:", sizeOptions)
		if err != nil {
			return "", err
		}

		state.DO.DBSize = dbSize

		if err := SaveState(state); err != nil {
			return "", fmt.Errorf("failed to save state: %w", err)
		}
	}

	fmt.Println("Creating database...")

	dbInfo, err := prov.CreateDatabase(ctx)
	if err != nil {
		return "", err
	}

	return dbInfo.ConnectionURL, nil
}

func useExistingDB(_ context.Context, p *gocli.Prompter, _ infra.Provisioner, _ *ProvisionState) (string, error) {
	connURL, err := p.Input("PostgreSQL connection URL", "")
	if err != nil {
		return "", err
	}

	if connURL == "" {
		return "", fmt.Errorf("connection URL is required")
	}

	return connURL, nil
}

func buildAndPushImage(p *gocli.Prompter, state *ProvisionState) error {
	confirm, err := p.Confirm("Do you want to build and push a new image?")
	if err != nil {
		return err
	}

	if !confirm {
		imageName, err := p.Input("Image name (e.g., ghcr.io/gosom/google-maps-scraper-saas:latest)", "ghcr.io/gosom/google-maps-scraper-saas:latest")
		if err != nil {
			return err
		}

		if imageName == "" {
			return fmt.Errorf("image name is required")
		}

		if state.Registry == nil {
			state.Registry = &infra.RegistryConfig{
				URL:   "ghcr.io",
				Image: imageName,
			}
		} else {
			state.Registry.Image = imageName
		}

		fmt.Println("Using existing image from registry.")

		return nil
	}

	if state.Registry == nil {
		registryURL, err := p.Input("Registry URL (e.g., ghcr.io)", "ghcr.io")
		if err != nil {
			return err
		}

		username, err := p.Input("Registry username", "")
		if err != nil {
			return err
		}

		token, err := p.Input("Registry token/PAT", "")
		if err != nil {
			return err
		}

		imageName, err := p.Input("Image name (e.g., username/gmapssaas:latest)", "ghcr.io/gosom/google-maps-scraper-saas:latest")
		if err != nil {
			return err
		}

		if imageName == "" {
			return fmt.Errorf("image name is required")
		}

		state.Registry = &infra.RegistryConfig{
			URL:      registryURL,
			Username: username,
			Token:    token,
			Image:    imageName,
		}
	}

	if err := cmdcommon.BuildAndPushImage(state.Registry); err != nil {
		if errors.Is(err, cmdcommon.ErrDockerLoginFailed) {
			state.Registry = nil
		}

		return err
	}

	return nil
}

func toAbsolutePath(path string) (string, error) {
	if path == "" {
		return path, nil
	}

	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}

		path = home + path[1:]
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	return absPath, nil
}

func initVPS(_ context.Context, p *gocli.Prompter, state *ProvisionState) (*providerResult, error) {
	var (
		host, port, user, keyPath, domain string
		err                               error
	)

	if state.VPS != nil {
		host = state.VPS.Host
		port = state.VPS.Port
		user = state.VPS.User
		keyPath = state.VPS.KeyPath
		domain = state.VPS.Domain

		fmt.Println("Using saved VPS configuration:")
		fmt.Printf("  Host: %s\n", host)
		fmt.Printf("  Port: %s\n", port)
		fmt.Printf("  User: %s\n", user)
		fmt.Printf("  Key:  %s\n", keyPath)
		fmt.Printf("  Domain: %s\n", domain)
	} else {
		host, err = p.Input("Host", "")
		if err != nil {
			return nil, err
		}

		port, err = p.Input("SSH Port", "22")
		if err != nil {
			return nil, err
		}

		user, err = p.Input("User", "root")
		if err != nil {
			return nil, err
		}

		keyPath, err = p.Input("Path to private key", "~/.ssh/id_ed25519")
		if err != nil {
			return nil, err
		}

		keyPath, err = toAbsolutePath(keyPath)
		if err != nil {
			return nil, err
		}

		domain, err = p.Input("Domain (leave empty for self-signed TLS)", "")
		if err != nil {
			return nil, err
		}

		state.VPS = &infra.VPSConfig{
			Host:    host,
			Port:    port,
			User:    user,
			KeyPath: keyPath,
			Domain:  domain,
		}
	}

	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}

	provisioner, err := vps.New(
		vps.WithHost(host),
		vps.WithPort(port),
		vps.WithUser(user),
		vps.WithPrivateKey(key),
	)
	if err != nil {
		return nil, err
	}

	setup := func(ctx context.Context) error {
		script := vps.GenerateSetupScript(vps.SetupConfig{
			SSHPort: port,
			Domain:  domain,
			Host:    host,
		})

		fmt.Println("Running setup script...")

		output, err := provisioner.ExecuteCommand(ctx, vps.WrapScript(script))
		if err != nil {
			return fmt.Errorf("setup failed: %s\n%w", output, err)
		}

		fmt.Println(output)

		return nil
	}

	return &providerResult{
		provisioner: provisioner,
		setup:       setup,
		getAppURL:   func() string { return cmdcommon.GetAppURL(state.VPS) },
	}, nil
}

func initDO(ctx context.Context, p *gocli.Prompter, state *ProvisionState) (*providerResult, error) {
	if state.DO != nil {
		fmt.Println("Using saved DigitalOcean configuration:")
		fmt.Printf("  Region: %s\n", state.DO.Region)

		if state.DO.AppID != "" {
			fmt.Printf("  App ID: %s\n", state.DO.AppID)
		}

		if state.DO.DBID != "" {
			fmt.Printf("  DB ID:  %s\n", state.DO.DBID)
		}
	} else {
		token, err := p.Input("DigitalOcean API token", "")
		if err != nil {
			return nil, err
		}

		if token == "" {
			return nil, fmt.Errorf("API token is required")
		}

		state.DO = &infra.DOConfig{Token: token}

		// Validate the token immediately
		fmt.Println("Validating API token...")

		tempProv := digitalocean.NewAppPlatform(state.DO)

		if err := tempProv.CheckConnectivity(ctx); err != nil {
			state.DO = nil
			return nil, fmt.Errorf("invalid API token: %w", err)
		}

		fmt.Println("Token is valid!")

		// List App Platform regions and let user choose
		regions, err := tempProv.ListAppRegions(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list regions: %w", err)
		}

		var regionOptions []gocli.Option[string]

		for _, r := range regions {
			label := fmt.Sprintf("%s (%s)", r.Label, r.Slug)
			if r.Default {
				label += " [default]"
			}

			regionOptions = append(regionOptions, gocli.Option[string]{Label: label, Value: r.Slug})
		}

		region, err := gocli.Select(p, "Select App Platform region:", regionOptions)
		if err != nil {
			return nil, err
		}

		state.DO.Region = region
	}

	prov := digitalocean.NewAppPlatform(state.DO)

	return &providerResult{
		provisioner: prov,
		setup:       func(_ context.Context) error { return nil },
		getAppURL:   prov.GetAppURL,
	}, nil
}

func initHetzner(ctx context.Context, p *gocli.Prompter, state *ProvisionState) (*providerResult, error) {
	if state.Hetzner != nil && state.VPS != nil {
		fmt.Println("Using saved Hetzner Cloud configuration:")
		fmt.Printf("  Server IP: %s\n", state.VPS.Host)

		if state.Hetzner.ServerID != 0 {
			fmt.Printf("  Server ID: %d\n", state.Hetzner.ServerID)
		}
	} else {
		token, err := p.Input("Hetzner Cloud API token", "")
		if err != nil {
			return nil, err
		}

		if token == "" {
			return nil, fmt.Errorf("API token is required")
		}

		state.Hetzner = &infra.HetznerConfig{Token: token}

		hProv := hetzner.New(token)

		// Validate token
		fmt.Println("Validating API token...")

		regions, err := hProv.ListLocations(ctx)
		if err != nil {
			state.Hetzner = nil
			return nil, fmt.Errorf("invalid API token: %w", err)
		}

		fmt.Println("Token is valid!")

		// Select location
		var locationOptions []gocli.Option[string]
		for _, r := range regions {
			locationOptions = append(locationOptions, gocli.Option[string]{
				Label: fmt.Sprintf("%s (%s)", r.Name, r.Slug),
				Value: r.Slug,
			})
		}

		location, err := gocli.Select(p, "Select server location:", locationOptions)
		if err != nil {
			return nil, err
		}

		// Select server type
		sizes, err := hProv.ListServerTypes(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list server types: %w", err)
		}

		slices.SortFunc(sizes, func(a, b infra.Size) int {
			if a.PriceMonthly < b.PriceMonthly {
				return -1
			}

			if a.PriceMonthly > b.PriceMonthly {
				return 1
			}

			return 0
		})

		var sizeOptions []gocli.Option[string]

		for _, s := range sizes {
			if len(s.Regions) > 0 && !slices.Contains(s.Regions, location) {
				continue
			}

			label := fmt.Sprintf("%s — %d vCPUs, %dMB RAM, %dGB — $%.0f/mo",
				s.Slug, s.VCPUs, s.Memory, s.Disk, s.PriceMonthly)
			sizeOptions = append(sizeOptions, gocli.Option[string]{Label: label, Value: s.Slug})
		}

		serverType, err := gocli.Select(p, "Select server type:", sizeOptions)
		if err != nil {
			return nil, err
		}

		// Generate SSH key if not already in state
		if state.SSHKey == nil {
			fmt.Println("Generating SSH key pair...")

			pub, priv, err := cryptoext.GenerateSSHKey()
			if err != nil {
				return nil, fmt.Errorf("failed to generate SSH key: %w", err)
			}

			state.SSHKey = &infra.SSHKey{Pub: pub, Key: priv}
		}

		// Create server
		fmt.Println("Creating Hetzner Cloud server...")

		serverID, ip, err := hProv.CreateServer(ctx, "gmapssaas", serverType, location, state.SSHKey.Pub, "")
		if err != nil {
			return nil, fmt.Errorf("failed to create server: %w", err)
		}

		state.Hetzner.ServerID = serverID

		fmt.Printf("Server created (ID: %d, IP: %s)\n", serverID, ip)

		// Wait for the server to get an IP if not available yet
		if ip == "" {
			fmt.Println("Waiting for server IP...")

			for range 20 {
				time.Sleep(5 * time.Second)

				ip, err = hProv.WaitForServer(ctx, serverID)
				if err != nil {
					return nil, err
				}

				if ip != "" {
					break
				}
			}

			if ip == "" {
				return nil, fmt.Errorf("server did not get an IP address")
			}
		}

		state.VPS = &infra.VPSConfig{
			Host: ip,
			Port: "22",
			User: "root",
		}
	}

	// Create VPS provisioner from the Hetzner server
	provisioner, err := vps.New(
		vps.WithHost(state.VPS.Host),
		vps.WithPort(state.VPS.Port),
		vps.WithUser(state.VPS.User),
		vps.WithPrivateKey([]byte(state.SSHKey.Key)),
	)
	if err != nil {
		return nil, err
	}

	setup := func(ctx context.Context) error {
		script := vps.GenerateSetupScript(vps.SetupConfig{
			SSHPort: state.VPS.Port,
			Domain:  state.VPS.Domain,
			Host:    state.VPS.Host,
		})

		fmt.Println("Running setup script on Hetzner server...")

		output, err := provisioner.ExecuteCommand(ctx, vps.WrapScript(script))
		if err != nil {
			return fmt.Errorf("setup failed: %s\n%w", output, err)
		}

		fmt.Println(output)

		return nil
	}

	return &providerResult{
		provisioner: provisioner,
		setup:       setup,
		getAppURL:   func() string { return cmdcommon.GetAppURL(state.VPS) },
	}, nil
}
