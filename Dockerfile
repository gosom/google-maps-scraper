# Build stage for Playwright dependencies
FROM golang:1.25.9-alpine AS builder

# Set up Go environment
ENV PATH="/usr/local/go/bin:${PATH}"

# Use Go proxy for faster downloads
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=sum.golang.org

WORKDIR /app

# Build metadata (required from CI/CD)
ARG GIT_COMMIT
ARG BUILD_DATE
ARG VERSION

# Install CA certificates, wget, and git (needed for go mod download)
RUN apk add --no-cache ca-certificates wget git

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
FROM debian:bookworm-slim

# Runtime environment variables for version tracking (required from CI/CD)
ARG GIT_COMMIT
ARG BUILD_DATE
ARG VERSION
ENV GIT_COMMIT=${GIT_COMMIT}
ENV BUILD_DATE=${BUILD_DATE}
ENV VERSION=${VERSION}

ENV PLAYWRIGHT_BROWSERS_PATH=/opt/browsers
ENV PLAYWRIGHT_DRIVER_PATH=/opt

# Install runtime dependencies and Node.js for Playwright
RUN apt-get update && apt-get install -y --no-install-recommends \
  ca-certificates \
  curl \
  wget \
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
  && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
  && apt-get install -y --no-install-recommends nodejs \
  && apt-get clean \
  && rm -rf /var/lib/apt/lists/*

# Non-root runtime user. Created before Playwright install so the browser cache
# is owned by uid 1001 — Playwright fails opaquely when the cache UID differs
# from the running process. /opt is the PLAYWRIGHT_DRIVER_PATH, /opt/browsers
# is the PLAYWRIGHT_BROWSERS_PATH, /gmapsdata is the runtime data volume mount.
RUN groupadd --system --gid 1001 brezel \
  && useradd --system --uid 1001 --gid 1001 --no-create-home --shell /usr/sbin/nologin brezel \
  && mkdir -p /opt/browsers /gmapsdata /webdata /logs \
  && chown -R brezel:brezel /opt /gmapsdata /webdata /logs

COPY --from=builder --chown=brezel:brezel /usr/bin/brezel-api /usr/bin/

# Copy migrations directory (root-owned, world-readable — fine for the brezel runtime user)
COPY scripts/migrations /scripts/migrations

USER brezel:brezel

# Install Playwright driver and browsers as the brezel user so the cache UID
# matches the runtime UID.
RUN PLAYWRIGHT_INSTALL_ONLY=1 brezel-api

# Expose the web server port
EXPOSE 8080

# Health check for container orchestration
HEALTHCHECK --interval=30s --timeout=10s --start-period=40s --retries=3 \
  CMD curl -f http://localhost:8080/health || exit 1

# Default to web mode for API server (concurrency auto-detected)
ENTRYPOINT ["brezel-api"]
CMD ["-web"]
