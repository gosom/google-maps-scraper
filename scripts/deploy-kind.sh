#!/bin/bash
set -e

# Create kind cluster if it doesn't exist
if ! kind get clusters | grep -q "gmaps-cluster"; then
    echo "Creating kind cluster..."
    kind create cluster --name gmaps-cluster
fi

# Build the Docker image
echo "Building Docker image..."
docker build -t gmaps-scraper:latest .

# Load the image into kind
echo "Loading image into kind cluster..."
kind load docker-image gmaps-scraper:latest --name gmaps-cluster

# Apply Kubernetes manifests
echo "Applying Kubernetes manifests..."
kubectl apply -f k8s/

# Wait for deployment to be ready
echo "Waiting for deployment to be ready..."
kubectl wait --for=condition=available --timeout=60s deployment/gmaps-scraper

# Get service URL
echo "Getting service URL..."
kubectl get svc gmaps-scraper

echo "Deployment completed successfully!" 