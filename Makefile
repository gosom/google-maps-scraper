APP_NAME := google_maps_scraper
VERSION := 1.8.2

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

build: ## builds the main binary
	go build -o google-maps-scraper

build-plugin: ## builds the streaming plugin
	@mkdir -p streaming_plugin
	@echo '//go:build plugin' > streaming_plugin/streaming_duplicate_filter.go
	@echo '' >> streaming_plugin/streaming_duplicate_filter.go
	@echo 'package main' >> streaming_plugin/streaming_duplicate_filter.go
	@echo '' >> streaming_plugin/streaming_duplicate_filter.go
	@echo 'import (' >> streaming_plugin/streaming_duplicate_filter.go
	@echo '	"github.com/gosom/google-maps-scraper/plugins"' >> streaming_plugin/streaming_duplicate_filter.go
	@echo '	"github.com/gosom/scrapemate"' >> streaming_plugin/streaming_duplicate_filter.go
	@echo ')' >> streaming_plugin/streaming_duplicate_filter.go
	@echo '' >> streaming_plugin/streaming_duplicate_filter.go
	@echo '// StreamingDuplicateFilterWriterFactory creates a new instance of the streaming duplicate filter' >> streaming_plugin/streaming_duplicate_filter.go
	@echo 'func StreamingDuplicateFilterWriterFactory() scrapemate.ResultWriter {' >> streaming_plugin/streaming_duplicate_filter.go
	@echo '	return plugins.NewStreamingDuplicateFilterWriter()' >> streaming_plugin/streaming_duplicate_filter.go
	@echo '}' >> streaming_plugin/streaming_duplicate_filter.go
	go build -buildmode=plugin -tags=plugin -o plugins/streaming_plugin.so streaming_plugin/streaming_duplicate_filter.go

build-all: build build-plugin ## builds both main binary and plugin

clean: ## removes build artifacts
	rm -f google-maps-scraper
	rm -f plugins/streaming_plugin.so
	rm -rf streaming_plugin/
	@echo "Build artifacts cleaned"

cross-compile: ## cross compiles the application
	GOOS=linux GOARCH=amd64 go build -o bin/$(APP_NAME)-${VERSION}-linux-amd64
	GOOS=darwin GOARCH=amd64 go build -o bin/$(APP_NAME)-${VERSION}-darwin-amd64
	GOOS=windows GOARCH=amd64 go build -o bin/$(APP_NAME)-${VERSION}-windows-amd64.exe
