#!/bin/bash

# Brezel.ai Staging Deployment Script
# This script automates the deployment process for Linux servers

set -e  # Exit on any error

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to check if command exists
check_command() {
    if ! command -v $1 &> /dev/null; then
        print_error "$1 is not installed. Please install it first."
        exit 1
    fi
}

# Function to check if service is running
check_service() {
    if ! systemctl is-active --quiet $1; then
        print_error "$1 service is not running. Please start it first."
        exit 1
    fi
}

print_status "🚀 Starting Brezel.ai deployment..."

# Check prerequisites
print_status "Checking prerequisites..."
check_command "docker"
check_command "docker-compose"
check_command "git"

# Check if PostgreSQL is running (if local)
if systemctl list-units --full -all | grep -Fq "postgresql.service"; then
    check_service "postgresql"
    print_status "✅ PostgreSQL service is running"
else
    print_warning "PostgreSQL service not found locally. Make sure external PostgreSQL is accessible."
fi

# Check if .env.staging exists
if [ ! -f ".env.staging" ]; then
    print_warning ".env.staging not found. Creating from example..."
    if [ -f ".env.staging.example" ]; then
        cp .env.staging.example .env.staging
        print_warning "⚠️  Please edit .env.staging with your database credentials before continuing."
        print_warning "   Run: nano .env.staging"
        read -p "Press Enter after editing .env.staging to continue..."
    else
        print_error ".env.staging.example not found. Please create .env.staging manually."
        exit 1
    fi
fi

# Build Docker image
print_status "Building Docker image..."
docker build -t brezel-staging-test . || {
    print_error "Failed to build Docker image"
    exit 1
}

print_status "✅ Docker image built successfully"

# Stop existing containers
print_status "Stopping existing containers..."
docker compose -f docker-compose.staging.yaml down --remove-orphans 2>/dev/null || true

# Start the application
print_status "Starting the application..."
docker compose -f docker-compose.staging.yaml --env-file .env.staging up -d || {
    print_error "Failed to start the application"
    exit 1
}

# Wait for the application to start
print_status "Waiting for application to start..."
sleep 10

# Check if the application is running
print_status "Verifying deployment..."

# Test health endpoint
for i in {1..30}; do
    if curl -s -f http://localhost:8080/health > /dev/null 2>&1; then
        print_status "✅ Application is healthy!"
        break
    else
        if [ $i -eq 30 ]; then
            print_error "Application failed to start properly"
            print_error "Check logs with: docker logs google-maps-scraper-2-brezel-api-1"
            exit 1
        fi
        print_status "Waiting for application... ($i/30)"
        sleep 2
    fi
done

# Display status
print_status "🎉 Deployment completed successfully!"
echo
print_status "Application URLs:"
echo "  Health Check: http://localhost:8080/health"
echo "  API Status:   http://localhost:8080/api/v1/status"
echo "  Web UI:       http://localhost:8080/"
echo "  API Docs:     http://localhost:8080/api/docs"
echo

print_status "Useful commands:"
echo "  View logs:    docker logs google-maps-scraper-2-brezel-api-1"
echo "  Stop app:     docker compose -f docker-compose.staging.yaml down"
echo "  Restart app:  docker compose -f docker-compose.staging.yaml restart"
echo

# Show application status
print_status "Current status:"
curl -s http://localhost:8080/api/v1/status | jq . 2>/dev/null || curl -s http://localhost:8080/api/v1/status

print_status "Deployment completed successfully! 🚀"