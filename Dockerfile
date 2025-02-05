# Build stage: use an Alpine-based Go image to compile the binary
FROM golang:1.23.6-alpine AS builder

# Install git for module downloads
RUN apk add --no-cache git

WORKDIR /app
# Copy the project source code
COPY . .

# Remove go.work (which contains an invalid Go version format for this build)
RUN rm -f go.work

# Download dependencies and build the binary
RUN go mod download
RUN go build -o google-maps-scraper .

# Final stage: use a minimal Alpine image for runtime
FROM alpine:latest

WORKDIR /app
# Copy the compiled binary from the builder stage
COPY --from=builder /app/google-maps-scraper .

# Expose port 8080 (the port the web server listens on)
EXPOSE 8080

# Run the scraper in web mode
CMD ["./google-maps-scraper", "-web"]
