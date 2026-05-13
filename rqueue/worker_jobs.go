package rqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"golang.org/x/crypto/ssh"

	"github.com/gosom/google-maps-scraper/cryptoext"
	"github.com/gosom/google-maps-scraper/infra"
	"github.com/gosom/google-maps-scraper/infra/cloudinit"
	"github.com/gosom/google-maps-scraper/log"
)

// configKeys for app_config lookups (must match admin/constants.go).
//
//nolint:gosec // These are configuration key names, not credentials.
const (
	configDOToken      = "do_api_token"
	configHetznerToken = "hetzner_api_token"
	configDatabaseURL  = "database_url"
	configHashSalt     = "hash_salt"
	configRegistry     = "registry_provider_settings"
	configSSHKeyPair   = "app_key_pair"
)

// providerTokenKey maps a provider name to its app_config key.
func providerTokenKey(provider string) string {
	switch provider {
	case "digitalocean":
		return configDOToken
	case "hetzner":
		return configHetznerToken
	default:
		return ""
	}
}

// getDecryptedConfig reads an encrypted value from app_config.
func getDecryptedConfig(ctx context.Context, pool *pgxpool.Pool, encKey []byte, key string) (string, error) {
	var value string

	var encrypted bool

	err := pool.QueryRow(ctx,
		`SELECT value, encrypted FROM app_config WHERE key = $1`, key,
	).Scan(&value, &encrypted)
	if err != nil {
		return "", fmt.Errorf("config %q not found: %w", key, err)
	}

	if encrypted {
		decrypted, err := cryptoext.Decrypt(value, encKey)
		if err != nil {
			return "", fmt.Errorf("failed to decrypt config %q: %w", key, err)
		}

		return decrypted, nil
	}

	return value, nil
}

// WorkerProvisionArgs contains only non-sensitive arguments for provisioning.
// Secrets (API tokens, DB URL, registry creds) are fetched at runtime from app_config.
type WorkerProvisionArgs struct {
	ResourceID      int    `json:"resource_id"`
	Provider        string `json:"provider"`
	Name            string `json:"name"`
	Region          string `json:"region"`
	Size            string `json:"size"`
	Concurrency     int    `json:"concurrency"` // Number of worker containers on the host.
	MaxJobsPerCycle int    `json:"max_jobs_per_cycle"`
	FastMode        bool   `json:"fast_mode"`
	Proxies         string `json:"proxies"`
}

func (WorkerProvisionArgs) Kind() string {
	return "worker_provision"
}

func (WorkerProvisionArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       QueueMaintenance,
		MaxAttempts: 1,
	}
}

// WorkerProvisionWorker handles worker provisioning in the background.
type WorkerProvisionWorker struct {
	river.WorkerDefaults[WorkerProvisionArgs]
	dbPool        *pgxpool.Pool
	encryptionKey []byte
}

func (w *WorkerProvisionWorker) Timeout(_ *river.Job[WorkerProvisionArgs]) time.Duration {
	return 10 * time.Minute
}

