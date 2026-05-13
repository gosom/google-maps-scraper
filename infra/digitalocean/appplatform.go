package digitalocean

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/digitalocean/godo"

	"github.com/gosom/google-maps-scraper/infra"
)

var _ infra.Provisioner = (*AppPlatformProvisioner)(nil)

// AppPlatformProvisioner implements infra.Provisioner for DigitalOcean App Platform.
type AppPlatformProvisioner struct {
	client *godo.Client
	cfg    *infra.DOConfig
	appURL string
}

// NewAppPlatform creates a new DigitalOcean App Platform provisioner.
func NewAppPlatform(cfg *infra.DOConfig) *AppPlatformProvisioner {
	return &AppPlatformProvisioner{
		client: godo.NewFromToken(cfg.Token),
		cfg:    cfg,
	}
}

// CheckConnectivity validates the DO API token by calling the account endpoint.
func (p *AppPlatformProvisioner) CheckConnectivity(ctx context.Context) error {
	_, _, err := p.client.Account.Get(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", infra.ErrConnectionFailed, err)
	}

	return nil
}

// ExecuteCommand is not supported on App Platform.
func (p *AppPlatformProvisioner) ExecuteCommand(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("execute command is not supported on App Platform")
}

// CreateDatabase provisions a DigitalOcean Managed PostgreSQL database.
// The cfg.DBSize and cfg.DBRegion must be set before calling this method.
func (p *AppPlatformProvisioner) CreateDatabase(ctx context.Context) (*infra.DatabaseInfo, error) {
	// Determine the latest PG version
	pgVersion, err := p.latestPGVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get PG versions: %w", err)
	}

	fmt.Printf("Creating managed PostgreSQL %s database (%s in %s)...\n",
		pgVersion, p.cfg.DBSize, p.cfg.DBRegion)

	db, _, err := p.client.Databases.Create(ctx, &godo.DatabaseCreateRequest{
		Name:       "gmapssaas-db",
		EngineSlug: "pg",
		Version:    pgVersion,
		SizeSlug:   p.cfg.DBSize,
		Region:     p.cfg.DBRegion,
		NumNodes:   1,
		Tags:       []string{"gmapssaas"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create database: %w", err)
	}

	p.cfg.DBID = db.ID
	fmt.Printf("Database created (ID: %s), waiting for it to come online...\n", db.ID)

	// Poll until the database is online
	err = pollUntil(ctx, 15*time.Second, 10*time.Minute, func() (bool, error) {
		db, _, err := p.client.Databases.Get(ctx, p.cfg.DBID)
		if err != nil {
			return false, fmt.Errorf("failed to get database status: %w", err)
		}

		fmt.Printf("  Database status: %s\n", db.Status)

		if db.Status == "online" {
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("database did not come online: %w", err)
	}

	// Fetch the database again to get the connection details
	db, _, err = p.client.Databases.Get(ctx, p.cfg.DBID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection info: %w", err)
	}

	if db.Connection == nil || db.Connection.URI == "" {
		return nil, fmt.Errorf("database connection URI is empty")
	}

	fmt.Printf("Database is online! Connection established.\n")

	return &infra.DatabaseInfo{
		ConnectionURL: db.Connection.URI,
	}, nil
}

// Deploy creates or updates a DigitalOcean App Platform application.
func (p *AppPlatformProvisioner) Deploy(ctx context.Context, cfg *infra.DeployConfig) error {
	repo, tag := parseImageRef(cfg.Registry.Image)

	spec := &godo.AppSpec{
		Name:   "gmapssaas",
		Region: p.cfg.Region,
		Services: []*godo.AppServiceSpec{{
			Name:             "server",
			InstanceSizeSlug: "basic-s",
			InstanceCount:    1,
			HTTPPort:         8080,
			RunCommand:       "/app/gmapssaas serve",
			Image:            buildImageSpec(cfg.Registry, repo, tag),
			HealthCheck: &godo.AppServiceSpecHealthCheck{
				HTTPPath:            "/health",
				InitialDelaySeconds: 30,
				PeriodSeconds:       10,
			},
			Envs: []*godo.AppVariableDefinition{
				{Key: "DATABASE_URL", Value: cfg.DatabaseURL, Type: godo.AppVariableType_Secret},
				{Key: "ENCRYPTION_KEY", Value: cfg.EncryptionKey, Type: godo.AppVariableType_Secret},
				{Key: "HASHID_SALT", Value: cfg.HashSalt, Scope: godo.AppVariableScope_RunTime},
			},
		}},
	}

	// If we already have an app ID, update it; otherwise try to find or create it.
	appID := p.cfg.AppID
	if appID == "" {
		appID = p.findExistingApp(ctx)
	}

	var deploymentID string

	if appID != "" {
		fmt.Printf("Updating existing App Platform application (%s)...\n", appID)

		_, _, err := p.client.Apps.Update(ctx, appID, &godo.AppUpdateRequest{Spec: spec})
		if err != nil {
			return fmt.Errorf("failed to update app: %w", err)
		}

		p.cfg.AppID = appID

		// Trigger a new deployment and capture its ID
		dep, _, err := p.client.Apps.CreateDeployment(ctx, appID)
		if err != nil {
			return fmt.Errorf("failed to create deployment: %w", err)
		}

		deploymentID = dep.ID
		fmt.Printf("Deployment created (%s), waiting...\n", deploymentID)
	} else {
		fmt.Println("Creating App Platform application...")

		app, _, err := p.client.Apps.Create(ctx, &godo.AppCreateRequest{Spec: spec})
		if err != nil {
			return fmt.Errorf("failed to create app: %w", err)
		}

		p.cfg.AppID = app.ID
		fmt.Printf("App created (ID: %s), waiting for deployment...\n", app.ID)
	}

	// Poll until our deployment is active
	err := pollUntil(ctx, 15*time.Second, 10*time.Minute, func() (bool, error) {
		app, _, err := p.client.Apps.Get(ctx, p.cfg.AppID)
		if err != nil {
			return false, fmt.Errorf("failed to get app status: %w", err)
		}

		// If we're tracking a specific deployment (update), wait for that one
		if deploymentID != "" {
			if app.InProgressDeployment != nil && app.InProgressDeployment.ID == deploymentID {
				fmt.Printf("  Deployment phase: %s\n", app.InProgressDeployment.Phase)
				return false, nil
			}

			if app.ActiveDeployment != nil && app.ActiveDeployment.ID == deploymentID {
				p.appURL = app.LiveURL
				return true, nil
			}

			// Check if our deployment failed (no longer in-progress and not active)
			dep, _, err := p.client.Apps.GetDeployment(ctx, p.cfg.AppID, deploymentID)
			if err == nil {
				switch dep.Phase { //nolint:exhaustive // only handle failure states; other phases mean still in progress
				case godo.DeploymentPhase_Error:
					return false, fmt.Errorf("deployment failed")
				case godo.DeploymentPhase_Canceled:
					return false, fmt.Errorf("deployment was canceled")
				}

				fmt.Printf("  Deployment phase: %s\n", dep.Phase)
			}

			return false, nil
		}

		// For new apps, wait for any active deployment
		if app.InProgressDeployment != nil {
			fmt.Printf("  Deployment phase: %s\n", app.InProgressDeployment.Phase)
			return false, nil
		}

		if app.ActiveDeployment != nil {
			switch app.ActiveDeployment.Phase { //nolint:exhaustive // only handle terminal states
			case godo.DeploymentPhase_Active:
				p.appURL = app.LiveURL
				return true, nil
			case godo.DeploymentPhase_Error:
				return false, fmt.Errorf("deployment failed")
			case godo.DeploymentPhase_Canceled:
				return false, fmt.Errorf("deployment was canceled")
			}
		}

		return false, nil
	})
	if err != nil {
		return fmt.Errorf("deployment did not complete: %w", err)
	}

	fmt.Printf("Application deployed! URL: %s\n", p.appURL)

	return nil
}

// findExistingApp searches for an existing app named "gmapssaas".
func (p *AppPlatformProvisioner) findExistingApp(ctx context.Context) string {
	apps, _, err := p.client.Apps.List(ctx, &godo.ListOptions{PerPage: 100})
	if err != nil {
		return ""
	}

	for _, app := range apps {
		if app.Spec != nil && app.Spec.Name == "gmapssaas" {
			return app.ID
		}
	}

	return ""
}

// ListDatabaseOptions returns the available PostgreSQL database options (regions, sizes, versions).
func (p *AppPlatformProvisioner) ListDatabaseOptions(ctx context.Context) (*godo.DatabaseEngineOptions, error) {
	opts, _, err := p.client.Databases.ListOptions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list database options: %w", err)
	}

	return &opts.PostgresSQLOptions, nil
}

// ListAppRegions returns the available App Platform regions.
func (p *AppPlatformProvisioner) ListAppRegions(ctx context.Context) ([]*godo.AppRegion, error) {
	regions, _, err := p.client.Apps.ListRegions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list app regions: %w", err)
	}

	// Filter out disabled regions
	var available []*godo.AppRegion

	for _, r := range regions {
		if !r.Disabled {
			available = append(available, r)
		}
	}

	return available, nil
}

// GetAppURL returns the live URL after a successful deployment.
func (p *AppPlatformProvisioner) GetAppURL() string {
	return p.appURL
}

// latestPGVersion returns the latest available PostgreSQL version.
func (p *AppPlatformProvisioner) latestPGVersion(ctx context.Context) (string, error) {
	pgOpts, err := p.ListDatabaseOptions(ctx)
	if err != nil {
		return "", err
	}

	if len(pgOpts.Versions) == 0 {
		return "", fmt.Errorf("no PostgreSQL versions available")
	}

	// Versions are typically sorted ascending; pick the last one
	return pgOpts.Versions[len(pgOpts.Versions)-1], nil
}

// registryType maps a registry URL to the DigitalOcean ImageSourceSpecRegistryType.
func registryType(url string) godo.ImageSourceSpecRegistryType {
	switch {
	case strings.Contains(url, "ghcr.io"):
		return godo.ImageSourceSpecRegistryType_Ghcr
	default:
		return godo.ImageSourceSpecRegistryType_DockerHub
	}
}

// parseImageRef splits "user/repo:tag" into repository and tag parts.
func parseImageRef(image string) (repo, tag string) {
	parts := strings.SplitN(image, ":", 2)
	repo = parts[0]
	tag = "latest"

	if len(parts) == 2 && parts[1] != "" {
		tag = parts[1]
	}

	return repo, tag
}

func buildImageSpec(reg *infra.RegistryConfig, repo, tag string) *godo.ImageSourceSpec {
	// Strip the registry URL prefix from the repository if present.
	// DO expects Registry="ghcr.io" and Repository="user/repo", not "ghcr.io/user/repo".
	repo = strings.TrimPrefix(repo, reg.URL+"/")

	spec := &godo.ImageSourceSpec{
		RegistryType: registryType(reg.URL),
		Registry:     reg.URL,
		Repository:   repo,
		Tag:          tag,
	}

	if reg.Username != "" && reg.Token != "" {
		spec.RegistryCredentials = reg.Username + ":" + reg.Token
	}

	return spec
}

// pollUntil repeatedly calls check at the given interval until it returns true or the timeout expires.
func pollUntil(ctx context.Context, interval, timeout time.Duration, check func() (bool, error)) error {
	deadline := time.After(timeout)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Check immediately before first tick
	done, err := check()
	if err != nil {
		return err
	}

	if done {
		return nil
	}

	for {
		select {
		case <-deadline:
			return fmt.Errorf("timed out after %s", timeout)
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			done, err := check()
			if err != nil {
				return err
			}

			if done {
				return nil
			}
		}
	}
}
