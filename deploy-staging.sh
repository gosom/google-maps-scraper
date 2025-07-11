#!/bin/bash
# Deployment script for Brezel.ai staging server
# This script pulls pre-built images from GitHub Container Registry

set -e

# Configuration
REGISTRY="ghcr.io"
BACKEND_IMAGE="yasseen-salama/google-maps-scraper"
FRONTEND_IMAGE="yasseen-salama/google-maps-scraper-webapp"
GITHUB_USER="${GITHUB_USER:-yasseen-salama}"
GITHUB_TOKEN="${GITHUB_TOKEN}"

echo "Starting deployment to staging server"
echo "Time: $(date)"
echo "User: $(whoami)"

# Check if running from correct directory
if [ ! -f "docker-compose.staging.yaml" ]; then
    echo "ERROR: docker-compose.staging.yaml not found. Are you in the correct directory?"
    exit 1
fi

# Login to GitHub Container Registry if token is provided
if [ ! -z "$GITHUB_TOKEN" ]; then
    echo "Logging in to GitHub Container Registry..."
    echo "$GITHUB_TOKEN" | docker login ghcr.io -u "$GITHUB_USER" --password-stdin
else
    echo "WARNING: GITHUB_TOKEN not set. Attempting to pull public images..."
fi

# Pull latest backend code
echo "Pulling latest code from git..."
git fetch origin
git checkout develop
git pull origin develop

# Setup environment
echo "Configuring environment variables..."
if [ ! -f ".env" ]; then
    echo "No .env file found, creating from template..."
    if [ -f ".env.example" ]; then
        cp .env.example .env
    else
        echo "ERROR: No .env or .env.example file found"
        exit 1
    fi
fi

# Backup current configuration
cp .env .env.backup.$(date +%Y%m%d_%H%M%S)

# Fix Docker networking for Linux
sed -i 's/host\.docker\.internal/172.17.0.1/g' .env

# Set concurrency for single-core servers
CPU_CORES=$(nproc)
echo "Server has $CPU_CORES CPU cores"
if [ $CPU_CORES -eq 1 ]; then
    echo "Configuring for single-core performance..."
    if grep -q "^CONCURRENCY=" .env; then
        sed -i "s/^CONCURRENCY=.*/CONCURRENCY=1/" .env
    else
        echo "CONCURRENCY=1" >> .env
    fi
fi

# Pull the pre-built backend image
echo "Pulling pre-built backend Docker image..."
if ! docker pull "${REGISTRY}/${BACKEND_IMAGE}:staging"; then
    echo "Failed to pull staging tag, trying develop tag..."
    if ! docker pull "${REGISTRY}/${BACKEND_IMAGE}:develop"; then
        echo "ERROR: Failed to pull backend image from registry"
        echo "Please ensure:"
        echo "1. The images are built and pushed by GitHub Actions"
        echo "2. You have access to the registry (set GITHUB_TOKEN if private)"
        exit 1
    fi
fi

# Check for frontend
FRONTEND_DIR="../scraper-webapp"
if [ ! -d "$FRONTEND_DIR" ]; then
    FRONTEND_DIR="../google-maps-scraper-webapp"
fi

if [ -d "$FRONTEND_DIR" ]; then
    echo "Found frontend at: $FRONTEND_DIR"
    
    # Update frontend code
    cd "$FRONTEND_DIR"
    git fetch origin
    git checkout develop || git checkout main
    git pull
    cd -
    
    # Pull frontend image
    echo "Pulling frontend Docker image..."
    if ! docker pull "${REGISTRY}/${FRONTEND_IMAGE}:staging"; then
        echo "WARNING: Failed to pull frontend image, will try to build locally"
        
        # Ensure .env.staging exists for frontend
        if [ ! -f "$FRONTEND_DIR/.env.staging" ]; then
            if [ -f "$FRONTEND_DIR/.env.example" ]; then
                cp "$FRONTEND_DIR/.env.example" "$FRONTEND_DIR/.env.staging"
            fi
            echo "NEXT_PUBLIC_API_URL=http://localhost:8080" >> "$FRONTEND_DIR/.env.staging"
        fi
        
        # Build frontend locally as fallback
        docker build -t gmaps-webapp-staging "$FRONTEND_DIR"
    fi
else
    echo "Frontend directory not found, proceeding with backend only"
fi

# Stop existing containers
echo "Stopping current containers..."
docker compose -f docker-compose.staging.yaml down --remove-orphans || true

# Clean up old images to save space
echo "Cleaning up old images..."
docker image prune -f || true

# Start containers with the new images
echo "Starting services with docker-compose..."
docker compose -f docker-compose.staging.yaml --env-file .env up -d

# Wait for startup
echo "Waiting for services to initialize..."
sleep 15

# Health check
echo "Checking backend health status..."
HEALTH_CHECK_URL="http://localhost:8080/health"
MAX_ATTEMPTS=30

for i in $(seq 1 $MAX_ATTEMPTS); do
    if curl -s -f $HEALTH_CHECK_URL > /dev/null 2>&1; then
        echo ""
        echo "Go Backend is running and healthy"
        echo ""
        echo "Staging deployment completed successfully!"
        echo "================================"
        echo "Staging backend API: http://$(hostname -I | awk '{print $1}'):8080"
        echo "Health endpoint: http://$(hostname -I | awk '{print $1}'):8080/health"
        echo "API documentation: http://$(hostname -I | awk '{print $1}'):8080/api/docs"
        
        if [ -d "$FRONTEND_DIR" ]; then
            echo "Staging frontend app: http://$(hostname -I | awk '{print $1}'):3000"
        fi
        
        echo ""
        echo "Running containers:"
        docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" | grep -E "(brezel|gmaps)" || true
        exit 0
    else
        echo "Health check attempt $i of $MAX_ATTEMPTS - Backend not ready yet..."
        
        if [ $i -eq $MAX_ATTEMPTS ]; then
            echo ""
            echo "ERROR: Backend failed to start after $MAX_ATTEMPTS attempts"
            echo ""
            echo "Container logs:"
            docker compose -f docker-compose.staging.yaml logs --tail 100
            exit 1
        fi
        
        sleep 3
    fi
done