func (w *WorkerProvisionWorker) Work(ctx context.Context, job *river.Job[WorkerProvisionArgs]) error {
	args := job.Args

	log.Info("provisioning worker",
		"resource_id", args.ResourceID,
		"provider", args.Provider,
		"name", args.Name,
		"region", args.Region,
		"size", args.Size,
	)

	// Fetch provider token from app_config
	tokenKey := providerTokenKey(args.Provider)
	if tokenKey == "" {
		updateResourceFailed(ctx, w.dbPool, args.ResourceID, "unsupported provider: "+args.Provider)
		return fmt.Errorf("unsupported provider: %s", args.Provider)
	}

	token, err := getDecryptedConfig(ctx, w.dbPool, w.encryptionKey, tokenKey)
	if err != nil {
		updateResourceFailed(ctx, w.dbPool, args.ResourceID, "failed to get provider token")
		return fmt.Errorf("failed to get provider token: %w", err)
	}

	// Fetch SSH public key
	sshKeyJSON, err := getDecryptedConfig(ctx, w.dbPool, w.encryptionKey, configSSHKeyPair)
	if err != nil {
		updateResourceFailed(ctx, w.dbPool, args.ResourceID, "failed to get SSH key")
		return fmt.Errorf("failed to get SSH key: %w", err)
	}

	var sshKey infra.SSHKey
	if err := json.Unmarshal([]byte(sshKeyJSON), &sshKey); err != nil {
		updateResourceFailed(ctx, w.dbPool, args.ResourceID, "failed to parse SSH key")
		return fmt.Errorf("failed to parse SSH key: %w", err)
	}

	// Build cloud-init from app_config secrets
	cloudInitScript, err := w.buildCloudInit(ctx, args)
	if err != nil {
		updateResourceFailed(ctx, w.dbPool, args.ResourceID, "failed to build cloud-init")
		return fmt.Errorf("failed to build cloud-init: %w", err)
	}

	// Create the worker
	prov, err := infra.NewWorkerProvisioner(args.Provider, token)
	if err != nil {
		updateResourceFailed(ctx, w.dbPool, args.ResourceID, err.Error())
		return err
	}

	result, err := prov.CreateWorker(ctx, &infra.WorkerCreateRequest{
		Name:      args.Name,
		Region:    args.Region,
		Size:      args.Size,
		SSHPubKey: sshKey.Pub,
		UserData:  cloudInitScript,
	})
	if err != nil {
		log.Error("failed to provision worker", "error", err, "resource_id", args.ResourceID)
		updateResourceFailed(ctx, w.dbPool, args.ResourceID, err.Error())

		return fmt.Errorf("failed to create worker: %w", err)
	}

	log.Info("worker created, waiting for IP",
		"resource_id", args.ResourceID,
		"provider_resource_id", result.ResourceID,
		"status", result.Status,
	)

	// Poll until the VM has an IP address assigned
	if result.IPAddress == "" {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				updateResourceFailed(ctx, w.dbPool, args.ResourceID, "provisioning timed out waiting for IP")
				return ctx.Err()
			case <-ticker.C:
				ws, err := prov.GetWorkerStatus(ctx, result.ResourceID)
				if err != nil {
					log.Debug("polling worker status", "error", err, "resource_id", result.ResourceID)
					continue
				}

				if ws.IPAddress != "" {
					result.IPAddress = ws.IPAddress
					result.Status = ws.Status
					log.Info("worker IP assigned",
						"resource_id", args.ResourceID,
						"ip_address", result.IPAddress,
					)

					goto ready
				}
			}
		}
	}
ready:

	// Persist IP address and provider resource ID immediately so the health check
	// can heal this worker if the SSH wait below times out.
	_, _ = w.dbPool.Exec(ctx,
		`UPDATE provisioned_resources
		 SET resource_id = $1, ip_address = $2, updated_at = NOW()
		 WHERE id = $3 AND deleted_at IS NULL`,
		result.ResourceID, result.IPAddress, args.ResourceID,
	)

	// Wait for SSH (port 2222) to be ready before marking as active
	log.Info("waiting for SSH to be ready", "resource_id", args.ResourceID, "ip_address", result.IPAddress)

	sshTicker := time.NewTicker(10 * time.Second)
	defer sshTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			updateResourceFailed(ctx, w.dbPool, args.ResourceID, "provisioning timed out waiting for SSH")
			return ctx.Err()
		case <-sshTicker.C:
			conn, err := net.DialTimeout("tcp", result.IPAddress+":2222", 5*time.Second)
			if err != nil {
				log.Debug("SSH not ready yet", "resource_id", args.ResourceID, "error", err)
				continue
			}

			_ = conn.Close()

			log.Info("SSH is ready", "resource_id", args.ResourceID, "ip_address", result.IPAddress)

			goto sshReady
		}
	}
sshReady:

	log.Info("worker provisioned",
		"resource_id", args.ResourceID,
		"provider_resource_id", result.ResourceID,
		"ip_address", result.IPAddress,
	)

	// Normalize status to "active" and guard against delete-during-provisioning race
	tag, err := w.dbPool.Exec(ctx,
		`UPDATE provisioned_resources
		 SET resource_id = $1, status = 'active', ip_address = $2, updated_at = NOW()
		 WHERE id = $3 AND status = 'provisioning' AND deleted_at IS NULL`,
		result.ResourceID, result.IPAddress, args.ResourceID,
	)
	if err != nil {
		log.Error("failed to update provisioned resource", "error", err, "resource_id", args.ResourceID)
		return fmt.Errorf("failed to update resource: %w", err)
	}

	if tag.RowsAffected() == 0 {
		// Resource was deleted while we were provisioning — clean up cloud resource
		log.Info("resource deleted during provisioning, cleaning up", "resource_id", args.ResourceID)
		_ = prov.DeleteWorker(ctx, result.ResourceID)
	}

	return nil
}

