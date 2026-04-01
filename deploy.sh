#!/bin/bash

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_header() {
    echo -e "${BLUE}[SMART-DEPLOY]${NC} $1"
}

cleanup_on_error() {
    print_error "Deployment failed. Cleaning up..."
    docker compose -f docker-compose.staging.yaml down --remove-orphans 2>/dev/null || true
}

trap cleanup_on_error ERR

print_header "Starting deploy.sh script..."

print_status "Running pre-flight checks..."

if ! docker info > /dev/null 2>&1; then
    print_error "Docker daemon is not running"
    exit 1
fi

if command -v lsof >/dev/null 2>&1; then
    if lsof -Pi :8080 -sTCP:LISTEN -t >/dev/null 2>&1; then
        print_warning "Port 8080 is already in use. Stopping existing services..."
        docker compose -f docker-compose.staging.yaml down --remove-orphans 2>/dev/null || true
    fi
    if lsof -Pi :3000 -sTCP:LISTEN -t >/dev/null 2>&1; then
        print_warning "Port 3000 is already in use. Stopping existing services..."
        docker compose -f docker-compose.staging.yaml down --remove-orphans 2>/dev/null || true
    fi
fi

if [ ! -f ".env.staging" ]; then
    print_warning ".env.staging not found. Creating from example..."
    if [ -f ".env.staging.example" ]; then
        cp .env.staging.example .env.staging
    else
        print_error ".env.staging.example not found. Cannot proceed."
        exit 1
    fi
fi

REQUIRED_VARS=("DSN" "CLERK_SECRET_KEY" "STRIPE_SECRET_KEY" "STRIPE_WEBHOOK_SECRET")
MISSING_VARS=()

for var in "${REQUIRED_VARS[@]}"; do
    if ! grep -q "^${var}=" .env.staging || grep -q "^${var}=$" .env.staging; then
        MISSING_VARS+=("$var")
    fi
done

