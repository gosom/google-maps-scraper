#!/bin/bash

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

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

print_header "Starting deploy.sh script..."

# 1. Detect CPU configuration
CPU_CORES=$(nproc 2>/dev/null || echo "1")
OPTIMAL_CONCURRENCY=$((CPU_CORES / 2))
if [ $OPTIMAL_CONCURRENCY -lt 1 ]; then
    OPTIMAL_CONCURRENCY=1
fi

print_status "System Analysis:"
echo "   CPU Cores: $CPU_CORES"
echo "   Optimal Concurrency: $OPTIMAL_CONCURRENCY"
echo

# 2. Check and fix .env.staging
print_status "Configuring environment..."
if [ ! -f ".env.staging" ]; then
    print_warning ".env.staging not found. Creating from example..."
    cp .env.staging.example .env.staging
fi

# Fix DSN for Linux if needed
if grep -q "host\.docker\.internal" .env.staging; then
    print_status "Fixing DSN for Linux compatibility..."
    sed -i 's/host\.docker\.internal/172.17.0.1/g' .env.staging
fi

# Add or update CONCURRENCY setting if on single core
if [ $CPU_CORES -eq 1 ]; then
    print_status "Single core server detected - setting explicit concurrency"
    if grep -q "^CONCURRENCY=" .env.staging; then
        sed -i "s/^CONCURRENCY=.*/CONCURRENCY=1/" .env.staging
    elif grep -q "^# CONCURRENCY=" .env.staging; then
        sed -i "s/^# CONCURRENCY=.*/CONCURRENCY=1/" .env.staging
    else
        echo "" >> .env.staging
        echo "# Single-core server configuration" >> .env.staging
        echo "CONCURRENCY=1" >> .env.staging
    fi
else
    print_status "🚀 Multi-core server detected - using auto-detection"
    # Remove explicit CONCURRENCY setting to use auto-detection
    sed -i '/^CONCURRENCY=/d' .env.staging
fi

print_status "Current configuration:"
echo "   Database: $(grep "^DSN=" .env.staging | cut -d'@' -f2 | cut -d':' -f1)"
if grep -q "^CONCURRENCY=" .env.staging; then
    echo "   Concurrency: $(grep "^CONCURRENCY=" .env.staging | cut -d'=' -f2) (explicit)"
else
    echo "   Concurrency: Auto-detected ($OPTIMAL_CONCURRENCY)"
fi
echo

# 3. Build with no cache to ensure latest fixes
print_status "Building Docker image with CPU optimizations..."
docker build --no-cache -t brezel-staging-test .

# 4. Stop existing containers
print_status "Stopping existing containers..."
docker compose -f docker-compose.staging.yaml down --remove-orphans 2>/dev/null || true

# 5. Start application
print_status "Starting app..."
docker compose -f docker-compose.staging.yaml --env-file .env.staging up -d

# 6. Wait and monitor
print_status "⏳ Waiting for application startup..."
sleep 5

# 7. Test startup with detailed monitoring
print_status "🔍 Monitoring startup process..."
for i in {1..30}; do
    if curl -s -f http://localhost:8080/health >/dev/null 2>&1; then
        print_status "✅ Application is healthy!"
        break
    else
        if [ $i -eq 30 ]; then
            print_error "❌ Application failed to start"
            echo
            print_error "🔍 Debugging information:"
            echo "Container status:"
            docker ps -a | grep brezel
            echo
            echo "Recent logs:"
            docker logs google-maps-scraper-brezel-api-1 --tail 10
            echo
            print_error "💡 Troubleshooting suggestions:"
            echo "   1. Check database connectivity: pg_isready -h 172.17.0.1 -p 5432"
            echo "   2. Verify .env.staging configuration"
            echo "   3. Check container logs: docker logs google-maps-scraper-brezel-api-1"
            exit 1
        fi
        echo "   Startup attempt $i/30..."
        sleep 2
    fi
done

# 8. Show success information
print_header "🎉 Deployment Successful!"
echo

# Get server info
SERVER_IP=$(hostname -I | awk '{print $1}' 2>/dev/null || echo "localhost")
CONTAINER_CPU_INFO=$(docker exec google-maps-scraper-brezel-api-1 nproc 2>/dev/null || echo "unknown")

print_status "🖥️ Server Information:"
echo "   Server IP: $SERVER_IP"
echo "   Host CPUs: $CPU_CORES"
echo "   Container CPUs: $CONTAINER_CPU_INFO"
echo

print_status "🌐 Application URLs:"
echo "   Web Interface:  http://$SERVER_IP:8080/"
echo "   Health Check:   http://$SERVER_IP:8080/health"
echo "   API Status:     http://$SERVER_IP:8080/api/v1/status"
echo "   API Docs:       http://$SERVER_IP:8080/api/docs"
echo

print_status "📊 Application Status:"
curl -s http://localhost:8080/api/v1/status | jq . 2>/dev/null || curl -s http://localhost:8080/api/v1/status
echo

print_status "🛠️ Management Commands:"
echo "   View logs:      docker logs google-maps-scraper-brezel-api-1"
echo "   Restart:        docker compose -f docker-compose.staging.yaml restart"
echo "   Stop:           docker compose -f docker-compose.staging.yaml down"
echo "   Monitor:        docker stats google-maps-scraper-brezel-api-1"
echo

print_header "✨ Your application is ready and optimized for $CPU_CORES CPU core(s)!"