// buildCloudInit fetches secrets from app_config and generates the cloud-init script.
func (w *WorkerProvisionWorker) buildCloudInit(ctx context.Context, args WorkerProvisionArgs) (string, error) { //nolint:gocritic // hugeParam: WorkerProvisionArgs is 112 bytes but passing by pointer would change River job semantics
	dbURL, err := getDecryptedConfig(ctx, w.dbPool, w.encryptionKey, configDatabaseURL)
	if err != nil {
		return "", fmt.Errorf("database URL: %w", err)
	}

	var hashSalt string
	if v, err := getDecryptedConfig(ctx, w.dbPool, w.encryptionKey, configHashSalt); err == nil {
		hashSalt = v
	}

	registryJSON, err := getDecryptedConfig(ctx, w.dbPool, w.encryptionKey, configRegistry)
	if err != nil {
		return "", fmt.Errorf("registry config: %w", err)
	}

	var registry infra.RegistryConfig
	if err := json.Unmarshal([]byte(registryJSON), &registry); err != nil {
		return "", fmt.Errorf("failed to parse registry config: %w", err)
	}

	ciCfg := cloudinit.Config{
		DatabaseURL:      dbURL,
		HashIDSalt:       hashSalt,
		Concurrency:      args.Concurrency,
		MaxJobsPerCycle:  args.MaxJobsPerCycle,
		FastMode:         args.FastMode,
		Proxies:          args.Proxies,
		RegistryURL:      registry.URL,
		RegistryUsername: registry.Username,
		RegistryToken:    registry.Token,
		Image:            resolveRegistryImage(registry.URL, registry.Image),
	}

	return cloudinit.Generate(ciCfg), nil
}

func resolveRegistryImage(registryURL, image string) string {
	registryURL = strings.TrimSpace(strings.TrimSuffix(registryURL, "/"))
	image = strings.TrimSpace(strings.TrimPrefix(image, "/"))

	if image == "" {
		return image
	}

	// Image already includes a registry host (e.g. ghcr.io/org/app:tag).
	firstSegment := image
	if idx := strings.IndexByte(image, '/'); idx != -1 {
		firstSegment = image[:idx]
	}

	if strings.Contains(firstSegment, ".") || strings.Contains(firstSegment, ":") || firstSegment == "localhost" {
		return image
	}

	if registryURL == "" {
		return image
	}

	return registryURL + "/" + image
}

// WorkerDeleteArgs contains only non-sensitive arguments for deletion.
type WorkerDeleteArgs struct {
	ResourceID         int    `json:"resource_id"`
	Provider           string `json:"provider"`
	ProviderResourceID string `json:"provider_resource_id"`
}

func (WorkerDeleteArgs) Kind() string {
	return "worker_delete"
}

func (WorkerDeleteArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       QueueMaintenance,
		MaxAttempts: 3,
	}
}

// WorkerDeleteWorker handles worker deletion in the background.
type WorkerDeleteWorker struct {
	river.WorkerDefaults[WorkerDeleteArgs]
	dbPool        *pgxpool.Pool
	encryptionKey []byte
}

func (w *WorkerDeleteWorker) Work(ctx context.Context, job *river.Job[WorkerDeleteArgs]) error {
	args := job.Args

	log.Info("deleting worker",
		"resource_id", args.ResourceID,
		"provider", args.Provider,
		"provider_resource_id", args.ProviderResourceID,
		"attempt", job.Attempt,
	)

	// If there's no real provider resource ID, the worker was never fully provisioned.
	if args.ProviderResourceID == "" || strings.HasPrefix(args.ProviderResourceID, "pending-") {
		log.Info("no provider resource ID, skipping cloud deletion", "resource_id", args.ResourceID, "provider_resource_id", args.ProviderResourceID)
	} else {
		// Gracefully stop the worker container before destroying the machine
		w.gracefulStop(ctx, args.ResourceID)

		// Fetch provider token from app_config
		tokenKey := providerTokenKey(args.Provider)
		if tokenKey == "" {
			return fmt.Errorf("unsupported provider: %s", args.Provider)
		}

		token, err := getDecryptedConfig(ctx, w.dbPool, w.encryptionKey, tokenKey)
		if err != nil {
			return fmt.Errorf("failed to get provider token: %w", err)
		}

		prov, err := infra.NewWorkerProvisioner(args.Provider, token)
		if err != nil {
			return err
		}

		if err := prov.DeleteWorker(ctx, args.ProviderResourceID); err != nil {
			return fmt.Errorf("failed to delete worker: %w", err)
		}
	}

	_, err := w.dbPool.Exec(ctx,
		`UPDATE provisioned_resources SET deleted_at = NOW(), status = 'deleted', updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`,
		args.ResourceID,
	)
	if err != nil {
		log.Error("failed to soft-delete provisioned resource", "error", err, "resource_id", args.ResourceID)
		return fmt.Errorf("failed to delete resource: %w", err)
	}

	log.Info("worker deleted", "resource_id", args.ResourceID)

	return nil
}

