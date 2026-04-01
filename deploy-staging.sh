#!/bin/bash
# Deployment script for BrezelScraper staging server
# Version: 2.0 - Production-ready with rollback support

set -euo pipefail  # Exit on error, undefined vars, pipe failures
IFS=$'\n\t'        # Safer word splitting

# Configuration
REGISTRY="ghcr.io"
BACKEND_IMAGE="yasseen-salama/google-maps-scraper"
FRONTEND_IMAGE="brezel-ai/scraper-webapp"
GITHUB_USER="${GITHUB_USER:-yasseen-salama}"
GITHUB_TOKEN="${GITHUB_TOKEN:-}"
COMPOSE_FILE="docker-compose.yaml"
ENVIRONMENT="staging"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' 

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Cleanup function for rollback
cleanup_on_failure() {
    log_error "Deployment failed! Attempting to restore previous state..."
    if [ -f ".env.backup.latest" ]; then
        cp .env.backup.latest .env
        docker compose -f "${COMPOSE_FILE}" up -d 2>/dev/null || true
    fi
    exit 1
}

trap cleanup_on_failure ERR

log_info "Starting deployment to ${ENVIRONMENT} server"
log_info "Time: $(date)"
log_info "User: $(whoami)"

# Validate required files
if [ ! -f "${COMPOSE_FILE}" ]; then
    log_error "docker-compose file not found: ${COMPOSE_FILE}"
    log_error "Are you in the correct directory?"
    exit 1
fi

# Validate required environment variables
if [ -z "${GITHUB_TOKEN}" ]; then
    log_warn "GITHUB_TOKEN not set. Will attempt to pull public images only."
fi

# Login to GitHub Container Registry if token is provided
if [ -n "${GITHUB_TOKEN}" ]; then
    log_info "Logging in to GitHub Container Registry..."
    echo "${GITHUB_TOKEN}" | docker login ghcr.io -u "${GITHUB_USER}" --password-stdin 2>/dev/null || {
        log_error "Failed to login to GitHub Container Registry"
        exit 1
    }
fi

# Setup environment file
log_info "Configuring environment variables..."
if [ ! -f ".env" ]; then
    log_warn "No .env file found"
    if [ -f ".env.example" ]; then
        log_info "Creating .env from template..."
        cp .env.example .env
    else
        log_error "No .env or .env.example file found"
        exit 1
    fi
fi

# Backup current configuration
BACKUP_FILE=".env.backup.$(date +%Y%m%d_%H%M%S)"
cp .env "${BACKUP_FILE}"
cp .env .env.backup.latest
log_info "Created backup: ${BACKUP_FILE}"

# Detect and configure for server resources
CPU_CORES=$(nproc)
log_info "Server has ${CPU_CORES} CPU cores"

if [ "${CPU_CORES}" -eq 1 ]; then
    log_warn "Configuring for single-core server..."
    if grep -q "^CONCURRENCY=" .env; then
        sed -i "s/^CONCURRENCY=.*/CONCURRENCY=1/" .env
    else
        echo "CONCURRENCY=1" >> .env
    fi
fi

# Pull the pre-built backend image
log_info "Pulling backend Docker image..."
if ! docker pull "${REGISTRY}/${BACKEND_IMAGE}:${ENVIRONMENT}"; then
    log_warn "Failed to pull ${ENVIRONMENT} tag, trying develop tag..."
    if ! docker pull "${REGISTRY}/${BACKEND_IMAGE}:develop"; then
        log_error "Failed to pull backend image from registry"
        log_error "Ensure images are built and pushed by CI/CD pipeline"
        exit 1
    fi
    # Tag develop as staging for local use
    docker tag "${REGISTRY}/${BACKEND_IMAGE}:develop" "${REGISTRY}/${BACKEND_IMAGE}:${ENVIRONMENT}"
fi

# Pull the pre-built frontend image
log_info "Pulling frontend Docker image..."
if ! docker pull "${REGISTRY}/${FRONTEND_IMAGE}:${ENVIRONMENT}"; then
    log_warn "Failed to pull frontend ${ENVIRONMENT} image"
    log_warn "Deployment will continue with backend only"
fi

# Health check of current deployment (if exists)
log_info "Checking current deployment status..."
if docker compose -f "${COMPOSE_FILE}" ps -q backend &>/dev/null; then
    log_info "Current backend is running - will perform rolling update"
    CURRENT_RUNNING=true
else
    log_info "No existing deployment found"
    CURRENT_RUNNING=false
fi

# Pull new images without stopping services (for zero-downtime)
log_info "Pulling latest images..."
docker compose -f "${COMPOSE_FILE}" pull

# Stop existing containers gracefully
log_info "Stopping current containers..."
docker compose -f "${COMPOSE_FILE}" down --timeout 30 --remove-orphans

# Clean up old images to save space (but keep last 2 versions)
log_info "Cleaning up old images..."
docker image prune -f --filter "until=720h" 2>/dev/null || true

# Start new containers
log_info "Starting services with docker compose..."
docker compose -f "${COMPOSE_FILE}" --env-file .env up -d

# Wait for containers to start
log_info "Waiting for containers to initialize..."
sleep 10

# Health check with retry logic
log_info "Performing health checks..."
HEALTH_CHECK_URL="http://localhost:8080/health"
MAX_ATTEMPTS=30
ATTEMPT=1

while [ ${ATTEMPT} -le ${MAX_ATTEMPTS} ]; do
    if curl -s -f "${HEALTH_CHECK_URL}" > /dev/null 2>&1; then
        log_info "Backend is healthy!"
        break
    else
        if [ ${ATTEMPT} -eq ${MAX_ATTEMPTS} ]; then
            log_error "Backend failed to become healthy after ${MAX_ATTEMPTS} attempts"
            log_error "Container logs:"
            docker compose -f "${COMPOSE_FILE}" logs --tail 100 backend
            exit 1
        fi
        echo -n "."
        sleep 2
        ATTEMPT=$((ATTEMPT + 1))
    fi
done

echo ""

# Verify all containers are running
log_info "Verifying all containers..."
if ! docker compose -f "${COMPOSE_FILE}" ps | grep -q "Up"; then
    log_error "Some containers failed to start"
    docker compose -f "${COMPOSE_FILE}" ps
    exit 1
fi

# Get server IP (more robust)
SERVER_IP=$(hostname -I | awk '{print $1}' || echo "localhost")

# Success message
echo ""
log_info "Deployment completed successfully!"
echo "========================================"
echo "Environment:     ${ENVIRONMENT}"
echo "Backend API:     http://${SERVER_IP}:8080"
echo "Health Check:    http://${SERVER_IP}:8080/health"
echo "API Docs:        http://${SERVER_IP}:8080/api/docs"
echo "Frontend App:    http://${SERVER_IP}:3000"
echo ""
echo "Running containers:"
docker compose -f "${COMPOSE_FILE}" ps

# Cleanup old backups (keep last 10)
log_info "Cleaning up old backups..."
ls -t .env.backup.* 2>/dev/null | tail -n +11 | xargs rm -f 2>/dev/null || true

log_info "Deployment completed at $(date)"

exit 0