APP_NAME := google_maps_scraper
VERSION := 1.6.1

# Helm/K8s related variables
CHART_NAME := leads-scraper-service
CHART_PATH := charts/$(CHART_NAME)
HELM_VALUES := $(CHART_PATH)/values.yaml
NAMESPACE := default
RELEASE_NAME := gmaps-scraper-leads-scraper-service

# Tool binaries
HELM := helm
KUBECTL := kubectl
YAMLLINT := yamllint
KUBEVAL := kubeval
KIND := kind

# Deployment variables
DOCKER_IMAGE := gosom/google-maps-scraper
DOCKER_TAG := $(VERSION)

# Combined deployment target that handles everything
.PHONY: deploy
deploy: check-requirements build-and-deploy ## Build and deploy the application to local kind cluster

# Check all requirements before starting deployment
.PHONY: check-requirements
check-requirements: check-docker check-required-tools
	@echo "✓ All requirements satisfied"

# Build and deploy process
.PHONY: build-and-deploy
build-and-deploy: docker-build deploy-local
	@echo "✓ Application deployed successfully"
	@echo "Access the application at http://localhost:8080"

# Helm test commands
.PHONY: helm-test
helm-test: ## Deploy with Helm tests enabled
	@echo "Deploying with tests enabled..."
	ENABLE_TESTS=true $(MAKE) deploy-local || exit 1
	@echo "Running Helm tests..."
	@echo "Waiting for deployment to stabilize..."
	sleep 5
	DEBUG=true ./scripts/test-deployment.sh

.PHONY: test-deployment
test-deployment: ## Run Helm tests after deployment
	@if [ "$(ENABLE_TESTS)" != "true" ]; then \
		echo "Tests are disabled. Set ENABLE_TESTS=true to run tests."; \
		exit 0; \
	fi
	./scripts/test-deployment.sh

default: help

# generate help info from comments: thanks to https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html
help: ## help information about make commands
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: check-docker
check-docker: ## Check if Docker Desktop is running and start it if not
	@if ! docker info > /dev/null 2>&1; then \
		echo "Docker Desktop is not running. Attempting to start it..."; \
		if [ "$(shell uname)" = "Darwin" ]; then \
			open -a Docker; \
			echo "Waiting for Docker to start..."; \
			while ! docker info > /dev/null 2>&1; do \
				sleep 1; \
			done; \
			echo "Docker Desktop is now running."; \
		else \
			echo "Please start Docker Desktop manually."; \
			exit 1; \
		fi \
	fi

# Tool installation and verification targets
.PHONY: install-tools
install-tools: ## Install required tools
	@mkdir -p script-runners
	@chmod +x script-runners/install-tools.sh
	@./script-runners/install-tools.sh

.PHONY: check-helm
check-helm: ## Check if helm is installed
	@which $(HELM) >/dev/null || (echo "helm is required but not installed. Run 'make install-tools' to install" && exit 1)

.PHONY: check-kubectl
check-kubectl: ## Check if kubectl is installed
	@which $(KUBECTL) >/dev/null || (echo "kubectl is required but not installed. Run 'make install-tools' to install" && exit 1)

.PHONY: check-yamllint
check-yamllint: ## Check if yamllint is installed
	@which $(YAMLLINT) >/dev/null || (echo "yamllint is required but not installed. Run 'make install-tools' to install" && exit 1)

.PHONY: check-kubeval
check-kubeval: ## Check if kubeval is installed
	@which $(KUBEVAL) >/dev/null || (echo "kubeval is required but not installed. Run 'make install-tools' to install" && exit 1)

.PHONY: check-kind
check-kind: ## Check if kind is installed
	@which $(KIND) >/dev/null || (echo "kind is required but not installed. Run 'make install-tools' to install" && exit 1)

.PHONY: check-required-tools
check-required-tools: check-helm check-kubectl check-yamllint check-kubeval check-kind # Check if required tools are installed

.PHONY: quick-validate
quick-validate: check-required-tools ## Run basic validations (faster)
	@mkdir -p script-runners
	@chmod +x script-runners/validate.sh
	@./script-runners/validate.sh $(CHART_PATH) $(RELEASE_NAME) $(NAMESPACE) --quick