if [ ${#MISSING_VARS[@]} -gt 0 ]; then
    print_error "Required variables missing or empty in .env.staging:"
    for var in "${MISSING_VARS[@]}"; do
        echo "   - $var"
    done
    exit 1
fi

print_status "Pre-flight checks passed"
echo

CPU_CORES=$(nproc 2>/dev/null || echo "1")
OPTIMAL_CONCURRENCY=$((CPU_CORES / 2))
if [ $OPTIMAL_CONCURRENCY -lt 1 ]; then
    OPTIMAL_CONCURRENCY=1
fi

print_status "System Analysis:"
echo "   CPU Cores: $CPU_CORES"
echo "   Optimal Concurrency: $OPTIMAL_CONCURRENCY"
echo

print_status "Configuring environment..."

if [ $CPU_CORES -eq 1 ]; then
    print_status "Single core server detected - setting explicit concurrency"
    if grep -q "^CONCURRENCY=" .env.staging; then
        sed -i "s/^CONCURRENCY=.*/CONCURRENCY=1/" .env.staging
    elif grep -q "^# CONCURRENCY=" .env.staging; then
        sed -i "s/^# CONCURRENCY=.*/CONCURRENCY=1/" .env.staging
    else
        echo "" >> .env.staging
        echo "CONCURRENCY=1" >> .env.staging
    fi
else
    print_status "Multi-core server detected - using auto-detection"
    sed -i '/^CONCURRENCY=/d' .env.staging
fi

print_status "Current configuration:"
DB_HOST=$(grep "^DSN=" .env.staging | cut -d'@' -f2 | cut -d':' -f1 || echo "unknown")
echo "   Database: $DB_HOST"
if grep -q "^CONCURRENCY=" .env.staging; then
    CONCURRENCY_VAL=$(grep "^CONCURRENCY=" .env.staging | cut -d'=' -f2)
    echo "   Concurrency: $CONCURRENCY_VAL (explicit)"
else
    echo "   Concurrency: Auto-detected ($OPTIMAL_CONCURRENCY)"
fi
echo

print_status "Building Docker image..."
if [ "${NO_CACHE:-0}" = "1" ]; then
    print_warning "Building with --no-cache"
    docker build --no-cache -t brezel-staging-test .
else
    docker build -t brezel-staging-test .
fi

print_status "Stopping existing containers..."
docker compose -f docker-compose.staging.yaml down --remove-orphans 2>/dev/null || true

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
if docker images | grep -q "brezel-staging-test"; then
    docker tag brezel-staging-test:latest brezel-staging-test:backup-${TIMESTAMP} 2>/dev/null || true
    print_status "Created backup tag: brezel-staging-test:backup-${TIMESTAMP}"
fi

print_status "Starting app..."
docker compose -f docker-compose.staging.yaml --env-file .env.staging up -d

print_status "Waiting for application startup..."
sleep 5

print_status "Monitoring startup process..."
BACKEND_HEALTHY=false

for i in {1..30}; do
    if curl -s -f http://localhost:8080/health >/dev/null 2>&1; then
        print_status "✅ Backend is healthy!"
        BACKEND_HEALTHY=true
        break
    else
        if [ $i -eq 30 ]; then
            print_error "❌ Backend failed to start"
            echo
            print_error "🔍 Debugging information:"
            echo "Container status:"
            docker ps -a | grep brezel
            echo
            echo "Recent backend logs:"
            docker logs brezelscraper-backend --tail 50
            echo
            echo "Recent frontend logs:"
            docker logs brezelscraper-frontend --tail 50
            echo
            print_error "💡 Troubleshooting suggestions:"
            echo "   1. Check database connectivity from container"
            echo "   2. Verify .env.staging DSN uses host.docker.internal"
            echo "   3. Check container logs: docker logs brezelscraper-backend"
            echo "   4. Test: docker exec brezelscraper-backend ping host.docker.internal"
            exit 1
        fi
        echo "   Startup attempt $i/30..."
        sleep 2
    fi
done

print_status "Testing frontend health..."
FRONTEND_HEALTHY=false

for i in {1..10}; do
    if curl -s -f http://localhost:3000 >/dev/null 2>&1; then
        print_status "✅ Frontend is responding!"
        FRONTEND_HEALTHY=true
        break
    else
        if [ $i -eq 10 ]; then
            print_warning "⚠️ Frontend may not be fully ready yet"
            docker logs brezelscraper-frontend --tail 20
        fi
        sleep 2
    fi
done

print_status "Testing backend-frontend connectivity..."
if docker exec brezelscraper-frontend sh -c "wget -q -O- http://brezelscraper-backend:8080/health" >/dev/null 2>&1; then
    print_status "✅ Frontend can reach backend"
else
    print_warning "⚠️ Frontend cannot reach backend"
fi

print_header "🎉 Deployment Successful!"
echo

SERVER_IP=$(hostname -I | awk '{print $1}' 2>/dev/null || echo "localhost")
CONTAINER_CPU_INFO=$(docker exec brezelscraper-backend nproc 2>/dev/null || echo "unknown")

print_status "Server Info:"
echo "   Server IP: $SERVER_IP"
echo "   Host CPUs: $CPU_CORES"
echo "   Container CPUs: $CONTAINER_CPU_INFO"
echo "   Deployment Time: $(date)"
echo

print_status "Application URLs:"
echo "   Backend API:    http://$SERVER_IP:8080/"
echo "   Frontend:       http://$SERVER_IP:3000/"
echo "   Health Check:   http://$SERVER_IP:8080/health"
echo "   API Status:     http://$SERVER_IP:8080/api/v1/status"
echo

print_status "Application Status:"
if command -v jq >/dev/null 2>&1; then
    curl -s http://localhost:8080/api/v1/status | jq . 2>/dev/null || curl -s http://localhost:8080/api/v1/status
else
    curl -s http://localhost:8080/api/v1/status
fi
echo

print_status "Management Commands:"
echo "   Backend logs:   docker logs brezelscraper-backend -f"
echo "   Frontend logs:  docker logs brezelscraper-frontend -f"
echo "   All logs:       docker compose -f docker-compose.staging.yaml logs -f"
echo "   Restart:        docker compose -f docker-compose.staging.yaml restart"
echo "   Stop:           docker compose -f docker-compose.staging.yaml down"
echo

print_status "Current Resource Usage:"
docker stats --no-stream brezelscraper-backend brezelscraper-frontend 2>/dev/null || true
echo

print_header "Your application is ready and optimized for $CPU_CORES CPU core(s)."

print_status "Cleaning up old backup images..."
docker images | grep "brezel-staging-test:backup-" | tail -n +6 | awk '{print $3}' | xargs -r docker rmi 2>/dev/null || true