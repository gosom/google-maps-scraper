package vps

import (
	"encoding/base64"
	"fmt"
)

type SetupConfig struct {
	SSHPort string
	Domain  string
	Host    string
}

func GenerateSetupScript(cfg SetupConfig) string {
	if cfg.SSHPort == "" {
		cfg.SSHPort = "22"
	}

	caddyConfig := generateCaddyfile(cfg.Domain, cfg.Host)

	return fmt.Sprintf(`#!/bin/bash
set -e

export DEBIAN_FRONTEND=noninteractive

apt-get update
apt-get install -y ca-certificates curl gnupg debian-keyring debian-archive-keyring apt-transport-https

# install docker
if ! command -v docker &> /dev/null; then
    curl -fsSL https://get.docker.com | sh
    systemctl enable docker
    systemctl start docker
fi

# install docker compose plugin
if ! docker compose version &> /dev/null; then
    apt-get install -y docker-compose-plugin
fi

# create app user with docker access, no sudo
if ! id -u app &> /dev/null; then
    useradd -r -m -s /bin/bash app
fi
usermod -aG docker app

# install caddy
if ! command -v caddy &> /dev/null; then
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
    apt-get update
    apt-get install -y caddy
fi

# configure caddy
cat > /etc/caddy/Caddyfile << 'CADDYEOF'
%s
CADDYEOF

systemctl enable caddy
systemctl restart caddy

# configure ufw
ufw --force reset
ufw default deny incoming
ufw default allow outgoing
ufw allow %s/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable

echo "Setup complete"
`, caddyConfig, cfg.SSHPort)
}

func WrapScript(script string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(script))

	return fmt.Sprintf(
		`echo '%s' | base64 -d > /tmp/gms-setup.sh && sudo bash /tmp/gms-setup.sh; ret=$?; rm -f /tmp/gms-setup.sh; exit $ret`,
		encoded,
	)
}

func GenerateDBScript(password string) string {
	return fmt.Sprintf(`#!/bin/bash
set -e

mkdir -p /opt/gms-db/data

if [ ! -f /opt/gms-db/server.crt ]; then
    openssl req -new -x509 -days 3650 -nodes \
        -subj "/CN=gms-postgres" \
        -keyout /opt/gms-db/server.key \
        -out /opt/gms-db/server.crt
    chmod 600 /opt/gms-db/server.key
    chown 999:999 /opt/gms-db/server.key /opt/gms-db/server.crt
fi

cat > /opt/gms-db/pg_hba.conf << 'HBAEOF'
local all all trust
hostssl all all 0.0.0.0/0 scram-sha-256
hostssl all all ::/0 scram-sha-256
HBAEOF

cat > /opt/gms-db/docker-compose.yml << 'COMPOSEEOF'
services:
  postgres:
    image: postgres:17
    restart: unless-stopped
    ports:
      - "5432:5432"
    volumes:
      - ./data:/var/lib/postgresql/data
      - ./server.crt:/var/lib/postgresql/server.crt:ro
      - ./server.key:/var/lib/postgresql/server.key:ro
      - ./pg_hba.conf:/var/lib/postgresql/pg_hba.conf:ro
    environment:
      POSTGRES_USER: gms
      POSTGRES_PASSWORD: %s
      POSTGRES_DB: gms
    command:
      - "postgres"
      - "-c"
      - "ssl=on"
      - "-c"
      - "ssl_cert_file=/var/lib/postgresql/server.crt"
      - "-c"
      - "ssl_key_file=/var/lib/postgresql/server.key"
      - "-c"
      - "hba_file=/var/lib/postgresql/pg_hba.conf"
COMPOSEEOF

cd /opt/gms-db && docker compose up -d

ufw allow 5432/tcp

echo "Database setup complete"
`, password)
}

type DeployScriptConfig struct {
	RegistryURL   string
	RegistryUser  string
	RegistryToken string
	ImageName     string
	DatabaseURL   string
	EncryptionKey string
	HashSalt      string
}

func GenerateDeployScript(cfg DeployScriptConfig) string { //nolint:gocritic // hugeParam: passing by value is intentional for immutable config
	fullImage := cfg.RegistryURL + "/" + cfg.ImageName

	var loginBlock string
	if cfg.RegistryUser != "" && cfg.RegistryToken != "" {
		loginBlock = fmt.Sprintf(`
# Login to container registry
echo '%s' | docker login %s -u %s --password-stdin
`, cfg.RegistryToken, cfg.RegistryURL, cfg.RegistryUser)
	}

	return fmt.Sprintf(`#!/bin/bash
set -e
%s
# Create app directory
mkdir -p /opt/gms-server

# Create .env file
cat > /opt/gms-server/.env << 'ENVEOF'
DATABASE_URL=%s
ENCRYPTION_KEY=%s
HASHID_SALT=%s
ENVEOF

chmod 600 /opt/gms-server/.env

# Create docker-compose.yml
cat > /opt/gms-server/docker-compose.yml << 'COMPOSEEOF'
services:
  server:
    image: %s
    restart: unless-stopped
    ports:
      - "127.0.0.1:8080:8080"
    env_file:
      - .env
    command: ["serve"]
COMPOSEEOF

# Pull and start
cd /opt/gms-server
docker compose pull
docker compose up -d

echo "Deployment complete"
`, loginBlock,
		cfg.DatabaseURL, cfg.EncryptionKey, cfg.HashSalt,
		fullImage)
}

func generateCaddyfile(domain, host string) string {
	if domain == "" {
		return fmt.Sprintf(`https://%s {
    tls internal
    reverse_proxy localhost:8080
}`, host)
	}

	return fmt.Sprintf(`%s {
    reverse_proxy localhost:8080
}`, domain)
}
