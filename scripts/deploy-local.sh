#!/bin/bash
set -e

# Enable debug mode if DEBUG environment variable is set
if [ "${DEBUG}" = "true" ]; then
    set -x
fi

# Configuration section
# -------------------
# CLUSTER_NAME: Name of the kind cluster
# NAMESPACE: Kubernetes namespace for deployment
# RELEASE_NAME: Helm release name
# CHART_PATH: Path to the Helm chart
# IMAGE_NAME: Docker image name (matches Makefile)
# IMAGE_TAG: Docker image tag (matches version)

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

# Print step description
print_step() {
    echo -e "${YELLOW}$1${NC}"
}

# Configuration section
CLUSTER_NAME="gmaps-cluster"
NAMESPACE="default"
RELEASE_NAME="gmaps-scraper-leads-scraper-service"
CHART_PATH="./charts/leads-scraper-service"
IMAGE_NAME="${DOCKER_IMAGE:-feelguuds/leads-scraper-service}"
IMAGE_TAG="${DOCKER_TAG:-latest}"
ENABLE_TESTS="false"
REDIS_PASSWORD="redis-local-dev"

# Parse arguments
while [ "$#" -gt 0 ]; do
    case "$1" in
        --enable-tests) ENABLE_TESTS="true"; shift ;;
        *) echo "Unknown parameter: $1"; exit 1 ;;
    esac
done

# setup_cluster function
# --------------------
# 1. Checks if kind cluster exists
# 2. If not, creates a new cluster with:
#    - Port mapping from host 8080 to container 8080
#    - Single control-plane node
#    - Custom configuration for local development
setup_cluster() {
    print_step "Setting up kind cluster..."
    
    # Verify kind is executable
    if ! kind version; then
        echo "Error: kind is not properly installed or not executable"
        echo "Trying to reinstall kind..."
        sudo rm -f /usr/local/bin/kind
        ./script-runners/install-tools.sh
    fi

    if ! kind get clusters | grep -q "$CLUSTER_NAME"; then
        echo "Creating kind cluster: $CLUSTER_NAME"
        cat <<EOF | kind create cluster --name "$CLUSTER_NAME" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 8080
    hostPort: 8080
    protocol: TCP
EOF
    else
        echo "Kind cluster $CLUSTER_NAME already exists"
    fi
}

# build_and_load_image function
# ---------------------------
# 1. Builds Docker image using the Dockerfile
# 2. Tags it appropriately
# 3. Loads it into kind cluster
# Note: kind clusters can't pull from local Docker daemon, so we load directly
build_and_load_image() {
    print_step "Building Docker image..."
    docker build -t "${IMAGE_NAME}:${IMAGE_TAG}" .

    print_step "Loading image into kind cluster..."
    kind load docker-image "${IMAGE_NAME}:${IMAGE_TAG}" --name "$CLUSTER_NAME"
}

# deploy_helm function
# ------------------
# 1. Creates a temporary values.local.yaml for local settings
# 2. Configures:
#    - Image details
#    - Service settings
#    - Basic scraper configuration
# 3. Deploys using Helm with:
#    - Namespace creation if needed
#    - Local values override
#    - Waits for completion
deploy_helm() {
    print_step "Deploying with Helm..."
    
    echo "Test status: ENABLE_TESTS=${ENABLE_TESTS}"

    # Ensure release name matches what test script expects
    echo "Using release name: $RELEASE_NAME"

    # Create values override file
    cat <<EOF > values.local.yaml
image:
  repository: ${IMAGE_NAME}
  tag: ${IMAGE_TAG}
  pullPolicy: IfNotPresent

service:
  type: ClusterIP
  port: 8080

config:
  redis:
    enabled: true
    dsn: "redis://:${REDIS_PASSWORD}@${RELEASE_NAME}-redis-master:6379/0"
    workers: 10
  scraper:
    webServer: true
    concurrency: 11
    depth: 5
    language: "en"
    searchRadius: 10000
    zoomLevel: 15
    fastMode: true

tests:
  enabled: ${ENABLE_TESTS}
  healthCheck:
    enabled: true
    path: "/health"
  configCheck:
    enabled: true

postgresql:
  enabled: true  # Enable PostgreSQL by default for local development
  auth:
    username: postgres
    password: postgres
    database: leads_scraper
    # PostgreSQL password will be stored in a secret
    existingSecret: ""
  primary:
    persistence:
      enabled: true
      size: 10Gi
    resources:
      requests:
        cpu: 100m
        memory: 256Mi
      limits:
        cpu: 1000m
        memory: 1Gi
    service:
      ports:
        postgresql: 5432
    # PostgreSQL configuration
    extraEnvVars:
      - name: POSTGRESQL_MAX_CONNECTIONS
        value: "100"
      - name: POSTGRESQL_SHARED_BUFFERS
        value: "128MB"

redis:
  enabled: true
  architecture: standalone
  auth:
    enabled: true
    password: "${REDIS_PASSWORD}"
  master:
    persistence:
      enabled: true
      size: 1Gi
    resources:
      requests:
        cpu: 100m
        memory: 128Mi
      limits:
        cpu: 500m
        memory: 256Mi
EOF

    echo "Generated values.local.yaml:"
    cat values.local.yaml

    # Add Bitnami repo if not already added
    if ! helm repo list | grep -q "bitnami"; then
        helm repo add bitnami https://charts.bitnami.com/bitnami
        helm repo update
    fi

    helm upgrade --install "$RELEASE_NAME" "$CHART_PATH" \
        --namespace "$NAMESPACE" \
        --create-namespace \
        -f values.local.yaml \
        --wait

    if [ $? -eq 0 ]; then
        echo -e "${GREEN}Deployment successful!${NC}"
        echo "Helm release '$RELEASE_NAME' deployed to namespace '$NAMESPACE'"
        rm values.local.yaml
    else
        echo "Deployment failed!"
        rm values.local.yaml
        exit 1
    fi
}

