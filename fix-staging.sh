#!/bin/bash

# Quick fix for the staging server concurrency issue

echo "🔧 Fixing staging server issues..."

# 1. Fix the .env.staging file for Linux
echo "Fixing .env.staging for Linux compatibility..."
if [ -f ".env.staging" ]; then
    # Replace host.docker.internal with 172.17.0.1 for Linux
    sed -i 's/host\.docker\.internal/172.17.0.1/g' .env.staging
    echo "✅ Updated .env.staging for Linux"
else
    echo "❌ .env.staging not found. Creating from example..."
    cp .env.staging.example .env.staging
    echo "⚠️  Please edit .env.staging with your database password"
fi

# 2. Stop current containers
echo "Stopping current containers..."
docker compose -f docker-compose.staging.yaml down 2>/dev/null || true

# 3. Rebuild the image with the concurrency fix
echo "Rebuilding Docker image with fixes..."
docker build -t brezel-staging-test .

# 4. Start the application with explicit concurrency
echo "Starting application with fixed configuration..."
docker compose -f docker-compose.staging.yaml --env-file .env.staging up -d

# 5. Wait and test
echo "Waiting for application to start..."
sleep 10

# 6. Check if it's working
echo "Testing application health..."
for i in {1..30}; do
    if curl -s -f http://localhost:8080/health >/dev/null 2>&1; then
        echo "✅ Application is now healthy!"
        break
    else
        if [ $i -eq 30 ]; then
            echo "❌ Application still not responding. Check logs:"
            echo "   docker logs google-maps-scraper-brezel-api-1"
            exit 1
        fi
        echo "Waiting... ($i/30)"
        sleep 2
    fi
done

echo "🎉 Staging server fixed successfully!"
echo
echo "Application URLs:"
SERVER_IP=$(hostname -I | awk '{print $1}' 2>/dev/null || echo "localhost")
echo "  🌐 Web UI:       http://$SERVER_IP:8080/"
echo "  💚 Health Check: http://$SERVER_IP:8080/health"
echo "  📊 API Status:   http://$SERVER_IP:8080/api/v1/status"
echo
echo "Check status:"
curl -s http://localhost:8080/api/v1/status | jq . 2>/dev/null || curl -s http://localhost:8080/api/v1/status