.PHONY: dry-run
dry-run: check-helm ## Perform a dry-run installation
	$(HELM) install $(RELEASE_NAME) $(CHART_PATH) \
		--dry-run \
		--debug \
		--namespace $(NAMESPACE)

.PHONY: template-all
template-all: check-helm ## Generate all templates with default values
	@mkdir -p generated-manifests
	$(HELM) template $(RELEASE_NAME) $(CHART_PATH) > generated-manifests/all.yaml

# Helm chart validation targets
.PHONY: lint-chart
lint-chart: check-helm ## Lint the Helm chart
	$(HELM) lint $(CHART_PATH)

.PHONY: lint-yaml
lint-yaml: check-yamllint ## Lint all YAML files in the chart
	$(YAMLLINT) $(CHART_PATH)

.PHONY: validate-templates
validate-templates: check-helm ## Validate Helm templates
	$(HELM) template $(RELEASE_NAME) $(CHART_PATH) --debug

.PHONY: validate-manifests
validate-manifests: check-helm check-kubeval ## Validate generated Kubernetes manifests against schemas
	$(HELM) template $(RELEASE_NAME) $(CHART_PATH) | $(KUBEVAL) --strict

vet: ## runs go vet
	go vet ./...

format: ## runs go fmt
	gofmt -s -w .

test: ## runs the unit tests
	go test -v -race -timeout 5m ./...

test-cover: ## outputs the coverage statistics
	go test -v -race -timeout 5m ./... -coverprofile coverage.out
	go tool cover -func coverage.out
	rm coverage.out

test-cover-report: ## an html report of the coverage statistics
	go test -v ./... -covermode=count -coverpkg=./... -coverprofile coverage.out
	go tool cover -html coverage.out -o coverage.html
	open coverage.html

lint: ## runs the linter
	go generate -v ./lint.go

cross-compile: ## cross compiles the application
	GOOS=linux GOARCH=amd64 go build -o bin/$(APP_NAME)-${VERSION}-linux-amd64
	GOOS=darwin GOARCH=amd64 go build -o bin/$(APP_NAME)-${VERSION}-darwin-amd64
	GOOS=windows GOARCH=amd64 go build -o bin/$(APP_NAME)-${VERSION}-windows-amd64.exe

docker-build: ## builds the docker image
	docker build -t gosom/google-maps-scraper:${VERSION} .
	docker tag gosom/google-maps-scraper:${VERSION} gosom/google-maps-scraper:latest

docker-push: ## pushes the docker image to registry
	docker push gosom/google-maps-scraper:${VERSION}
	docker push gosom/google-maps-scraper:latest

docker-dev: ## starts development environment with postgres
	docker-compose -f docker-compose.dev.yaml up -d

docker-dev-down: ## stops development environment
	docker-compose -f docker-compose.dev.yaml down

docker-clean: ## removes all docker artifacts
	docker-compose -f docker-compose.dev.yaml down -v
	docker rmi gosom/google-maps-scraper:${VERSION} gosom/google-maps-scraper:latest || true

build: ## builds the executable
	go build -o bin/$(APP_NAME)

run: build ## builds and runs the application
	./bin/$(APP_NAME)

docker-run: docker-build ## builds and runs the docker container
	docker run -p 8080:8080 gosom/google-maps-scraper:${VERSION}

precommit: check-docker check-required-tools lint build docker-build test format vet quick-validate ## runs the precommit hooks

.PHONY: deploy-local
deploy-local: check-docker ## Deploy to local kind cluster
	./scripts/deploy-local.sh $(if $(ENABLE_TESTS),--enable-tests)

.PHONY: clean-local
clean-local: ## Clean up local deployment
	rm -rf helm-test-logs
	helm uninstall $(RELEASE_NAME) --namespace $(NAMESPACE) || true
	kind delete cluster --name gmaps-cluster || true

.PHONY: port-forward
port-forward: ## Port forward the service to localhost (only if needed)
	kubectl port-forward -n $(NAMESPACE) svc/$(RELEASE_NAME) 8080:8080
	open http://localhost:8080/api/docs