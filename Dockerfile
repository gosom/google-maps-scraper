# Build stage for Playwright dependencies
FROM golang:1.25-bullseye AS playwright-deps

# Use Go proxy for faster downloads
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=sum.golang.org
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/browsers

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && go install github.com/playwright-community/playwright-go/cmd/playwright@latest \
    && mkdir -p /opt/browsers \
    && playwright install chromium --with-deps

# Build stage
FROM golang:1.25-bullseye AS builder

# Use Go proxy for faster downloads
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=sum.golang.org

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./

# Configure go env and download dependencies
RUN go env -w GOPROXY=https://proxy.golang.org,direct \
    && go mod download

# Copy source code
COPY . .

# Build the binary with proper name for Brezel.ai
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o /usr/bin/brezel-api .

# Final stage - optimized for API + scraping
FROM debian:bullseye-slim

ENV PLAYWRIGHT_BROWSERS_PATH=/opt/browsers
ENV PLAYWRIGHT_DRIVER_PATH=/opt

# Install runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    libnss3 \
    libnspr4 \
    libatk1.0-0 \
    libatk-bridge2.0-0 \
    libcups2 \
    libdrm2 \
    libdbus-1-3 \
    libxkbcommon0 \
    libatspi2.0-0 \
    libx11-6 \
    libxcomposite1 \
    libxdamage1 \
    libxext6 \
    libxfixes3 \
    libxrandr2 \
    libgbm1 \
    libpango-1.0-0 \
    libcairo2 \
    libasound2 \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Copy Playwright browsers and cache
COPY --from=playwright-deps /opt/browsers /opt/browsers
COPY --from=playwright-deps /root/.cache/ms-playwright-go /opt/ms-playwright-go

# Set proper permissions
RUN chmod -R 755 /opt/browsers \
    && chmod -R 755 /opt/ms-playwright-go

# Copy the application binary
COPY --from=builder /usr/bin/brezel-api /usr/bin/

# Copy migrations directory
COPY scripts/migrations /scripts/migrations

# Expose the web server port
EXPOSE 8080

# Health check for container orchestration
HEALTHCHECK --interval=30s --timeout=10s --start-period=40s --retries=3 \
  CMD curl -f http://localhost:8080/health || exit 1

# Default to web mode for API server (concurrency auto-detected)
ENTRYPOINT ["brezel-api"]
CMD ["-web"]