// gracefulStop SSHs into the worker and runs `docker compose down -t 10` to send
// SIGTERM and wait up to 10 seconds for the container to finish active jobs.
// Errors are logged but not returned — deletion proceeds regardless.
func (w *WorkerDeleteWorker) gracefulStop(ctx context.Context, resourceID int) {
	// Look up worker IP
	var ip string

	err := w.dbPool.QueryRow(ctx,
		`SELECT ip_address FROM provisioned_resources WHERE id = $1`,
		resourceID,
	).Scan(&ip)
	if err != nil || ip == "" {
		log.Info("no IP for graceful stop, skipping", "resource_id", resourceID)
		return
	}

	// Fetch SSH private key
	sshKeyJSON, err := getDecryptedConfig(ctx, w.dbPool, w.encryptionKey, configSSHKeyPair)
	if err != nil {
		log.Warn("failed to get SSH key for graceful stop", "error", err, "resource_id", resourceID)
		return
	}

	var sshKey infra.SSHKey
	if err := json.Unmarshal([]byte(sshKeyJSON), &sshKey); err != nil {
		log.Warn("failed to parse SSH key for graceful stop", "error", err, "resource_id", resourceID)
		return
	}

	signer, err := ssh.ParsePrivateKey([]byte(sshKey.Key))
	if err != nil {
		log.Warn("failed to parse SSH private key for graceful stop", "error", err, "resource_id", resourceID)
		return
	}

	hostPort := net.JoinHostPort(ip, "2222")

	hostKeyCallback, err := infra.NewTOFUHostKeyCallback(hostPort)
	if err != nil {
		log.Warn("failed to initialize SSH host key callback for graceful stop", "error", err, "resource_id", resourceID)
		return
	}

	sshConfig := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         5 * time.Second,
	}

	client, err := ssh.Dial("tcp", hostPort, sshConfig)
	if err != nil {
		log.Warn("SSH dial failed for graceful stop", "error", err, "resource_id", resourceID, "ip", ip)
		return
	}

	defer func() { _ = client.Close() }()

	session, err := client.NewSession()
	if err != nil {
		log.Warn("SSH session failed for graceful stop", "error", err, "resource_id", resourceID)
		return
	}

	defer func() { _ = session.Close() }()

	log.Info("sending graceful stop to worker", "resource_id", resourceID, "ip", ip)

	// docker compose down sends SIGTERM and waits up to -t seconds
	if err := session.Run("cd /opt/gms-worker && docker compose down -t 10"); err != nil {
		log.Warn("graceful stop command failed", "error", err, "resource_id", resourceID)
		return
	}

	log.Info("worker stopped gracefully", "resource_id", resourceID, "ip", ip)
}

func updateResourceFailed(ctx context.Context, pool *pgxpool.Pool, id int, errMsg string) {
	ctx = context.WithoutCancel(ctx)

	_, _ = pool.Exec(ctx,
		`UPDATE provisioned_resources
		 SET status = 'failed',
		     metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{error}', to_jsonb($1::text)),
		     updated_at = NOW()
		 WHERE id = $2`,
		errMsg, id,
	)
}

// WorkerHealthCheckArgs is a periodic job that checks health of all active workers.
type WorkerHealthCheckArgs struct{}

func (WorkerHealthCheckArgs) Kind() string {
	return "worker_health_check"
}

func (WorkerHealthCheckArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       QueueMaintenance,
		MaxAttempts: 1,
	}
}

// WorkerHealthCheckWorker checks health of all active provisioned workers.
type WorkerHealthCheckWorker struct {
	river.WorkerDefaults[WorkerHealthCheckArgs]
	dbPool        *pgxpool.Pool
	encryptionKey []byte
}

