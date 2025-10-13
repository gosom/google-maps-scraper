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

sec: ## runs gosec security scanner on entire codebase
	gosec -conf .gosec.json ./...

sec-web: ## runs gosec security scanner on web/API components
	gosec -conf .gosec.json ./web/... ./models/... ./billing/...

sec-db: ## runs gosec security scanner on database components
	gosec -conf .gosec.json ./postgres/...

sec-critical: ## runs gosec for critical issues only (SQL injection, XSS, hardcoded secrets)
	gosec -conf .gosec.json -include=G101,G201,G202,G203,G304,G401,G402 ./...

sec-json: ## runs gosec and outputs results in JSON format
	gosec -conf .gosec.json -fmt json -out gosec-report.json ./...

sec-html: ## runs gosec and generates HTML report
	gosec -conf .gosec.json -fmt html -out gosec-report.html ./...

sec-sarif: ## runs gosec and generates SARIF report for GitHub
	gosec -conf .gosec.json -fmt sarif -out gosec-report.sarif ./...

security-full: sec sec-json sec-html ## runs complete security audit with multiple report formats
	@echo "Security scan complete. Reports generated:"
	@echo "  - Console output (above)"
	@echo "  - JSON: gosec-report.json"
	@echo "  - HTML: gosec-report.html"

cross-compile: ## cross compiles the application
	GOOS=linux GOARCH=amd64 go build -o bin/$(APP_NAME)-${VERSION}-linux-amd64
	GOOS=darwin GOARCH=amd64 go build -o bin/$(APP_NAME)-${VERSION}-darwin-amd64
	GOOS=windows GOARCH=amd64 go build -o bin/$(APP_NAME)-${VERSION}-windows-amd64.exe
