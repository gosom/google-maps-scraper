// Package cloudinit provides cloud-init script generation for worker provisioning.
package cloudinit

import (
	"strconv"
	"strings"
)

// Config contains the configuration for generating a cloud-init script.
type Config struct {
	// DatabaseURL is the PostgreSQL connection string.
	DatabaseURL string

	// HashIDSalt is the salt for encoding job IDs.
	HashIDSalt string

	// Concurrency is the number of worker containers to run on this host.
	Concurrency int

	// MaxJobsPerCycle is the number of jobs to process before restarting.
	MaxJobsPerCycle int

	// FastMode enables the stealth HTTP scraping mode.
	FastMode bool

	// Proxies is a comma-separated list of proxy URLs.
	Proxies string

	// RegistryURL is the container registry URL (e.g., "ghcr.io", "registry.digitalocean.com/myregistry").
	RegistryURL string

	// RegistryUsername is the username for registry authentication.
	RegistryUsername string

	// RegistryToken is the token/password for registry authentication.
	RegistryToken string

	// Image is the full image reference (e.g., "ghcr.io/gosom/google-maps-scraper-pro:latest").
	Image string
}

// dockerLoginCmd returns the docker login command for the configured registry.
// Returns an empty string if no credentials are provided.
func dockerLoginCmd(cfg Config) string { //nolint:gocritic // hugeParam: Config is 136 bytes but passing by pointer would change API semantics
	if cfg.RegistryToken == "" || cfg.RegistryURL == "" {
		return ""
	}

	return "echo '" + cfg.RegistryToken + "' | docker login " + cfg.RegistryURL + " -u " + cfg.RegistryUsername + " --password-stdin"
}

func boolStr(b bool) string {
	if b {
		return "true"
	}

	return "false"
}

func normalize(cfg Config) Config { //nolint:gocritic // hugeParam: Config is 136 bytes but passing by pointer would change API semantics
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 8
	}

	if cfg.MaxJobsPerCycle <= 0 {
		cfg.MaxJobsPerCycle = 100
	}

	return cfg
}

func workerReplicaCount(instances int) int {
	if instances <= 1 {
		return 0
	}

	return instances - 1
}

func composeYAML(cfg Config) string { //nolint:gocritic // hugeParam: Config is 136 bytes but passing by pointer would change API semantics
	return strings.NewReplacer(
		"{{IMAGE}}", cfg.Image,
	).Replace(`x-worker-common: &worker-common
  image: {{IMAGE}}
  restart: unless-stopped
  env_file:
    - .env
  command: ["worker"]
  cpus: "1.5"
  mem_limit: "2g"
  mem_reservation: "1500m"
  shm_size: "1g"
  tmpfs:
    - /tmp:size=512m
  ulimits:
    nofile:
      soft: 65536
      hard: 65536
  logging:
    driver: json-file
    options:
      max-size: "10m"
      max-file: "3"

services:
  worker:
    <<: *worker-common

  worker_replica:
    <<: *worker-common`)
}

func newReplacer(cfg Config, dockerLogin, proxies string) *strings.Replacer { //nolint:gocritic // hugeParam: Config is 136 bytes but passing by pointer would change API semantics
	return strings.NewReplacer(
		"{{DOCKER_LOGIN}}", dockerLogin,
		"{{DATABASE_URL}}", cfg.DatabaseURL,
		"{{WORKER_INSTANCES}}", strconv.Itoa(cfg.Concurrency),
		"{{WORKER_REPLICA_COUNT}}", strconv.Itoa(workerReplicaCount(cfg.Concurrency)),
		"{{MAX_JOBS_PER_CYCLE}}", strconv.Itoa(cfg.MaxJobsPerCycle),
		"{{HASHID_SALT}}", cfg.HashIDSalt,
		"{{FAST_MODE}}", boolStr(cfg.FastMode),
		"{{PROXIES}}", proxies,
		"{{IMAGE}}", cfg.Image,
	)
}