func (w *WorkerHealthCheckWorker) Work(ctx context.Context, _ *river.Job[WorkerHealthCheckArgs]) error {
	// Check all workers with an IP that aren't in a terminal state.
	// This also heals workers stuck with raw provider statuses (e.g. "new", "running")
	// by normalizing them to "active" when reachable.
	rows, err := w.dbPool.Query(ctx,
		`SELECT id, status, ip_address FROM provisioned_resources
		 WHERE status NOT IN ('failed', 'deleted', 'deleting') AND ip_address != '' AND deleted_at IS NULL`,
	)
	if err != nil {
		return fmt.Errorf("failed to list active workers: %w", err)
	}
	defer rows.Close()

	type worker struct {
		id     int
		status string
		ip     string
	}

	var workers []worker

	for rows.Next() {
		var wr worker
		if err := rows.Scan(&wr.id, &wr.status, &wr.ip); err != nil {
			return fmt.Errorf("failed to scan worker: %w", err)
		}

		workers = append(workers, wr)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate workers: %w", err)
	}

	sshKeyJSON, err := getDecryptedConfig(ctx, w.dbPool, w.encryptionKey, configSSHKeyPair)
	if err != nil {
		return fmt.Errorf("failed to get SSH key for health checks: %w", err)
	}

	var sshKey infra.SSHKey
	if err := json.Unmarshal([]byte(sshKeyJSON), &sshKey); err != nil {
		return fmt.Errorf("failed to parse SSH key for health checks: %w", err)
	}

	signer, err := ssh.ParsePrivateKey([]byte(sshKey.Key))
	if err != nil {
		return fmt.Errorf("failed to parse SSH private key for health checks: %w", err)
	}

	for _, wr := range workers {
		health := checkWorkerHealth(ctx, wr.ip, signer)

		healthJSON, err := json.Marshal(health)
		if err != nil {
			log.Error("failed to marshal health", "error", err, "worker_id", wr.id)
			continue
		}

		// Normalize non-active workers to "active" when reachable
		if wr.status != "active" && health.Reachable {
			log.Info("normalizing worker status to active", "worker_id", wr.id, "old_status", wr.status)
			_, err = w.dbPool.Exec(ctx,
				`UPDATE provisioned_resources
				 SET status = 'active',
				     metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{health}', $1),
				     updated_at = NOW()
				 WHERE id = $2`,
				healthJSON, wr.id,
			)
		} else {
			_, err = w.dbPool.Exec(ctx,
				`UPDATE provisioned_resources
				 SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{health}', $1),
				     updated_at = NOW()
				 WHERE id = $2`,
				healthJSON, wr.id,
			)
		}

		if err != nil {
			log.Error("failed to update worker health", "error", err, "worker_id", wr.id)
		}
	}

	log.Info("worker health check completed", "workers_checked", len(workers))

	return nil
}

// healthResult is the health data stored in provisioned_resources metadata.
type healthResult struct {
	Reachable        bool    `json:"reachable"`
	Status           string  `json:"status"`
	Uptime           string  `json:"uptime"`
	JobsProcessed    int64   `json:"jobs_processed"`
	ResultsCollected int64   `json:"results_collected"`
	ResultsPerMinute float64 `json:"results_per_minute"`
	Concurrency      int     `json:"concurrency"`
	CheckedAt        string  `json:"checked_at"`
}

// CleanupArgs is a periodic job that cleans up expired sessions, stale rate limits,
// and old scrape results.
type CleanupArgs struct{}

func (CleanupArgs) Kind() string {
	return "cleanup"
}

func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       QueueMaintenance,
		MaxAttempts: 1,
	}
}

// CleanupWorker performs periodic database cleanup.
type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	dbPool *pgxpool.Pool
}

func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	// 1. Clean up expired sessions
	result, err := w.dbPool.Exec(ctx, `DELETE FROM admin_sessions WHERE expires_at < NOW()`)
	if err != nil {
		log.Error("cleanup: failed to delete expired sessions", "error", err)
	} else if result.RowsAffected() > 0 {
		log.Info("cleanup: deleted expired sessions", "count", result.RowsAffected())
	}

	// 2. Clean up stale rate limits (older than 1 hour)
	result, err = w.dbPool.Exec(ctx, `DELETE FROM rate_limits WHERE window_start < NOW() - INTERVAL '1 hour'`)
	if err != nil {
		log.Error("cleanup: failed to delete stale rate limits", "error", err)
	} else if result.RowsAffected() > 0 {
		log.Info("cleanup: deleted stale rate limits", "count", result.RowsAffected())
	}

	// 3. Clean up old scrape results (completed jobs older than 30 days)
	result, err = w.dbPool.Exec(ctx,
		`DELETE FROM scrape_results WHERE job_id IN (
			SELECT id FROM river_job WHERE state = 'completed' AND finalized_at < NOW() - INTERVAL '30 days'
		)`)
	if err != nil {
		log.Error("cleanup: failed to delete old scrape results", "error", err)
	} else if result.RowsAffected() > 0 {
		log.Info("cleanup: deleted old scrape results", "count", result.RowsAffected())
	}

	return nil
}

