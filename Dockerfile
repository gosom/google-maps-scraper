FROM golang:1.23.6-bullseye

# Install curl first, then install Node.js 18, Git, and ca-certificates.
RUN apt-get update && \
    apt-get install -y curl && \
    curl -fsSL https://deb.nodesource.com/setup_18.x | bash - && \
    apt-get install -y nodejs git ca-certificates && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy the entire project into the container
COPY . .

# Remove go.work if present to avoid version conflicts
RUN rm -f go.work

# Download dependencies and build the Go binary
RUN go mod download
RUN go build -o google-maps-scraper .

# Tell ms-playwright-go to use the system Node binary
ENV PLAYWRIGHT_NODE_PATH=/usr/bin/node

# Install all required OS dependencies for Playwright browsers
RUN apt-get update && apt-get install -y \
    libnss3 \
    libnspr4 \
    libdbus-1-3 \
    libatk1.0-0 \
    libatk-bridge2.0-0 \
    libcups2 \
    libdrm2 \
    libx11-6 \
    libxcomposite1 \
    libxdamage1 \
    libxext6 \
    libxfixes3 \
    libxrandr2 \
    libgbm1 \
    libxcb1 \
    libxkbcommon0 \
    libpango-1.0-0 \
    libcairo2 \
    libasound2 \
    libatspi2.0-0 && \
    rm -rf /var/lib/apt/lists/*

# Pre-install Playwright browsers
RUN npx playwright install

# Expose port 8080 for the web server
EXPOSE 8080

# Run the scraper in web mode
CMD ["./google-maps-scraper", "-web"]