// Generate creates a cloud-config YAML for cloud-init based worker setup.
// Both DigitalOcean and Hetzner support this format natively.
// SSH key is injected by the cloud provider at creation time;
// we harden SSH (port 2222, pubkey-only) so the app can reach workers
// from App Platform where port 22 is blocked.
func Generate(cfg Config) string { //nolint:gocritic // hugeParam: Config is 136 bytes but passing by pointer would change API semantics
	cfg = normalize(cfg)

	// Build optional docker registry login as a runcmd entry.
	dockerLogin := ""
	if loginCmd := dockerLoginCmd(cfg); loginCmd != "" {
		dockerLogin = "  - " + loginCmd + "\n"
	}

	proxies := strings.TrimSpace(cfg.Proxies)

	// cloud-config YAML processed by cloud-init on first boot.
	// write_files runs before runcmd (guaranteed by cloud-init ordering).
	// Uses {{PLACEHOLDER}} syntax to avoid corrupting values containing '%'.
	script := `#cloud-config
write_files:
  - path: /opt/gms-worker/.env
    permissions: '0600'
    content: |
      DATABASE_URL={{DATABASE_URL}}
      WORKER_INSTANCES={{WORKER_INSTANCES}}
      CONCURRENCY=1
      MAX_JOBS_PER_CYCLE={{MAX_JOBS_PER_CYCLE}}
      HASHID_SALT={{HASHID_SALT}}
      FAST_MODE={{FAST_MODE}}
      PROXIES={{PROXIES}}
      DISABLE_TELEMETRY=1
  - path: /opt/gms-worker/docker-compose.yml
    permissions: '0644'
    content: |
      x-worker-common: &worker-common
        image: {{IMAGE}}
        restart: unless-stopped
        env_file:
          - .env
        command: ["worker"]
        cpus: "1.5"
        mem_limit: "2g"
        mem_reservation: "1500m"
        shm_size: "1g"
        tmpfs:
          - /tmp:size=512m
        ulimits:
          nofile:
            soft: 65536
            hard: 65536
        logging:
          driver: json-file
          options:
            max-size: "10m"
            max-file: "3"

      services:
        worker:
          <<: *worker-common

        worker_replica:
          <<: *worker-common
runcmd:
  - |
    # Redirect port 2222 -> 22 so App Platform (which blocks port 22) can reach sshd
    iptables -t nat -A PREROUTING -p tcp --dport 2222 -j REDIRECT --to-port 22
    # Persist the rule across reboots
    mkdir -p /etc/iptables
    iptables-save > /etc/iptables/rules.v4
    # Install iptables-persistent non-interactively to auto-load rules on boot
    DEBIAN_FRONTEND=noninteractive apt-get install -y iptables-persistent
  - |
    # Harden SSH: disable password auth
    sed -i 's/^#\?PasswordAuthentication .*/PasswordAuthentication no/' /etc/ssh/sshd_config
    sed -i 's/^#\?PermitRootLogin .*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config
    systemctl restart ssh 2>/dev/null || systemctl restart sshd 2>/dev/null || true
  - |
    if ! command -v docker &> /dev/null; then
      curl -fsSL https://get.docker.com | sh
      systemctl enable docker
      systemctl start docker
    fi
{{DOCKER_LOGIN}}  - cd /opt/gms-worker && docker compose pull && docker compose up -d --remove-orphans --scale worker_replica={{WORKER_REPLICA_COUNT}}
`

	return newReplacer(cfg, dockerLogin, proxies).Replace(script)
}

