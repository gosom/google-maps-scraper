package cmdupdate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/urfave/cli/v3"

	"github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdcommon"
	"github.com/gosom/google-maps-scraper/cmd/gmapssaas/cmdprovision"
	"github.com/gosom/google-maps-scraper/infra"
	"github.com/gosom/google-maps-scraper/infra/digitalocean"
	"github.com/gosom/google-maps-scraper/migrations"
)

type Deployer interface {
	Deploy(ctx context.Context, state *cmdprovision.ProvisionState) (appURL string, err error)
}

var deployers = map[string]Deployer{
	"VPS (bring your own server)": &vpsDeployer{},
	"DigitalOcean (App Platform)": &doDeployer{},
	"Hetzner Cloud":               &vpsDeployer{}, // Hetzner creates a VPS, same deploy flow
}

var Command = &cli.Command{
	Name:  "update",
	Usage: "Build, push and deploy updates to the server",
	Action: func(ctx context.Context, _ *cli.Command) error {
		return runUpdate(ctx)
	},
}

func runUpdate(ctx context.Context) error {
	if err := cmdprovision.EnsureStateDir(); err != nil {
		return err
	}

	state, err := cmdprovision.LoadState()
	if err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}

	if state == nil {
		return fmt.Errorf("no provision state found. Run 'gmapssaas provision' first")
	}

	if state.Registry == nil {
		return fmt.Errorf("no registry configuration found in state")
	}

	deployer, ok := deployers[state.Provider]
	if !ok {
		return fmt.Errorf("unsupported provider: %s", state.Provider)
	}

	if shouldBuildAndPush(state.Registry) {
		fmt.Println("Building and pushing Docker image...")

		if err := cmdcommon.BuildAndPushImage(state.Registry); err != nil {
			return fmt.Errorf("failed to build/push image: %w", err)
		}
	} else {
		fmt.Println("Skipping image build/push (registry credentials not configured). Using existing image from registry.")
	}

	// Run migrations
	fmt.Println("Running database migrations...")

	n, err := migrations.RunWithDSN(state.DatabaseURL)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	if n == 0 {
		fmt.Println("No new migrations")
	} else {
		fmt.Printf("Applied %d migration(s)\n", n)
	}

	// Deploy
	fmt.Println("Deploying application...")

	appURL, err := deployer.Deploy(ctx, state)
	if err != nil {
		return fmt.Errorf("deployment failed: %w", err)
	}

	// Update workers
	if err := updateWorkers(ctx, state); err != nil {
		fmt.Printf("Warning: failed to update some workers: %v\n", err)
	}

	// Print summary
	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println("                     UPDATE COMPLETE")
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println()
	fmt.Printf("  Application URL:  %s\n", appURL)
	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════")

	return nil
}

func shouldBuildAndPush(reg *infra.RegistryConfig) bool {
	if reg == nil {
		return false
	}

	return reg.Username != "" && reg.Token != ""
}

// updateWorkers loops through active workers and pulls the new image + restarts.
func updateWorkers(ctx context.Context, state *cmdprovision.ProvisionState) error {
	if state.SSHKey == nil || state.DatabaseURL == "" {
		return nil
	}

	pool, err := pgxpool.New(ctx, state.DatabaseURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx,
		`SELECT id, name, ip_address FROM provisioned_resources
		 WHERE deleted_at IS NULL AND status IN ('active', 'running') AND ip_address != ''`)
	if err != nil {
		return fmt.Errorf("failed to query workers: %w", err)
	}
	defer rows.Close()

	type worker struct {
		id   int
		name string
		ip   string
	}

	var workers []worker

	for rows.Next() {
		var w worker
		if err := rows.Scan(&w.id, &w.name, &w.ip); err != nil {
			continue
		}

		workers = append(workers, w)
	}

	if len(workers) == 0 {
		return nil
	}

	fmt.Printf("Updating %d worker(s)...\n", len(workers))

	var lastErr error

	for _, w := range workers {
		fmt.Printf("  Updating worker %s (%s)...\n", w.name, w.ip)

		prov, err := cmdcommon.NewVPSProvisionerWithKey(
			&infra.VPSConfig{Host: w.ip, Port: "2222", User: "root"},
			[]byte(state.SSHKey.Key),
		)
		if err != nil {
			fmt.Printf("    Error: %v\n", err)
			lastErr = err

			continue
		}

		cmd := buildWorkerUpdateCommand()

		output, err := prov.ExecuteCommand(ctx, cmd)
		if err != nil {
			fmt.Printf("    Error: %v\n", err)
			lastErr = err

			continue
		}

		_ = output

		fmt.Printf("    Updated successfully\n")
	}

	return lastErr
}

func buildWorkerUpdateCommand() string {
	return "cd /opt/gms-worker && " +
		"worker_instances=$(sed -n 's/^WORKER_INSTANCES=//p' .env | tail -n1) && " +
		"case \"$worker_instances\" in ''|*[!0-9]*) worker_instances=1 ;; esac && " +
		"if [ -f docker-compose.yml ]; then perl -0777 -i -pe 's/\\n  worker:\\n    <<: \\*worker-common\\n    ports:\\n      - \"8080:8080\"\\n/\\n  worker:\\n    <<: *worker-common\\n/s' docker-compose.yml; fi && " +
		"worker_replicas=0 && " +
		"if [ \"$worker_instances\" -gt 1 ]; then worker_replicas=$((worker_instances-1)); fi && " +
		"docker compose pull && " +
		"docker compose up -d --remove-orphans --scale worker_replica=$worker_replicas"
}

type vpsDeployer struct{}

func (d *vpsDeployer) Deploy(ctx context.Context, state *cmdprovision.ProvisionState) (string, error) {
	if state.VPS == nil {
		return "", fmt.Errorf("no VPS configuration found in state")
	}

	var provisioner infra.Provisioner

	var err error

	switch {
	case state.VPS.KeyPath != "":
		provisioner, err = cmdcommon.NewVPSProvisioner(state.VPS)
	case state.SSHKey != nil:
		provisioner, err = cmdcommon.NewVPSProvisionerWithKey(state.VPS, []byte(state.SSHKey.Key))
	default:
		return "", fmt.Errorf("no SSH key found for VPS")
	}

	if err != nil {
		return "", fmt.Errorf("failed to create provisioner: %w", err)
	}

	deployConfig := &infra.DeployConfig{
		Registry:      state.Registry,
		DatabaseURL:   state.DatabaseURL,
		EncryptionKey: state.EncryptionKey,
		HashSalt:      state.HashSalt,
	}

	if err := provisioner.Deploy(ctx, deployConfig); err != nil {
		return "", err
	}

	return cmdcommon.GetAppURL(state.VPS), nil
}

type doDeployer struct{}

func (d *doDeployer) Deploy(ctx context.Context, state *cmdprovision.ProvisionState) (string, error) {
	if state.DO == nil {
		return "", fmt.Errorf("no DigitalOcean configuration found in state")
	}

	prov := digitalocean.NewAppPlatform(state.DO)

	deployConfig := &infra.DeployConfig{
		Registry:      state.Registry,
		DatabaseURL:   state.DatabaseURL,
		EncryptionKey: state.EncryptionKey,
		HashSalt:      state.HashSalt,
	}

	if err := prov.Deploy(ctx, deployConfig); err != nil {
		return "", err
	}

	return prov.GetAppURL(), nil
}
