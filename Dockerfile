# Build stage for Playwright dependencies
FROM golang:1.25.4-alpine AS builder

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
FROM debian:bullseye-slim

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

# Install Playwright and browsers
# We do this in the final stage or a separate stage to keep the image size optimized, 
# but we need the go binary to install playwright driver if we use the go library's install command.
# Alternatively, we can install the driver and browsers directly.

# Let's use the builder to install playwright driver and browsers to a temporary location, then copy them.
# Actually, it's easier to install them in the final image or a dedicated deps stage.
# Let's stick to the original multi-stage approach but fix the base image.

COPY --from=builder /usr/bin/brezel-api /usr/bin/

# Install Playwright driver and browsers
RUN PLAYWRIGHT_INSTALL_ONLY=1 brezel-api

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