// GenerateEnvFileContent generates the .env file content for a worker.
func GenerateEnvFileContent(cfg Config) string { //nolint:gocritic // hugeParam: Config is 136 bytes but passing by pointer would change API semantics
	cfg = normalize(cfg)

	r := strings.NewReplacer(
		"{{DATABASE_URL}}", cfg.DatabaseURL,
		"{{WORKER_INSTANCES}}", strconv.Itoa(cfg.Concurrency),
		"{{MAX_JOBS_PER_CYCLE}}", strconv.Itoa(cfg.MaxJobsPerCycle),
		"{{HASHID_SALT}}", cfg.HashIDSalt,
		"{{FAST_MODE}}", boolStr(cfg.FastMode),
		"{{PROXIES}}", strings.TrimSpace(cfg.Proxies),
	)

	return r.Replace(`DATABASE_URL={{DATABASE_URL}}
WORKER_INSTANCES={{WORKER_INSTANCES}}
CONCURRENCY=1
MAX_JOBS_PER_CYCLE={{MAX_JOBS_PER_CYCLE}}
HASHID_SALT={{HASHID_SALT}}
FAST_MODE={{FAST_MODE}}
PROXIES={{PROXIES}}
DISABLE_TELEMETRY=1`)
}

// GenerateUpdateCommand generates the SSH command to update worker configuration.
func GenerateUpdateCommand(cfg Config) string { //nolint:gocritic // hugeParam: Config is 136 bytes but passing by pointer would change API semantics
	cfg = normalize(cfg)

	envContent := GenerateEnvFileContent(cfg)
	composeContent := composeYAML(cfg)
	scale := strconv.Itoa(workerReplicaCount(cfg.Concurrency))

	return "cat > /opt/gms-worker/.env << 'ENVEOF'\n" + envContent + "\nENVEOF\n" +
		"cat > /opt/gms-worker/docker-compose.yml << 'COMPOSEEOF'\n" + composeContent + "\nCOMPOSEEOF\n" +
		"cd /opt/gms-worker && docker compose pull && docker compose up -d --remove-orphans --scale worker_replica=" + scale
}

// GenerateSetupScript generates a setup script for manual provisioning via SSH.
func GenerateSetupScript(cfg Config) string { //nolint:gocritic // hugeParam: Config is 136 bytes but passing by pointer would change API semantics
	cfg = normalize(cfg)

	dockerLogin := dockerLoginCmd(cfg)
	if dockerLogin == "" {
		dockerLogin = "echo 'No registry login required'"
	}

	proxies := strings.TrimSpace(cfg.Proxies)

	script := `#!/bin/bash
set -euo pipefail

echo "Starting worker setup..."

# Install Docker if not present
if ! command -v docker &> /dev/null; then
    echo "Docker is not installed. Installing Docker..."
    curl -fsSL https://get.docker.com | sh
    systemctl enable docker
    systemctl start docker
fi

# Login to registry
{{DOCKER_LOGIN}}

# Create the worker directory
mkdir -p /opt/gms-worker

# Create environment file
cat > /opt/gms-worker/.env << 'ENVEOF'
DATABASE_URL={{DATABASE_URL}}
WORKER_INSTANCES={{WORKER_INSTANCES}}
CONCURRENCY=1
MAX_JOBS_PER_CYCLE={{MAX_JOBS_PER_CYCLE}}
HASHID_SALT={{HASHID_SALT}}
FAST_MODE={{FAST_MODE}}
PROXIES={{PROXIES}}
DISABLE_TELEMETRY=1
ENVEOF

# Create docker-compose file
cat > /opt/gms-worker/docker-compose.yml << 'COMPOSEEOF'
x-worker-common: &worker-common
  image: {{IMAGE}}
  restart: unless-stopped
  env_file:
    - .env
  command: ["worker"]
  cpus: "1.5"
  mem_limit: "2g"
  mem_reservation: "1500m"
  shm_size: "1g"
  tmpfs:
    - /tmp:size=512m
  ulimits:
    nofile:
      soft: 65536
      hard: 65536
  logging:
    driver: json-file
    options:
      max-size: "10m"
      max-file: "3"

services:
  worker:
    <<: *worker-common

  worker_replica:
    <<: *worker-common
COMPOSEEOF

# Pull and start the worker
cd /opt/gms-worker
docker compose pull
docker compose up -d --remove-orphans --scale worker_replica={{WORKER_REPLICA_COUNT}}

echo "Worker setup complete!"
`

	return newReplacer(cfg, dockerLogin, proxies).Replace(script)
}
