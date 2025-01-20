#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

# Configuration
NAMESPACE="${NAMESPACE:-default}"
RELEASE_NAME="${RELEASE_NAME:-gmaps-scraper-leads-scraper-service}"

# Function to check if helm release exists
check_helm_release() {
    if ! helm status "$RELEASE_NAME" -n "$NAMESPACE" >/dev/null 2>&1; then
        echo -e "${RED}Error: Helm release '$RELEASE_NAME' not found in namespace '$NAMESPACE'${NC}"
        echo "Available releases:"
        helm list -n "$NAMESPACE"
        echo "Available deployments:"
        kubectl get deployments -n "$NAMESPACE"
        exit 1
    fi
}

# Function to wait for PostgreSQL to be ready
wait_for_postgres() {
    echo "Waiting for PostgreSQL StatefulSet to be ready..."
    kubectl rollout status statefulset "$RELEASE_NAME-postgresql" -n "$NAMESPACE" --timeout=120s || {
        echo -e "${RED}PostgreSQL StatefulSet rollout failed${NC}"
        kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/name=postgresql"
        return 1
    }

    # Get PostgreSQL pod name
    PG_POD=$(kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/name=postgresql" -o jsonpath="{.items[0].metadata.name}")
    
    echo "Waiting for PostgreSQL to accept connections..."
    RETRIES=0
    MAX_RETRIES=30
    until kubectl exec -n "$NAMESPACE" "$PG_POD" -- pg_isready -U postgres || [ $RETRIES -eq $MAX_RETRIES ]; do
        echo "Waiting for PostgreSQL to be ready... ($RETRIES/$MAX_RETRIES)"
        sleep 2
        RETRIES=$((RETRIES + 1))
    done

    if [ $RETRIES -eq $MAX_RETRIES ]; then
        echo -e "${RED}Timeout waiting for PostgreSQL${NC}"
        return 1
    fi

    echo -e "${GREEN}PostgreSQL is ready${NC}"
    return 0
}

# Build and load the local image
echo "Building and loading local image..."
docker build -t leads-scraper-service:latest . || {
    echo -e "${RED}Failed to build Docker image${NC}"
    exit 1
}

kind load docker-image leads-scraper-service:latest --name gmaps-cluster || {
    echo -e "${RED}Failed to load image into kind cluster${NC}"
    exit 1
}

# Install/upgrade Helm chart with local PostgreSQL enabled
echo "Installing/upgrading Helm chart..."
helm upgrade --install "$RELEASE_NAME" ./charts/leads-scraper-service \
    --namespace "$NAMESPACE" \
    --create-namespace \
    --set postgresql.enabled=true \
    --set image.repository=leads-scraper-service \
    --set image.tag=latest \
    --set image.pullPolicy=IfNotPresent \
    --set tests.enabled=true || {
    echo -e "${RED}Failed to install/upgrade Helm chart${NC}"
    exit 1
}

# Debug information
echo "Current pods in namespace:"
kubectl get pods -n "$NAMESPACE"

# Check if helm release exists before proceeding
check_helm_release

echo "----------------------------------------"
echo "Helm release status:"
helm status "$RELEASE_NAME" -n "$NAMESPACE"

echo "----------------------------------------"
echo "Helm values:"
helm get values "$RELEASE_NAME" -n "$NAMESPACE"

echo "----------------------------------------"

# Wait for PostgreSQL to be ready
wait_for_postgres || {
    echo -e "${RED}Failed waiting for PostgreSQL${NC}"
    exit 1
}

# Check if tests are enabled - using a more precise yaml parsing
TESTS_ENABLED=$(helm get values "$RELEASE_NAME" --namespace "$NAMESPACE" -o yaml | awk '/^tests:/{f=1;next} f==1&&/^[^ ]/{f=0} f==1&&/enabled:/{print $2;exit}')

if [ -z "$TESTS_ENABLED" ] || [ "$TESTS_ENABLED" != "true" ]; then
    echo -e "${RED}Tests are not enabled in values.yaml. Please enable tests by setting tests.enabled=true${NC}"
    exit 1
fi

echo "Running Helm tests..."

# Wait for the main service to be ready
echo "Waiting for service to be ready..."
kubectl rollout status deployment "$RELEASE_NAME" -n "$NAMESPACE" --timeout=60s || {
    echo -e "${RED}Deployment rollout failed${NC}"
    kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/instance=$RELEASE_NAME"
    exit 1
}

# Wait for service endpoints to be ready
echo "Waiting for service endpoints..."
RETRIES=0
MAX_RETRIES=30
until kubectl get endpoints -n "$NAMESPACE" "$RELEASE_NAME" -o jsonpath='{.subsets[0].addresses[0].ip}' 2>/dev/null || [ $RETRIES -eq $MAX_RETRIES ]; do
    echo "Waiting for endpoints to be ready... ($RETRIES/$MAX_RETRIES)"
    sleep 2
    RETRIES=$((RETRIES + 1))
done

if [ $RETRIES -eq $MAX_RETRIES ]; then
    echo -e "${RED}Timeout waiting for service endpoints${NC}"
    kubectl get endpoints -n "$NAMESPACE" "$RELEASE_NAME" -o yaml
    exit 1
fi

# Give the service a moment to initialize
echo "Waiting for service to initialize..."
sleep 5

# Create a log directory with timestamp
LOG_DIR="helm-test-logs-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$LOG_DIR"

echo "Using command: helm test $RELEASE_NAME --namespace $NAMESPACE"
# Run helm test with detailed logging
if ! helm test "$RELEASE_NAME" --namespace "$NAMESPACE" --debug --logs > "$LOG_DIR/helm-test.log" 2>&1; then
    echo -e "${RED}✗ Tests failed${NC}"
    echo "Test output:"
    cat "$LOG_DIR/helm-test.log"
    
    echo "Collecting test pod logs..."
    for pod in $(kubectl get pods -n "$NAMESPACE" -l "helm.sh/hook=test" -o name); do
        echo "=== Logs for $pod ==="
        kubectl logs -n "$NAMESPACE" "$pod" > "$LOG_DIR/$(basename "$pod").log" 2>&1
        cat "$LOG_DIR/$(basename "$pod").log"
    done
    
    exit 1
fi

echo -e "${GREEN}✓ All tests passed${NC}"

# Get the health check path from values
HEALTH_PATH=$(helm get values "$RELEASE_NAME" --namespace "$NAMESPACE" -o yaml | awk '/healthCheck:/{f=1;next} f==1&&/^[^ ]/{f=0} f==1&&/path:/{print $2;exit}')
HEALTH_PATH=${HEALTH_PATH:-"/health"}

# Additional health check
echo "Checking service health endpoint at $HEALTH_PATH..."
POD_NAME=$(kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/instance=$RELEASE_NAME" -o jsonpath="{.items[0].metadata.name}")
if kubectl exec "$POD_NAME" -n "$NAMESPACE" -- wget -q -O - "http://localhost:8080${HEALTH_PATH}" | grep -q "ok"; then
    echo -e "${GREEN}✓ Health check passed${NC}"
else
    echo -e "${RED}✗ Health check failed${NC}"
    echo "Health check response:"
    kubectl exec "$POD_NAME" -n "$NAMESPACE" -- wget -q -O - "http://localhost:8080${HEALTH_PATH}"
    exit 1
fi

# Test PostgreSQL connection
echo "Testing PostgreSQL connection..."
APP_POD=$(kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/instance=$RELEASE_NAME" -o jsonpath="{.items[0].metadata.name}")
if kubectl exec "$APP_POD" -n "$NAMESPACE" -- wget -q -O - "http://localhost:8080/health" | grep -q "database.*ok"; then
    echo -e "${GREEN}✓ PostgreSQL connection test passed${NC}"
else
    echo -e "${RED}✗ PostgreSQL connection test failed${NC}"
    echo "Health check response:"
    kubectl exec "$APP_POD" -n "$NAMESPACE" -- wget -q -O - "http://localhost:8080/health"
    exit 1
fi 