func checkWorkerHealth(ctx context.Context, ip string, signer ssh.Signer) *healthResult {
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	hostPort := net.JoinHostPort(ip, "2222")

	hostKeyCallback, err := infra.NewTOFUHostKeyCallback(hostPort)
	if err != nil {
		return &healthResult{Reachable: false, CheckedAt: checkedAt}
	}

	sshConfig := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         5 * time.Second,
	}

	client, err := ssh.Dial("tcp", hostPort, sshConfig)
	if err != nil {
		return &healthResult{Reachable: false, CheckedAt: checkedAt}
	}

	defer func() { _ = client.Close() }()

	expectedRaw, err := runSSHCommand(ctx, client,
		`cd /opt/gms-worker && sed -n 's/^WORKER_INSTANCES=//p' .env | tail -n1`)
	if err != nil {
		return &healthResult{Reachable: false, CheckedAt: checkedAt}
	}

	expected := 1
	if strings.TrimSpace(expectedRaw) != "" {
		if n, parseErr := fmt.Sscanf(strings.TrimSpace(expectedRaw), "%d", &expected); parseErr != nil || n != 1 || expected <= 0 {
			expected = 1
		}
	}

	cidsRaw, err := runSSHCommand(ctx, client, `cd /opt/gms-worker && docker compose ps -q worker worker_replica`)
	if err != nil {
		return &healthResult{
			Reachable:   false,
			Status:      "compose_error",
			Concurrency: expected,
			CheckedAt:   checkedAt,
		}
	}

	cids := strings.Fields(cidsRaw)
	if len(cids) == 0 {
		return &healthResult{
			Reachable:   false,
			Status:      "down",
			Concurrency: expected,
			CheckedAt:   checkedAt,
		}
	}

	type containerHealth struct {
		Status           string  `json:"status"`
		Uptime           string  `json:"uptime"`
		JobsProcessed    int64   `json:"jobs_processed"`
		ResultsCollected int64   `json:"results_collected"`
		ResultsPerMinute float64 `json:"results_per_minute"`
	}

	reachable := 0

	jobsProcessed := int64(0)
	resultsCollected := int64(0)
	resultsPerMinute := float64(0)

	for _, cid := range cids {
		cmd := fmt.Sprintf(`ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' %s 2>/dev/null); `+
			`if [ -n "$ip" ]; then `+
			`if command -v curl >/dev/null 2>&1; then curl -fsS --max-time 2 "http://$ip:8080/health"; `+
			`elif command -v wget >/dev/null 2>&1; then wget -qO- --timeout=2 "http://$ip:8080/health"; fi; `+
			`fi`, cid)
		body, err := runSSHCommand(ctx, client, cmd)

		if err != nil || strings.TrimSpace(body) == "" {
			continue
		}

		var ch containerHealth
		if err := json.Unmarshal([]byte(body), &ch); err != nil {
			continue
		}

		reachable++
		jobsProcessed += ch.JobsProcessed
		resultsCollected += ch.ResultsCollected
		resultsPerMinute += ch.ResultsPerMinute
	}

	status := "degraded"
	if reachable == 0 {
		status = "down"
	} else if reachable == expected {
		status = "ok"
	}

	return &healthResult{
		Reachable:        reachable > 0,
		Status:           status,
		Uptime:           fmt.Sprintf("%d/%d replicas healthy", reachable, expected),
		JobsProcessed:    jobsProcessed,
		ResultsCollected: resultsCollected,
		ResultsPerMinute: resultsPerMinute,
		Concurrency:      expected,
		CheckedAt:        checkedAt,
	}
}

func runSSHCommand(ctx context.Context, client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}

	defer func() { _ = session.Close() }()

	done := make(chan error, 1)
	out := []byte(nil)

	go func() {
		var runErr error
		out, runErr = session.Output(cmd)
		done <- runErr
	}()

	select {
	case <-ctx.Done():
		_ = session.Close()
		return "", ctx.Err()
	case err := <-done:
		return strings.TrimSpace(string(out)), err
	}
}
