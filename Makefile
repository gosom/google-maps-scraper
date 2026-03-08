APP_NAME := google_maps_scraper
VERSION := 1.10.2

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

build: ## builds the application (default: playwright)
	go build -o bin/$(APP_NAME) .

build-rod: ## builds the application with go-rod browser engine
	go build -tags rod -o bin/$(APP_NAME)-rod .

cross-compile-rod: ## cross compiles the application with go-rod
	GOOS=linux GOARCH=amd64 go build -tags rod -o bin/$(APP_NAME)-${VERSION}-rod-linux-amd64
	GOOS=darwin GOARCH=amd64 go build -tags rod -o bin/$(APP_NAME)-${VERSION}-rod-darwin-amd64
	GOOS=windows GOARCH=amd64 go build -tags rod -o bin/$(APP_NAME)-${VERSION}-rod-windows-amd64.exe

docker: ## builds docker image with playwright (default)
	docker build -t $(APP_NAME):$(VERSION) .

docker-rod: ## builds docker image with go-rod
	docker build -f Dockerfile.rod -t $(APP_NAME):$(VERSION)-rod .

# --- SaaS targets ---

build-saas: ## builds the SaaS binary (API server, worker, admin)
	go build -o bin/gmapssaas ./cmd/gmapssaas/

docker-saas: ## builds docker image for SaaS
	docker build -f Dockerfile.saas -t gmapssaas:$(VERSION) .

SAAS_IMAGE ?= ghcr.io/gosom/google-maps-scraper-saas:latest

saas-docker-push: docker-saas ## builds and pushes the SaaS docker image
	docker tag gmapssaas:$(VERSION) $(SAAS_IMAGE)
	docker push $(SAAS_IMAGE)

provision: ## run the provisioning wizard via Docker (state persisted to ~/.gmapssaas)
	docker run --rm -it \
	  -v "$(HOME)/.gmapssaas:/root/.gmapssaas" \
	  -v /var/run/docker.sock:/var/run/docker.sock \
	  -v "$(HOME)/.ssh:/root/.ssh:ro" \
	  gmapssaas:$(VERSION) provision

saas-dev: ## start SaaS development environment (postgres + migrations + admin user + hot reload)
	@docker compose -f docker-compose.saas.yaml up -d postgres
	@echo "Waiting for postgres..."
	@until docker compose -f docker-compose.saas.yaml exec -T postgres pg_isready -U postgres > /dev/null 2>&1; do sleep 1; done
	@echo "Running migrations..."
	@sql-migrate up -config=migrations/dbconfig.yml
	@echo "Creating admin user..."
	@go run ./cmd/gmapssaas admin create-user -u admin -p '1234#abcd'
	@echo "Starting server with hot reload on :8080..."
	@air

saas-dev-stop: ## stop SaaS development environment
	@docker compose -f docker-compose.saas.yaml down

saas-dev-reset: ## reset SaaS development environment (drops all data)
	@docker compose -f docker-compose.saas.yaml down -v

saas-run-server: ## run the SaaS API server locally
	@go run ./cmd/gmapssaas serve

saas-run-worker: ## run the SaaS worker locally
	@go run ./cmd/gmapssaas worker

saas-provision: ## run infrastructure provisioning wizard
	@go run ./cmd/gmapssaas provision

saas-migrate-up: ## run all pending SaaS database migrations
	@sql-migrate up -config=migrations/dbconfig.yml

saas-migrate-down: ## rollback the last SaaS migration
	@sql-migrate down -config=migrations/dbconfig.yml -limit=1

saas-migrate-status: ## show SaaS migration status
	@sql-migrate status -config=migrations/dbconfig.yml

saas-migrate-new: ## create a new SaaS migration (usage: make saas-migrate-new name=xxx)
	@if [ -z "$(name)" ]; then echo "Error: name required. Usage: make saas-migrate-new name=xxx"; exit 1; fi
	@sql-migrate new -config=migrations/dbconfig.yml $(name)

saas-gen: ## regenerate swagger docs for the SaaS API
	@swag init -g api/doc.go -o api/docs

gen: saas-gen ## generate swagger docs

saas-psql: ## connect to SaaS development database
	PGPASSWORD=postgres psql -h localhost -p 5432 -U postgres gmapssaas

clean: ## clean build artifacts
	@rm -rf bin/ tmp/
