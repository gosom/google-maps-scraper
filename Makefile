APP_NAME := google_maps_scraper
VERSION := 1.8.1

default: help

# generate help info from comments: thanks to https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html
help: ## help information about make commands
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

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

vuln: ## runs vulnerability checks
	go tool govulncheck -C . -show verbose -format text -scan symbol ./...

lint: ## runs the linter
	go tool golangci-lint -v run ./...

cross-compile: ## cross compiles the application
	GOOS=linux GOARCH=amd64 go build -o bin/$(APP_NAME)-${VERSION}-linux-amd64
	GOOS=darwin GOARCH=amd64 go build -o bin/$(APP_NAME)-${VERSION}-darwin-amd64
	GOOS=windows GOARCH=amd64 go build -o bin/$(APP_NAME)-${VERSION}-windows-amd64.exe

sec: ## runs gosec security scan
	gosec -conf .gosec.json ./...

sec-critical: ## runs gosec showing only critical issues
	gosec -conf .gosec.json -severity=critical ./...

sec-html: ## generates gosec HTML report
	gosec -conf .gosec.json -fmt html -out gosec-report.html ./...

check-env: ## enforce env-config boundary: no os.Getenv outside pkg/config and allowed exceptions
	@set -e; \
	direct=$$(grep -rn 'os\.Getenv\|os\.LookupEnv' --include='*.go' --exclude-dir='.claude' . \
	  | grep -v '_test.go' \
	  | grep -v ':[[:space:]]*//' \
	  | grep -v 'pkg/config/' \
	  | grep -v 'pkg/appenv/appenv\.go' \
	  | grep -v 'runner/runner\.go' \
	  | grep -v 'web/handlers/version\.go' \
	  | grep -v 'config/config\.go' \
	  | grep -v 'web/scrape\.go' \
	  || true); \
	helpers=$$(grep -rnE '\b(getEnv|getEnvOrDefault|envInt|envDuration|dbEnvInt|dbEnvDuration|parseCSVEnv|stripeWebhookSecretsFromEnv)\(' --include='*.go' --exclude-dir='.claude' . \
	  | grep -v '_test.go' \
	  | grep -v 'pkg/config/' \
	  | grep -v 'web/scrape\.go' \
	  | grep -v 'web/handlers/version\.go' \
	  || true); \
	if [ -n "$$direct" ] || [ -n "$$helpers" ]; then \
	  echo "FAIL: Env access found outside pkg/config boundary:"; \
	  echo "DIRECT:"; echo "$$direct"; \
	  echo "HELPERS:"; echo "$$helpers"; \
	  exit 1; \
	fi; \
	echo "✅ env-config boundary intact"