# verify_deployment function
# ------------------------
# 1. Waits for pods to be ready
# 2. Shows deployment status
# 3. Shows service details
verify_deployment() {
    print_step "Verifying deployment..."
    
    # Wait for Redis to be ready first
    echo "Waiting for Redis to be ready..."
    if ! kubectl wait --for=condition=ready pod -l "app.kubernetes.io/name=redis,app.kubernetes.io/instance=$RELEASE_NAME" \
        --namespace "$NAMESPACE" \
        --timeout=300s; then
        echo -e "${RED}Redis pods did not become ready within timeout${NC}"
        kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/name=redis"
        exit 1
    fi

    # Additional Redis readiness check
    echo "Verifying Redis connectivity..."
    for i in {1..30}; do
        if kubectl run redis-test-$i --rm --restart=Never -i --image redis -- redis-cli -h "$RELEASE_NAME-redis-master" -a redis-local-dev ping | grep -q "PONG"; then
            echo "Redis is accepting connections"
            break
        fi
        if [ $i -eq 30 ]; then
            echo -e "${RED}Redis is not accepting connections after multiple attempts${NC}"
            echo "Redis service status:"
            kubectl get svc -n "$NAMESPACE" -l "app.kubernetes.io/name=redis"
            echo "Redis pod status:"
            kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/name=redis"
            exit 1
        fi
        echo "Waiting for Redis to accept connections (attempt $i/30)..."
        sleep 5
    done

    # Wait for application pods to be ready
    echo "Waiting for application pods to be ready..."
    if ! kubectl wait --for=condition=ready pod -l "app.kubernetes.io/instance=$RELEASE_NAME" \
        --namespace "$NAMESPACE" \
        --timeout=300s; then
        echo -e "${RED}Application pods did not become ready within timeout${NC}"
        echo "Application pod logs:"
        kubectl logs -l "app.kubernetes.io/instance=$RELEASE_NAME" -n "$NAMESPACE"
        echo "Pod status:"
        kubectl get pods -n "$NAMESPACE"
        exit 1
    fi

    # Get deployment status
    echo "Checking deployment status..."
    kubectl get deployment -n "$NAMESPACE" "$RELEASE_NAME"
    
    # Get service details
    echo "Checking service details..."
    kubectl get svc -n "$NAMESPACE" "$RELEASE_NAME"
    
    echo -e "${GREEN}Deployment verification complete!${NC}"
    echo "To access the service, use: kubectl port-forward -n $NAMESPACE svc/$RELEASE_NAME 8080:8080"
}

# Main execution
main() {
    setup_cluster
    build_and_load_image
    deploy_helm
    verify_deployment
    
    echo -e "\n${GREEN}Development environment is ready!${NC}"
    echo "To access the service, run:"
    echo "kubectl port-forward -n $NAMESPACE svc/$RELEASE_NAME 8080:8080"
}

# Execute main function
main 